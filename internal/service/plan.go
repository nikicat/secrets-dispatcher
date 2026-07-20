package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nikicat/secrets-dispatcher/internal/config"
)

// installInputs are the resolved, side-effect-free facts an install is built
// from. Shared by Install (the executor) and Plan (the --dry-run view) so the
// two cannot disagree about mode, paths, or the proxy command line.
type installInputs struct {
	mode       string
	runtimeDir string
	configPath string
	execStart  string // proxy unit ExecStart
}

func resolveInstallInputs(opts Options) (installInputs, error) {
	mode := opts.Mode
	if mode == "" {
		mode = "remote"
	}
	switch mode {
	case "remote", "local", "full":
	default:
		return installInputs{}, fmt.Errorf("unknown mode %q (must be remote, local, or full)", mode)
	}

	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		return installInputs{}, fmt.Errorf("XDG_RUNTIME_DIR must be set")
	}

	self, err := os.Executable()
	if err != nil {
		return installInputs{}, fmt.Errorf("find executable: %w", err)
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return installInputs{}, fmt.Errorf("resolve executable: %w", err)
	}

	configPath := opts.ConfigPath
	if configPath == "" {
		configPath = config.DefaultPath()
		if configPath == "" {
			return installInputs{}, fmt.Errorf("cannot determine config path (is HOME set?)")
		}
	}

	return installInputs{
		mode:       mode,
		runtimeDir: runtimeDir,
		configPath: configPath,
		execStart:  self + " serve --config " + configPath,
	}, nil
}

// buildUnits returns the unit files (name -> content) a mode installs.
// provider drives the detection-driven backend default (resolveBackendExec).
func buildUnits(mode, execStart string, provider Provider, backendPath string) (map[string]string, error) {
	needed := make(map[string]string)
	switch mode {
	case "remote":
		needed[unitFileName] = fmt.Sprintf(unitTemplate, execStart)
	case "local", "full":
		dbusDaemon, err := lookPathFunc("dbus-daemon")
		if err != nil {
			return nil, fmt.Errorf("find dbus-daemon: %w", err)
		}
		backendExec, err := resolveBackendExec(backendPath, provider)
		if err != nil {
			return nil, err
		}
		needed["secrets-dispatcher-bus.socket"] = busSocketTemplate
		needed["secrets-dispatcher-bus.service"] = fmt.Sprintf(busServiceTemplate, dbusDaemon)
		needed["secrets-dispatcher-backend.service"] = fmt.Sprintf(backendServiceTemplate, backendExec)
		needed[unitFileName] = fmt.Sprintf(localProxyTemplate, execStart)
	}
	return needed, nil
}

// A Change is one concrete action an install will perform: a file write, a
// backup/restore, a removal, a command, or a signal. Plan returns the exact
// list so --dry-run (US-1) can show what would happen without doing it.
type Change struct {
	Op     string // "write", "backup", "restore", "remove", "run", "signal"
	Target string
	Note   string
}

func (c Change) String() string {
	// Pad the plain verb to width first, then colorize — the escape bytes must
	// not count toward the column width or the targets misalign.
	s := cInfo(fmt.Sprintf("%-7s", c.Op)) + " " + c.Target
	if c.Note != "" {
		s += cDim("  # " + c.Note)
	}
	return s
}

