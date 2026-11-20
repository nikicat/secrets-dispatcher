package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/systemd"
	"github.com/nikicat/secrets-dispatcher/internal/testutil"
)

type statusFixture struct {
	unitsDir systemd.UnitDir
	files    takeoverFiles
	ownerExe procutil.Path
	units    *fakeUnits
}

// Returns a fixture over a temporary XDG home. Its fakes report pid 4242 as
// the owner of org.freedesktop.secrets, ownerExe as that owner's executable,
// and units.states as the unit states, with active as the default.
func newStatusFixture(t *testing.T, ownerExe procutil.Path) *statusFixture {
	t.Helper()
	f := &statusFixture{
		files:    tempTakeoverFiles(t),
		ownerExe: ownerExe,
		units:    mockUnits(t),
	}
	var err error
	f.unitsDir, err = unitDir()
	require.NoError(t, err)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockSession(t, 4242)
	mockExe(t, func(procutil.PID) (procutil.Path, bool) {
		return f.ownerExe, true
	})
	return f
}

func (f *statusFixture) writeUnits(t *testing.T, units ...systemd.Unit) {
	t.Helper()
	for _, u := range units {
		require.NoError(t, f.unitsDir.UnitFile(u).Write("x"))
	}
}

func TestStatusHealthyTakeover(t *testing.T) {
	f := newStatusFixture(t, "/usr/bin/secrets-dispatcher")
	f.writeUnits(t, allUnitNames...)
	f.files.place(t)

	assert.NoError(t, Status(t.Context()))
}

func TestStatusWarnsOnRegrab(t *testing.T) {
	f := newStatusFixture(t, "/usr/bin/gnome-keyring-daemon")
	f.writeUnits(t, allUnitNames...)
	f.files.place(t)

	assert.ErrorContains(t, Status(t.Context()), "problem")
}

func TestStatusWarnsOnMissingMask(t *testing.T) {
	f := newStatusFixture(t, "/usr/bin/secrets-dispatcher")
	f.writeUnits(t, allUnitNames...)
	require.NoError(t, f.files.dropIn.write(NarrateMilestones, "x"))
	require.NoError(t, f.files.shadow.write(NarrateMilestones))

	assert.ErrorContains(t, Status(t.Context()), "problem")
}

func TestStatusWarnsOnInconsistentDemotion(t *testing.T) {
	f := newStatusFixture(t, "/usr/bin/secrets-dispatcher")
	f.writeUnits(t, allUnitNames...)
	require.NoError(t, f.files.mask.write(NarrateMilestones))
	require.NoError(t, f.files.dropIn.write(NarrateMilestones, "x")) // drop-in without the autostart shadow

	assert.ErrorContains(t, Status(t.Context()), "problem")
}

func TestStatusWarnsOnDeadBackend(t *testing.T) {
	f := newStatusFixture(t, "/usr/bin/secrets-dispatcher")
	f.writeUnits(t, allUnitNames...)
	f.files.place(t)
	f.units.states[backendServiceUnit] = "failed"

	assert.ErrorContains(t, Status(t.Context()), "problem")
}

func TestStatusNamesEachInactiveTakeoverUnit(t *testing.T) {
	f := newStatusFixture(t, "/usr/bin/secrets-dispatcher")
	f.writeUnits(t, allUnitNames...)
	f.files.place(t)
	f.units.states[proxyServiceUnit] = "inactive"
	f.units.states[busSocketUnit] = "failed"

	var err error
	out := captureStdout(t, func() { err = Status(t.Context()) })

	assert.EqualError(t, err, "2 problem(s) found")
	assert.Contains(t, out, "⚠ secrets-dispatcher-bus.socket is failed\n⚠ secrets-dispatcher.service is inactive\n")
}

func TestStatusNotInstalledClean(t *testing.T) {
	newStatusFixture(t, "/usr/bin/gnome-keyring-daemon")

	assert.NoError(t, Status(t.Context()))
}

func TestStatusWarnsOnLeftoverMask(t *testing.T) {
	f := newStatusFixture(t, "/usr/bin/gnome-keyring-daemon")
	require.NoError(t, f.files.mask.write(NarrateMilestones)) // takeover residue without any units

	assert.ErrorContains(t, Status(t.Context()), "problem")
}

func TestStatusRemoteModeOwnerIsProvider(t *testing.T) {
	// Remote topology: the stock provider owning the session name is healthy.
	f := newStatusFixture(t, "/usr/bin/gnome-keyring-daemon")
	f.writeUnits(t, proxyServiceUnit)

	assert.NoError(t, Status(t.Context()))
}

func TestStatusRemoteModeWarnsOnUnowned(t *testing.T) {
	// Remote topology with the unit active but no owner of
	// org.freedesktop.secrets is a problem, since secret lookups hang.
	f := newStatusFixture(t, "")
	f.writeUnits(t, proxyServiceUnit)
	mockSession(t, 0)

	assert.ErrorContains(t, Status(t.Context()), "problem")
}

func TestStatusRemoteModeNamesInactiveProxy(t *testing.T) {
	f := newStatusFixture(t, "/usr/bin/gnome-keyring-daemon")
	f.writeUnits(t, proxyServiceUnit)
	f.units.states[proxyServiceUnit] = "failed"

	var err error
	out := captureStdout(t, func() { err = Status(t.Context()) })

	assert.EqualError(t, err, "1 problem(s) found")
	assert.Contains(t, out, "⚠ secrets-dispatcher.service is failed\n")
}

func TestStatusReportsUnreachableUserManagerOnce(t *testing.T) {
	f := newStatusFixture(t, "/usr/bin/secrets-dispatcher")
	f.writeUnits(t, allUnitNames...)
	f.files.place(t)
	mockUnitsUnreachable(t)

	var err error
	out := captureStdout(t, func() { err = Status(t.Context()) })

	assert.EqualError(t, err, "1 problem(s) found")
	assert.Contains(t, out, "  secrets-dispatcher.service             unknown\n")
	assert.Contains(t, out, "⚠ connect to the user systemd manager: no bus\n")
	assert.NotContains(t, out, "is unknown")
}

// Reports no state for any unit.
type silentUnits struct{ fakeUnits }

func (*silentUnits) ActiveStates(context.Context, []systemd.Unit) (map[systemd.Unit]unitState, error) {
	return map[systemd.Unit]unitState{}, nil
}

func TestActiveStatesMarksUnreportedUnitUnknown(t *testing.T) {
	testutil.Swap(t, &openUnitManager, func(context.Context) (unitManager, error) { return &silentUnits{}, nil })

	assert.Equal(t, reportedStates{proxyServiceUnit: unitUnknown}, activeStates(t.Context(), []systemd.Unit{proxyServiceUnit}))
}
