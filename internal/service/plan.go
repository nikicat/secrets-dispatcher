package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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
	execStart  systemd.Command // proxy unit ExecStart
}

func resolveInstallInputs(ctx context.Context, opts Options) (installInputs, error) {
	topology, err := opts.Mode.topology(ctx, opts.Backend)
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
		execStart:  systemd.NewCommand(self, config.ServeArgs(source.File)...),
	}, nil
}

// One action that an install performs, as the dry run prints it. A unit
// manager call prints as the systemctl command that does the same.
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
func Plan(ctx context.Context, opts Options) ([]Change, error) {
	in, err := resolveInstallInputs(ctx, opts)
	if err != nil {
		return nil, err
	}

	unitsDir, err := unitDir()
	if err != nil {
		return nil, err
	}
	files, err := locateTakeoverFiles()
	if err != nil {
		return nil, err
	}

	changes := []Change{
		{"write", in.configPath.String(), "set serve topology: " + opts.Mode.String() + " (other settings preserved)"},
	}
	changes = append(changes, in.topology.planPrepare(files)...)

	needed, err := in.topology.units(in.execStart)
	if err != nil {
		return nil, err
	}
	notes, err := in.topology.unitNotes()
	if err != nil {
		return nil, err
	}
	for _, u := range allUnitNames {
		file := unitsDir.UnitFile(u)
		if _, ok := needed[u]; ok {
			changes = append(changes, Change{"write", file.String(), notes[u]})
		} else if file.Exists() {
			changes = append(changes, Change{"remove", file.String(), "stale unit from a previous mode (stopped and disabled first)"})
		}
	}

	changes = append(changes, reloadChange())
	for _, u := range in.topology.activated() {
		changes = append(changes, unitChange(opEnable, u, ""))
	}
	if opts.Start {
		for _, u := range in.topology.activated() {
			changes = append(changes, unitChange(opStart, u, ""))
		}
	}
	return changes, nil
}

// Returns the change that applies op to u, as the `systemctl --user` command
// that does the same. Install makes the call over D-Bus (ARCHITECTURE.md,
// "Service setup").
func unitChange(op unitOp, u systemd.Unit, note string) Change {
	return Change{"run", op.commandFor(u), note}
}

// Returns unitManager.Reload as the dry run prints it.
func reloadChange() Change {
	return Change{"run", opReload.command(), ""}
}

// Prints the changes that Install(opts) would make.
func PrintPlan(ctx context.Context, opts Options) error {
	changes, err := Plan(ctx, opts)
	if err != nil {
		return err
	}
	fmt.Printf("Dry run — %s would:\n", InstallCommand.hint())
	for _, c := range changes {
		fmt.Println("  " + c.String())
	}
	return nil
}
