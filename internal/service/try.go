package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/config"
)

type TryOptions struct {
	// The config file that the trial copies its settings from. Try never writes
	// to it (README, "Temporary — try it").
	ConfigPath config.Path
	Backend    Backend
	// DryRun prints the changes the trial would make, without making any.
	DryRun bool
	// How much of the install and the restore Try prints.
	Narration Narration
}

// Puts secrets-dispatcher in front of the current Secret Service for as long
// as ctx lives, then restores the original setup exactly.
func Try(ctx context.Context, opts TryOptions) error {
	unitsDir, err := unitDir()
	if err != nil {
		return err
	}
	for _, u := range allUnitNames {
		if _, err := os.Stat(u.Path(unitsDir)); err == nil {
			return fmt.Errorf("%s is already installed — the trial would remove it on exit; run `secrets-dispatcher service uninstall` first (see `service status`)", u)
		}
	}

	runtimeDir, err := config.LookupRuntimeDir()
	if err != nil {
		return err
	}
	trialConfig := runtimeDir.TrialConfig()
	base, err := opts.ConfigPath.OrDefault()
	if err != nil {
		return err
	}
	baseConfig := base.File
	h := host{narration: opts.Narration}
	installOpts := Options{ConfigPath: trialConfig.Path(), Start: true, Mode: ModeLocal, Backend: opts.Backend, Narration: opts.Narration}

	if opts.DryRun {
		changes, err := Plan(installOpts)
		if err != nil {
			return err
		}
		printTrialPlan(baseConfig, trialConfig, changes)
		return nil
	}

	// Remember the provider being displaced so the restore can confirm it is
	// back. Install re-detects (and prints) right after; both see the same
	// pre-takeover state.
	orig := DetectProvider()

	if err := baseConfig.CopyTo(trialConfig); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("copy the base config to the trial: %w", err)
	}

	if err := Install(installOpts); err != nil {
		fmt.Println(cErr("Install failed — reverting partial changes…"))
		if restoreErr := h.restoreTrial(trialConfig, orig); restoreErr != nil {
			fmt.Printf("%s restore after failed install: %v\n", cWarn("WARNING:"), restoreErr)
		}
		return err
	}

	announceTrial(trialConfig)
	watchOwnership(ctx)

	fmt.Println()
	fmt.Println(cDim("Stopping the trial — restoring the original Secret Service…"))
	return h.restoreTrial(trialConfig, orig)
}

func printTrialPlan(baseConfig, trialConfig config.File, changes []Change) {
	fmt.Println(cBold("Dry run — the trial would:"))
	fmt.Printf("  %s\n", Change{"copy", fmt.Sprintf("%s -> %s", baseConfig, trialConfig), "trial config; your config is never modified"})
	for _, c := range changes {
		fmt.Println("  " + c.String())
	}
	fmt.Println(cDim("On Ctrl-C every change is reverted: units stopped and removed, D-Bus"))
	fmt.Println(cDim("activation and gnome-keyring restored, trial config deleted."))
}

// Prints the web UI address from the config at trialConfig, or a warning when
// that config does not load, and the commands that exercise the trial.
func announceTrial(trialConfig config.File) {
	fmt.Println()
	if listen, err := listenAddr(trialConfig); err != nil {
		fmt.Printf("%s Web UI address unknown: %v\n", cWarn("WARNING:"), err)
	} else {
		fmt.Printf("%s Web UI: %s\n", cOK("✓"), cBold(listen.BaseURL()))
	}
	fmt.Printf("%s See it work (requests ask for approval in the web UI unless a rule matches):\n", cInfo("→"))
	fmt.Printf("    %s   %s\n", cBold("secret-tool store --label=demo service demo"), cDim("# then:"))
	fmt.Printf("    %s\n", cBold("secret-tool lookup service demo"))
	fmt.Printf("%s stops the trial and fully restores the original setup.\n", cBold("Ctrl-C"))
}

// Returns the address serve listens on under the config file at path.
func listenAddr(path config.File) (config.ListenAddr, error) {
	cfg, err := path.Load()
	if err != nil {
		return "", err
	}
	if cfg, err = cfg.WithDefaults(); err != nil {
		return "", err
	}
	return cfg.Listen, nil
}

// watchOwnership blocks until ctx is done, reporting when the dispatcher
// loses (or re-gains) org.freedesktop.secrets — the live re-grab warning.
func watchOwnership(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	inFront := true
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			p := DetectProvider()
			if nowInFront := p.Kind == ProviderDispatcher; nowInFront != inFront {
				inFront = nowInFront
				if inFront {
					fmt.Println(cOK("org.freedesktop.secrets is owned by secrets-dispatcher again"))
				} else {
					fmt.Printf("%s lost org.freedesktop.secrets to %s — a provider re-grabbed it\n", cWarn("WARNING:"), p)
				}
			}
		}
	}
}

// restoreTrial reverses the trial: full uninstall, drop the throwaway config,
// and confirm the displaced provider is back in front.
func (h host) restoreTrial(trialConfig config.File, orig Provider) error {
	err := Uninstall(h.narration)
	if rmErr := trialConfig.Remove(); rmErr != nil && err == nil {
		err = fmt.Errorf("remove trial config: %w", rmErr)
	}
	pokeSecretsActivation()
	if orig.Kind != ProviderNone && orig.Kind != ProviderDispatcher {
		if p, ok := waitForOwner(orig.Kind, ownerWaitTimeout); ok {
			fmt.Printf("%s Restored: org.freedesktop.secrets is owned by %s again\n", cOK("✓"), p)
			return err
		}
		fmt.Printf("%s %s has not re-taken org.freedesktop.secrets yet — it returns on next access or login\n", cDim("note:"), orig.Kind)
		return err
	}
	fmt.Println(cOK("✓") + " Restored: all trial state removed")
	return err
}

// pokeSecretsActivation touches org.freedesktop.secrets so D-Bus activation
// (just unmasked) starts the stock provider without waiting for its next
// client. The call exists for its side effect; errors are irrelevant.
func pokeSecretsActivation() {
	_, _ = execOutputFunc("busctl", "--user", "--timeout=10", "call",
		"org.freedesktop.secrets", "/org/freedesktop/secrets",
		"org.freedesktop.DBus.Properties", "Get",
		"ss", "org.freedesktop.Secret.Service", "Collections")
}
