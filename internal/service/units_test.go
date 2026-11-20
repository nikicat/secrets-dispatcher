package service

import (
	"cmp"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/systemd"
	"github.com/nikicat/secrets-dispatcher/internal/testutil"
)

// One call on the unit manager; unit is empty for opReload.
type unitCall struct {
	op   unitOp
	unit systemd.Unit
}

// Records every call and succeeds while ctx lives, reporting states (default
// active).
type fakeUnits struct {
	calls  []unitCall
	states map[systemd.Unit]unitState
	closed bool
}

func (f *fakeUnits) record(ctx context.Context, op unitOp, u systemd.Unit) error {
	f.calls = append(f.calls, unitCall{op, u})
	return ctx.Err()
}

func (f *fakeUnits) Unit(u systemd.Unit) unit         { return fakeUnit{f, u} }
func (f *fakeUnits) Reload(ctx context.Context) error { return f.record(ctx, opReload, "") }
func (f *fakeUnits) Close()                           { f.closed = true }

// A unit whose calls its fakeUnits records.
type fakeUnit struct {
	f    *fakeUnits
	name systemd.Unit
}

func (u fakeUnit) Enable(ctx context.Context) ([]unitFileChange, error) {
	if err := u.f.record(ctx, opEnable, u.name); err != nil {
		return nil, err
	}
	return []unitFileChange{{changeSymlink, "/wants/" + string(u.name), "/units/" + string(u.name)}}, nil
}

func (u fakeUnit) Disable(ctx context.Context) ([]unitFileChange, error) {
	return nil, u.f.record(ctx, opDisable, u.name)
}

func (u fakeUnit) Start(ctx context.Context) error      { return u.f.record(ctx, opStart, u.name) }
func (u fakeUnit) Stop(ctx context.Context) error       { return u.f.record(ctx, opStop, u.name) }
func (u fakeUnit) TryRestart(ctx context.Context) error { return u.f.record(ctx, opTryRestart, u.name) }
func (u fakeUnit) Restart(ctx context.Context) error    { return u.f.record(ctx, opRestart, u.name) }

func (f *fakeUnits) ActiveStates(ctx context.Context, units []systemd.Unit) (map[systemd.Unit]unitState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	states := make(map[systemd.Unit]unitState, len(units))
	for _, u := range units {
		states[u] = cmp.Or(f.states[u], unitActive)
	}
	return states, nil
}

// Makes openUnitManager return a fresh fakeUnits for the rest of the test.
func mockUnits(t *testing.T) *fakeUnits {
	t.Helper()
	f := &fakeUnits{states: map[systemd.Unit]unitState{}}
	testutil.Swap(t, &openUnitManager, func(context.Context) (unitManager, error) { return f, nil })
	return f
}

// Makes openUnitManager fail for the rest of the test, as when no user
// manager runs.
func mockUnitsUnreachable(t *testing.T) {
	t.Helper()
	testutil.Swap(t, &openUnitManager, func(context.Context) (unitManager, error) {
		return nil, errors.New("connect to the user systemd manager: no bus")
	})
}

// Returns a jobCall that reports result for the job it queues.
func jobResult(result string) jobCall {
	return func(_ context.Context, _, mode string, ch chan<- string) (int, error) {
		if mode != jobModeReplace {
			return 0, errors.New("unexpected mode " + mode)
		}
		ch <- result
		return 1, nil
	}
}

func TestJobDoneSucceeds(t *testing.T) {
	assert.NoError(t, userUnit{name: proxyServiceUnit}.job(t.Context(), opStart, jobResult("done")))
}

func TestJobFailureNamesUnitAndResult(t *testing.T) {
	assert.EqualError(t, userUnit{name: proxyServiceUnit}.job(t.Context(), opStart, jobResult("failed")),
		string(opStart)+" "+string(proxyServiceUnit)+": job failed")
}

func TestJobQueueErrorNamesUnit(t *testing.T) {
	refuse := func(context.Context, string, string, chan<- string) (int, error) {
		return 0, errors.New("Unit not found")
	}
	assert.EqualError(t, userUnit{name: busSocketUnit}.job(t.Context(), opStop, refuse),
		string(opStop)+" "+string(busSocketUnit)+": Unit not found")
}

func TestJobStopsWaitingWhenContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	// Queues the job and never reports its result; Ctrl-C comes while job waits.
	queued := func(context.Context, string, string, chan<- string) (int, error) {
		time.AfterFunc(10*time.Millisecond, cancel)
		return 1, nil
	}
	assert.ErrorIs(t, userUnit{name: proxyServiceUnit}.job(ctx, opStart, queued), context.Canceled)
}

func TestUnitFileChangeReadsAsSystemctl(t *testing.T) {
	assert.Equal(t, "Created symlink /a → /b.", unitFileChange{changeSymlink, "/a", "/b"}.String())
	assert.Equal(t, `Removed "/a".`, unitFileChange{changeUnlink, "/a", ""}.String())
}

func TestInstallFailsWithoutUserManager(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	mockUnitsUnreachable(t)

	require.ErrorContains(t, Install(t.Context(), Options{Mode: ModeRemote}), "user systemd manager")
	assert.NoDirExists(t, filepath.Join(tmpDir, "systemd"))
}