// Plan computes the exact changes Install(opts) would make, in execution
// order, without making any. It reads current state (name owner, existing
// files) but never writes. The unit contents and paths come from the same
// helpers Install uses; only the sequence is mirrored here — keep it in sync
// with Install.
func Plan(opts Options) ([]Change, error) {
	in, err := resolveInstallInputs(opts)
	if err != nil {
		return nil, err
	}

	unitsDir, err := unitDir()
	if err != nil {
		return nil, err
	}
	dbusDir, err := dbusServiceDir()
	if err != nil {
		return nil, err
	}
	asDir, err := autostartDir()
	if err != nil {
		return nil, err
	}
	maskPath := filepath.Join(dbusDir, dbusServiceFile)
	dropIn := filepath.Join(unitsDir, gkDropInDirName, gkDropInName)
	shadow := filepath.Join(asDir, gkAutostartName)

	changes := []Change{
		{"write", in.configPath, "set serve topology: " + in.mode + " (other settings preserved)"},
	}

	var provider Provider
	switch in.mode {
	case "local", "full":
		provider = DetectProvider()

		// maskDBusActivation
		if existing, err := os.ReadFile(maskPath); err == nil && string(existing) != dbusActivationMask {
			changes = append(changes, Change{"backup", maskPath, "-> " + maskPath + dbusBackupSuffix})
		}
		changes = append(changes, Change{"write", maskPath, "mask D-Bus activation of org.freedesktop.secrets"})

		// demoteGnomeKeyring
		if provider.Kind == ProviderGnomeKeyring {
			changes = append(changes, Change{"write", dropIn, "demote gnome-keyring to pkcs11-only (secrets move to the private backend)"})
			if existing, err := os.ReadFile(shadow); err == nil && string(existing) != gkAutostartShadow {
				changes = append(changes, Change{"backup", shadow, "-> " + shadow + dbusBackupSuffix})
			}
			changes = append(changes, Change{"write", shadow, "hide the gnome-keyring secrets autostart kicker (Hidden=true)"})
			changes = append(changes,
				Change{"run", "systemctl --user daemon-reload", ""},
				Change{"run", "systemctl --user try-restart " + gkServiceUnit, "restart without the secrets component"})
		}

		// stopDBusActivatedService
		if provider.PID > 0 && provider.Kind != ProviderDispatcher {
			changes = append(changes, Change{"signal", fmt.Sprintf("SIGTERM -> pid %d (%s)", provider.PID, provider.Kind), "current owner of org.freedesktop.secrets"})
		}
	case "remote":
		// unmaskDBusActivation
		if existing, err := os.ReadFile(maskPath); err == nil && string(existing) == dbusActivationMask {
			if _, err := os.Stat(maskPath + dbusBackupSuffix); err == nil {
				changes = append(changes, Change{"restore", maskPath + dbusBackupSuffix, "-> " + maskPath})
			} else {
				changes = append(changes, Change{"remove", maskPath, "D-Bus activation mask"})
			}
		}
		// restoreGnomeKeyring
		restored := false
		if _, err := os.Stat(dropIn); err == nil {
			changes = append(changes, Change{"remove", dropIn, "gnome-keyring demotion drop-in"})
			restored = true
		}
		if existing, err := os.ReadFile(shadow); err == nil && string(existing) == gkAutostartShadow {
			changes = append(changes, Change{"remove", shadow, "autostart shadow"})
			if _, err := os.Stat(shadow + dbusBackupSuffix); err == nil {
				changes = append(changes, Change{"restore", shadow + dbusBackupSuffix, "-> " + shadow})
			}
			restored = true
		}
		if restored {
			changes = append(changes,
				Change{"run", "systemctl --user daemon-reload", ""},
				Change{"run", "systemctl --user try-restart " + gkServiceUnit, "restart with all components"})
		}
	}

	needed, err := buildUnits(in.mode, in.execStart, provider, opts.BackendPath)
	if err != nil {
		return nil, err
	}
	for _, name := range allUnitNames {
		path := filepath.Join(unitsDir, name)
		if content, ok := needed[name]; ok {
			note := ""
			if name == "secrets-dispatcher-backend.service" {
				// Surface the resolved backend — the one non-obvious content choice.
				note = "private backend: " + execStartOf(content)
			}
			changes = append(changes, Change{"write", path, note})
		} else if _, err := os.Stat(path); err == nil {
			changes = append(changes, Change{"remove", path, "stale unit from a previous mode (stopped and disabled first)"})
		}
	}

	changes = append(changes, Change{"run", "systemctl --user daemon-reload", ""})
	if in.mode != "remote" {
		changes = append(changes, Change{"run", "systemctl --user enable secrets-dispatcher-bus.socket", ""})
	}
	changes = append(changes, Change{"run", "systemctl --user enable " + unitFileName, ""})
	if opts.Start {
		if in.mode != "remote" {
			changes = append(changes, Change{"run", "systemctl --user start secrets-dispatcher-bus.socket", ""})
		}
		changes = append(changes, Change{"run", "systemctl --user start " + unitFileName, ""})
	}
	return changes, nil
}

// execStartOf extracts the ExecStart= command from a unit file body.
func execStartOf(unit string) string {
	for line := range strings.Lines(unit) {
		if cmd, ok := strings.CutPrefix(line, "ExecStart="); ok {
			return strings.TrimSpace(cmd)
		}
	}
	return "?"
}

// PrintPlan prints the changes Install(opts) would make (--dry-run, US-1).
func PrintPlan(opts Options) error {
	changes, err := Plan(opts)
	if err != nil {
		return err
	}
	fmt.Println("Dry run — `service install` would:")
	for _, c := range changes {
		fmt.Println("  " + c.String())
	}
	return nil
}
