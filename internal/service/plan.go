package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/systemd"
)

type installInputs struct {
	mode       Mode
	topology   topology
	runtimeDir config.RuntimeDir
	draft      config.Draft
	execStart  systemd.Command // proxy unit ExecStart
	start      bool
}

// Returns the inputs of an install with opts. Errors with config.ErrNoRuntimeDir
// when there is no runtime directory. Errors when the config does not load, and
// as resolveMode and Mode.topology do.
func resolveInstallInputs(ctx context.Context, opts Options) (installInputs, error) {
	draft, err := opts.config().Draft()
	if err != nil {
		return installInputs{}, err
	}

	runtimeDir, err := config.LookupRuntimeDir()
	if err != nil {
		return installInputs{}, err
	}

	mode, err := resolveMode(opts.Mode, draft.Serve.TopologyConfig, runtimeDir)
	if err != nil {
		return installInputs{}, err
	}
	topology, err := mode.topology(ctx, opts.Backend)
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

	return installInputs{
		mode:       mode,
		topology:   topology,
		runtimeDir: runtimeDir,
		draft:      draft,
		execStart:  systemd.NewCommand(self, config.ServeArgs(draft.File())...),
		start:      opts.Start,
	}, nil
}

// What a Change does to its target, as the dry run prints it.
type changeOp string

const (
	changeWrite   changeOp = "write"
	changeBackup  changeOp = "backup"
	changeRestore changeOp = "restore"
	changeRemove  changeOp = "remove"
	// Runs the command that the target spells, or a call that does the same.
	changeRun    changeOp = "run"
	changeSignal changeOp = "signal"
)

// One action of the service setup, as the dry run prints it (ARCHITECTURE.md,
// "Service setup").
type Change struct {
	Op     changeOp
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

// A change together with how the service setup makes it (ARCHITECTURE.md,
// "Service setup").
type step struct {
	Change
	apply func(ctx context.Context, h host) error
}

// Returns the changes that steps make, in order.
func changesOf(steps []step) []Change {
	changes := make([]Change, len(steps))
	for i, s := range steps {
		changes[i] = s.Change
	}
	return changes
}

type installPlan struct {
	steps    []step
	topology topology
	start    bool
}

// Returns the installPlan of an install with opts into the layout. Errors as
// resolveInstallInputs and layout.plan do.
func (l layout) planInstall(ctx context.Context, opts Options) (installPlan, error) {
	in, err := resolveInstallInputs(ctx, opts)
	if err != nil {
		return installPlan{}, err
	}
	return l.plan(in)
}

// Returns the installPlan of an install of the given inputs into the layout. It
// makes no change. Errors as topology.prepare and topology.units do.
func (l layout) plan(in installInputs) (installPlan, error) {
	steps := []step{in.writeTopologyStep()}

	prepare, err := in.topology.prepare(l.files)
	if err != nil {
		return installPlan{}, err
	}
	steps = append(steps, prepare...)

	needed, err := in.topology.units(in.execStart)
	if err != nil {
		return installPlan{}, err
	}
	for _, u := range allUnitNames {
		want, ok := needed[u]
		file := l.unitFile(u, want.note)
		if ok {
			steps = append(steps, file.writeStep(want.content))
		} else if file.file.Exists() {
			steps = append(steps,
				unitStep(opStop, u, "stale unit from a previous mode"),
				unitStep(opDisable, u, ""),
				file.removeStep())
		}
	}

	steps = append(steps, reloadStep())
	for _, u := range in.topology.activated() {
		steps = append(steps, unitStep(opEnable, u, ""))
	}
	if in.start {
		for _, u := range in.topology.activated() {
			steps = append(steps, unitStep(opStart, u, ""))
		}
	}
	return installPlan{steps: steps, topology: in.topology, start: in.start}, nil
}

// Returns the changes that Install(opts) makes, in order, without making any.
func Plan(ctx context.Context, opts Options) ([]Change, error) {
	l, err := locateLayout()
	if err != nil {
		return nil, err
	}
	p, err := l.planInstall(ctx, opts)
	if err != nil {
		return nil, err
	}
	return changesOf(p.steps), nil
}

// Prints the changes that Install(opts) makes, without making any.
func PrintPlan(ctx context.Context, opts Options) error {
	changes, err := Plan(ctx, opts)
	if err != nil {
		return err
	}
	printChanges(fmt.Sprintf("Dry run — %s would:", InstallCommand.hint()), changes)
	return nil
}

func printChanges(header string, changes []Change) {
	fmt.Println(header)
	for _, c := range changes {
		fmt.Println("  " + c.String())
	}
}
