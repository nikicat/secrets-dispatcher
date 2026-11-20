package service

import (
	"context"
	"fmt"

	sddbus "github.com/coreos/go-systemd/v22/dbus"

	"github.com/nikicat/secrets-dispatcher/internal/strs"
	"github.com/nikicat/secrets-dispatcher/internal/systemd"
)

// The user's systemd manager, which the service setup drives over D-Bus.
type unitManager interface {
	// Returns a handle on u, without a call to systemd.
	Unit(u systemd.Unit) unit
	// Rereads every unit file, as `systemctl --user daemon-reload` does.
	Reload(ctx context.Context) error
	// Returns the ActiveState of each given unit, as systemd reports it.
	ActiveStates(ctx context.Context, units []systemd.Unit) (map[systemd.Unit]unitState, error)
	Close()
}

// One unit of the user's systemd manager. Start, Stop, TryRestart and Restart
// wait for systemd to finish the job, and error when its result is not done.
type unit interface {
	// Enables the unit file and returns the symlinks systemd changed.
	Enable(ctx context.Context) ([]unitFileChange, error)
	// Disables the unit file and returns the symlinks systemd changed.
	Disable(ctx context.Context) ([]unitFileChange, error)
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	// Restarts the unit only when it is running.
	TryRestart(ctx context.Context) error
	Restart(ctx context.Context) error
}

// A unit's ActiveState as systemd reports it, such as active or failed, or
// unitUnknown.
type unitState string

const (
	unitActive unitState = "active"
	// The user's systemd manager reported no state for the unit.
	unitUnknown unitState = "unknown"
)

// An operation of the user's systemd manager, spelled as the systemctl verb
// that does the same. A dry run prints it as that command (ARCHITECTURE.md,
// "Service setup").
type unitOp string

const (
	opEnable     unitOp = "enable"
	opDisable    unitOp = "disable"
	opStart      unitOp = "start"
	opStop       unitOp = "stop"
	opTryRestart unitOp = "try-restart"
	opRestart    unitOp = "restart"
	opReload     unitOp = "daemon-reload"
)

func (op unitOp) command() string { return "systemctl --user " + string(op) }

// Returns the systemctl command line that applies op to u.
func (op unitOp) commandFor(u systemd.Unit) string { return op.command() + " " + string(u) }

// A symlink that enabling or disabling a unit file created or removed.
//
// smell: destination only means something when kind is symlink — kept, mirrors
// go-systemd's EnableUnitFileChange 1:1. Split into created and removed when a
// second consumer reads the changes.
type unitFileChange struct {
	kind unitFileChangeKind
	// The symlink.
	filename string
	// Where the symlink points; empty for a removed one.
	destination string
}

// What systemd did to a symlink, such as "symlink" or "unlink".
type unitFileChangeKind string

const (
	changeSymlink unitFileChangeKind = "symlink"
	changeUnlink  unitFileChangeKind = "unlink"
)

// Reads as systemctl reports the same change.
func (c unitFileChange) String() string {
	switch c.kind {
	case changeSymlink:
		return fmt.Sprintf("Created symlink %s → %s.", c.filename, c.destination)
	case changeUnlink:
		return fmt.Sprintf("Removed %q.", c.filename)
	}
	return fmt.Sprintf("%s %s %s", c.kind, c.filename, c.destination)
}

// Connects to the user's systemd manager; the caller closes the result.
// Replaced in tests.
var openUnitManager = func(ctx context.Context) (unitManager, error) {
	conn, err := sddbus.NewUserConnectionContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect to the user systemd manager: %w", err)
	}
	return userManager{conn: conn}, nil
}

// The user's systemd manager over its D-Bus API.
type userManager struct {
	conn *sddbus.Conn
}

func (m userManager) Unit(u systemd.Unit) unit { return userUnit{m.conn, u} }

// A unit of the user's systemd manager over its D-Bus API.
type userUnit struct {
	conn *sddbus.Conn
	name systemd.Unit
}

// The job mode that systemctl uses by default. A new job replaces a
// conflicting job that is still queued.
const jobModeReplace = "replace"

// A go-systemd method that queues a job for a unit, such as
// Conn.StartUnitContext.
type jobCall func(ctx context.Context, name, mode string, ch chan<- string) (int, error)

// Runs the job that call queues for the unit and waits for its result. Errors
// when the result is not done, or when ctx ends first.
func (u userUnit) job(ctx context.Context, op unitOp, call jobCall) error {
	done := make(chan string, 1)
	if _, err := call(ctx, string(u.name), jobModeReplace, done); err != nil {
		return u.fail(op, err)
	}
	select {
	case result := <-done:
		if result != "done" {
			return u.fail(op, fmt.Errorf("job %s", result))
		}
		return nil
	case <-ctx.Done():
		return u.fail(op, ctx.Err())
	}
}

func (u userUnit) fail(op unitOp, err error) error {
	return fmt.Errorf("%s %s: %w", op, u.name, err)
}

func (u userUnit) Start(ctx context.Context) error {
	return u.job(ctx, opStart, u.conn.StartUnitContext)
}

func (u userUnit) Stop(ctx context.Context) error {
	return u.job(ctx, opStop, u.conn.StopUnitContext)
}

func (u userUnit) TryRestart(ctx context.Context) error {
	return u.job(ctx, opTryRestart, u.conn.TryRestartUnitContext)
}

func (u userUnit) Restart(ctx context.Context) error {
	return u.job(ctx, opRestart, u.conn.RestartUnitContext)
}

func (u userUnit) Enable(ctx context.Context) ([]unitFileChange, error) {
	_, changes, err := u.conn.EnableUnitFilesContext(ctx, []string{string(u.name)}, false, false)
	if err != nil {
		return nil, u.fail(opEnable, err)
	}
	return fileChanges(changes), nil
}

func (u userUnit) Disable(ctx context.Context) ([]unitFileChange, error) {
	changes, err := u.conn.DisableUnitFilesContext(ctx, []string{string(u.name)}, false)
	if err != nil {
		return nil, u.fail(opDisable, err)
	}
	return fileChanges(changes), nil
}

// Enabling and disabling report their changes in the same shape.
func fileChanges[C sddbus.EnableUnitFileChange | sddbus.DisableUnitFileChange](changes []C) []unitFileChange {
	out := make([]unitFileChange, len(changes))
	for i, c := range changes {
		e := sddbus.EnableUnitFileChange(c)
		out[i] = unitFileChange{unitFileChangeKind(e.Type), e.Filename, e.Destination}
	}
	return out
}

func (m userManager) Reload(ctx context.Context) error {
	if err := m.conn.ReloadContext(ctx); err != nil {
		return fmt.Errorf("reload the user systemd manager: %w", err)
	}
	return nil
}

func (m userManager) ActiveStates(ctx context.Context, units []systemd.Unit) (map[systemd.Unit]unitState, error) {
	statuses, err := m.conn.ListUnitsByNamesContext(ctx, strs.Of(units))
	if err != nil {
		return nil, fmt.Errorf("list the units of the user systemd manager: %w", err)
	}
	states := make(map[systemd.Unit]unitState, len(statuses))
	for _, s := range statuses {
		states[systemd.Unit(s.Name)] = unitState(s.ActiveState)
	}
	return states, nil
}

func (m userManager) Close() { m.conn.Close() }
