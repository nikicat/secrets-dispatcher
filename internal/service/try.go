package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/config"
)

// TryOptions configures the reversible trial (US-9).
type TryOptions struct {
	// ConfigPath is the base config the trial copies its settings (rules,
	// listen address) from. Default: config.DefaultPath(). Never modified.
	ConfigPath string
	// BackendPath selects the private backend, same forms as Options.BackendPath.
	BackendPath string
	// DryRun prints the changes the trial would make, without making any.
	DryRun bool
}

// trialConfigPath returns the throwaway config used for the trial. It lives
// under XDG_RUNTIME_DIR so even a crashed trial leaves nothing after reboot.
func trialConfigPath() (string, error) {
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		return "", fmt.Errorf("XDG_RUNTIME_DIR must be set")
	}
	return filepath.Join(runtimeDir, "secrets-dispatcher", "try-config.yaml"), nil
}

// Try puts secrets-dispatcher in front of the current Secret Service for as
// long as ctx lives, then restores the original setup exactly (US-9). It
// composes the proven primitives: Install (local mode, detection-driven
// backend) against a throwaway config, an ownership watch, and Uninstall.
func Try(ctx context.Context, opts TryOptions) error {
	unitsDir, err := unitDir()
	if err != nil {
		return err
	}
	for _, name := range allUnitNames {
		if _, err := os.Stat(filepath.Join(unitsDir, name)); err == nil {
			return fmt.Errorf("%s is already installed — the trial would remove it on exit; run `secrets-dispatcher service uninstall` first (see `service status`)", name)
		}
	}

	trialConfig, err := trialConfigPath()
	if err != nil {
		return err
	}
	baseConfig := opts.ConfigPath
	if baseConfig == "" {
		baseConfig = config.DefaultPath()
	}
	installOpts := Options{ConfigPath: trialConfig, Start: true, Mode: "local", BackendPath: opts.BackendPath}

	if opts.DryRun {
		changes, err := Plan(installOpts)
		if err != nil {
			return err
		}
		fmt.Println(cBold("Dry run — the trial would:"))
		fmt.Printf("  %s\n", Change{"copy", baseConfig + " -> " + trialConfig, "trial config; your config is never modified"})
		for _, c := range changes {
			fmt.Println("  " + c.String())
		}
		fmt.Println(cDim("On Ctrl-C every change is reverted: units stopped and removed, D-Bus"))
		fmt.Println(cDim("activation and gnome-keyring restored, trial config deleted."))
		return nil
	}

	// Remember the provider being displaced so the restore can confirm it is
	// back. Install re-detects (and prints) right after; both see the same
	// pre-takeover state.
	orig := DetectProvider()

	if data, err := os.ReadFile(baseConfig); err == nil {
		if err := os.MkdirAll(filepath.Dir(trialConfig), 0700); err != nil {
			return fmt.Errorf("create trial config dir: %w", err)
		}
		if err := os.WriteFile(trialConfig, data, 0600); err != nil {
			return fmt.Errorf("write trial config: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read config %s: %w", baseConfig, err)
	}

	if err := Install(installOpts); err != nil {
		fmt.Println(cErr("Install failed — reverting partial changes…"))
		if restoreErr := restoreTrial(trialConfig, orig); restoreErr != nil {
			fmt.Printf("%s restore after failed install: %v\n", cWarn("WARNING:"), restoreErr)
		}
		return err
	}

	listen := config.DefaultListenAddr
	if cfg, err := config.Load(trialConfig); err == nil && cfg.Listen != "" {
		listen = cfg.Listen
	}
	fmt.Println()
	fmt.Printf("%s Web UI: %s\n", cOK("✓"), cBold("http://"+listen))
	fmt.Printf("%s See it work (requests ask for approval in the web UI unless a rule matches):\n", cInfo("→"))
	fmt.Printf("    %s   %s\n", cBold("secret-tool store --label=demo service demo"), cDim("# then:"))
	fmt.Printf("    %s\n", cBold("secret-tool lookup service demo"))
	fmt.Printf("%s stops the trial and fully restores the original setup.\n", cBold("Ctrl-C"))

	watchOwnership(ctx)

	fmt.Println()
	fmt.Println(cDim("Stopping the trial — restoring the original Secret Service…"))
	return restoreTrial(trialConfig, orig)
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
func restoreTrial(trialConfig string, orig Provider) error {
	err := Uninstall()
	if rmErr := os.Remove(trialConfig); rmErr != nil && !os.IsNotExist(rmErr) && err == nil {
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
