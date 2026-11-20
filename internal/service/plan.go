package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/systemd"
)

// installInputs are the resolved, side-effect-free facts an install is built
// from. Shared by Install (the executor) and Plan (the --dry-run view) so the
// two cannot disagree about mode, paths, or the proxy command line.
type installInputs struct {
	topology   topology
	runtimeDir config.RuntimeDir
	configPath config.File
	execStart  string // proxy unit ExecStart
}

func resolveInstallInputs(opts Options) (installInputs, error) {
	topology, err := opts.Mode.topology(opts.Backend)
	if err != nil {
		return installInputs{}, err
	}

	runtimeDir, err := config.LookupRuntimeDir()
	if err != nil {
		return installInputs{}, err
	}

	self, err := os.Executable()
	if err != nil {
		return installInputs{}, fmt.Errorf("find executable: %w", err)
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return installInputs{}, fmt.Errorf("resolve executable: %w", err)
	}

	source, err := opts.ConfigPath.OrDefault()
	if err != nil {
		return installInputs{}, err
	}

	return installInputs{
		topology:   topology,
		runtimeDir: runtimeDir,
		configPath: source.File,
		execStart:  strings.Join(append([]string{self}, config.ServeArgs(source.File)...), " "),
	}, nil
}

// A Change is one concrete action an install will perform: a file write, a
// backup/restore, a removal, a command, or a signal.
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
	files := takeoverFiles{
		mask:   filepath.Join(dbusDir, dbusServiceFile),
		dropIn: filepath.Join(unitsDir, gkDropInDirName, gkDropInName),
		shadow: filepath.Join(asDir, gkAutostartName),
	}

	changes := []Change{
		{"write", in.configPath.String(), "set serve topology: " + opts.Mode.String() + " (other settings preserved)"},
	}
	changes = append(changes, in.topology.planPrepare(files)...)

	needed, err := in.topology.units(in.execStart)
	if err != nil {
		return nil, err
	}
	for _, u := range allUnitNames {
		path := u.Path(unitsDir)
		if content, ok := needed[u]; ok {
			note := ""
			if u == backendServiceUnit {
				// Surface the resolved backend — the one non-obvious content choice.
				note = "private backend: " + execStartOf(content)
			}
			changes = append(changes, Change{"write", path, note})
		} else if _, err := os.Stat(path); err == nil {
			changes = append(changes, Change{"remove", path, "stale unit from a previous mode (stopped and disabled first)"})
		}
	}

	changes = append(changes, daemonReloadChange())
	for _, u := range in.topology.activated() {
		changes = append(changes, systemctlChange(verbEnable, u, ""))
	}
	if opts.Start {
		for _, u := range in.topology.activated() {
			changes = append(changes, systemctlChange(verbStart, u, ""))
		}
	}
	return changes, nil
}

// Returns the change that runs `systemctl --user verb u`.
func systemctlChange(verb systemctlVerb, u systemd.Unit, note string) Change {
	return Change{"run", "systemctl --user " + string(verb) + " " + string(u), note}
}

// Returns the change that runs `systemctl --user daemon-reload`.
func daemonReloadChange() Change {
	return Change{"run", "systemctl --user " + string(verbDaemonReload), ""}
}

// execStartOf extracts the ExecStart= command from a unit file body.
func execStartOf(content string) string {
	for line := range strings.Lines(content) {
		if cmd, ok := strings.CutPrefix(line, "ExecStart="); ok {
			return strings.TrimSpace(cmd)
		}
	}
	return "?"
}

// Prints the changes that Install(opts) would make.
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
