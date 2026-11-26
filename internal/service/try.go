package service

import (
	"context"
	"fmt"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/config"
)

type TryOptions struct {
	// The config file that the trial copies its settings from. Try never writes
	// to it (README, "Temporary — try it").
	ConfigPath config.Path
	// The private backend, as Options.Backend.
	Backend *Backend
	// DryRun prints the changes the trial would make, without making any.
	DryRun bool
	// How much of the install and the restore Try prints.
	Narration Narration
}

// Puts secrets-dispatcher in front of the current Secret Service for as long
// as ctx lives, then restores the original setup exactly.
func Try(ctx context.Context, opts TryOptions) error {
	l, err := locateLayout()
	if err != nil {
		return err
	}
	if installed := l.installed(); len(installed) > 0 {
		return fmt.Errorf("%s is already installed — the trial would remove it on exit; run %s first (see %s)", installed[0], UninstallCommand.hint(), StatusCommand.hint())
	}

	runtimeDir, err := config.LookupRuntimeDir()
	if err != nil {
		return err
	}
	trialFile := runtimeDir.TrialConfig()
	in, err := resolveInstallInputs(ctx, Options{Config: trialConfig{opts.ConfigPath, trialFile}, Start: true, Mode: new(ModeLocal), Backend: opts.Backend})
	if err != nil {
		return err
	}
	p, err := l.plan(in)
	if err != nil {
		return err
	}

	if opts.DryRun {
		printTrialPlan(changesOf(p.steps))
		return nil
	}

	// Remember the provider being displaced so the restore can confirm it is
	// back. No step has run yet, so this is the pre-takeover state.
	t := trial{layout: l, config: trialFile, narration: opts.Narration, orig: DetectProvider(ctx)}

	// Ctrl-C has cancelled ctx by now, and the restore must still run.
	restoreCtx := context.WithoutCancel(ctx)
	if err := p.run(ctx, opts.Narration); err != nil {
		fmt.Println(cErr("Install failed — reverting partial changes…"))
		if restoreErr := t.restore(restoreCtx); restoreErr != nil {
			fmt.Printf("%s restore after failed install: %v\n", cWarn("WARNING:"), restoreErr)
		}
		return err
	}

	t.announce()
	watchOwnership(ctx)

	fmt.Println()
	fmt.Println(cDim("Stopping the trial — restoring the original Secret Service…"))
	return t.restore(restoreCtx)
}

// A takeover that is undone when the trial ends, so the user's setup comes
// back exactly (ADR 0009).
type trial struct {
	layout    layout
	config    config.File
	narration Narration
	orig      Provider
}

type trialConfig struct {
	base  config.Path
	trial config.File
}

func (c trialConfig) Draft() (config.Draft, error) {
	draft, err := c.base.Draft()
	if err != nil {
		return config.Draft{}, err
	}
	return draft.To(c.trial), nil
}

func printTrialPlan(changes []Change) {
	printChanges(cBold("Dry run — the trial would:"), changes)
	fmt.Println(cDim("On Ctrl-C every change is reverted: units stopped and removed, D-Bus"))
	fmt.Println(cDim("activation and gnome-keyring restored, trial config deleted."))
}

// Prints the trial's web UI address and the commands that exercise the
// trial. When the trial's config fails to load, prints a warning instead of
// the address.
func (t trial) announce() {
	fmt.Println()
	if listen, err := listenAddr(t.config); err != nil {
		fmt.Printf("%s Web UI address unknown: %v\n", cWarn("WARNING:"), err)
	} else {
		fmt.Printf("%s Web UI: %s\n", cOK("✓"), cBold(listen.BaseURL().String()))
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
	settings, err := cfg.WithDefaults()
	if err != nil {
		return "", err
	}
	return settings.Listen, nil
}

// Blocks until ctx is done. Prints a warning each time the dispatcher loses
// org.freedesktop.secrets, and a notice each time it gets the name back.
func watchOwnership(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	inFront := true
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			p := DetectProvider(ctx)
			if nowInFront := p.is(ProviderDispatcher); nowInFront != inFront {
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

// Uninstalls the trial and deletes its config, which gives the Secret Service
// back to the provider it displaced. Errors when either of those fails. A
// provider that has not taken org.freedesktop.secrets back yet is no error.
func (t trial) restore(ctx context.Context) error {
	err := t.layout.uninstall(ctx, t.narration)
	if rmErr := t.config.Remove(); rmErr != nil && err == nil {
		err = fmt.Errorf("remove trial config: %w", rmErr)
	}
	// D-Bus activation, just unmasked, starts the stock provider now instead
	// of at its next client.
	_ = session.pokeSecrets(ctx)
	if o := t.orig.displaced(); o != nil {
		if p := waitForOwner(ctx, o.Kind()); p.is(o.Kind()) {
			fmt.Printf("%s Restored: org.freedesktop.secrets is owned by %s again\n", cOK("✓"), p)
			return err
		}
		fmt.Printf("%s %s has not re-taken org.freedesktop.secrets yet — it returns on next access or login\n", cDim("note:"), o.Kind())
		return err
	}
	fmt.Println(cOK("✓") + " Restored: all trial state removed")
	return err
}
