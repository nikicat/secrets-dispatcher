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
	layout layout
	units  *fakeUnits
}

// Returns a fixture over a temporary XDG home. Its fakes report pid 4242 as
// the owner of org.freedesktop.secrets, ownerExe as that owner's executable,
// and units.states as the unit states, with active as the default.
func newStatusFixture(t *testing.T, ownerExe procutil.Path) *statusFixture {
	t.Helper()
	f := &statusFixture{
		layout: tempLayout(t),
		units:  mockUnits(t),
	}

	mockSession(t, 4242)
	mockExePath(t, ownerExe)
	return f
}

func (f *statusFixture) writeUnits(t *testing.T, units ...systemd.Unit) {
	t.Helper()
	for _, u := range units {
		require.NoError(t, f.layout.unitDir.UnitFile(u).Write("x"))
	}
}

// Returns the problems that Status would report, without printing.
func (f *statusFixture) problems(t *testing.T) []string {
	t.Helper()
	return f.layout.readStatus(t.Context()).installation().problems()
}

func TestStatusHealthyTakeover(t *testing.T) {
	f := newStatusFixture(t, "/usr/bin/secrets-dispatcher")
	f.writeUnits(t, allUnitNames...)
	f.layout.files.place(t)

	assert.NoError(t, Status(t.Context()))
}

func TestStatusWarnsOnRegrab(t *testing.T) {
	f := newStatusFixture(t, "/usr/bin/gnome-keyring-daemon")
	f.writeUnits(t, allUnitNames...)
	f.layout.files.place(t)

	assert.Equal(t, []string{"installed for takeover but NOT in front — the owner is gnome-keyring (pid 4242) (re-grab? try `systemctl --user restart secrets-dispatcher.service`)"}, f.problems(t))
}

func TestStatusWarnsOnMissingMask(t *testing.T) {
	f := newStatusFixture(t, "/usr/bin/secrets-dispatcher")
	f.writeUnits(t, allUnitNames...)
	applySteps(t, []step{f.layout.files.dropIn.writeStep("x")})
	applySteps(t, f.layout.files.shadow.writeSteps())

	assert.Equal(t, []string{"D-Bus activation of org.freedesktop.secrets is not masked — the stock provider can be activated back in front"}, f.problems(t))
}

func TestStatusWarnsOnInconsistentDemotion(t *testing.T) {
	f := newStatusFixture(t, "/usr/bin/secrets-dispatcher")
	f.writeUnits(t, allUnitNames...)
	applySteps(t, f.layout.files.mask.writeSteps())
	applySteps(t, []step{f.layout.files.dropIn.writeStep("x")}) // drop-in without the autostart shadow

	assert.Equal(t, []string{"gnome-keyring demotion is inconsistent (drop-in: true, autostart shadow: false) — reinstall or uninstall to converge"}, f.problems(t))
}

func TestStatusWarnsOnDeadBackend(t *testing.T) {
	f := newStatusFixture(t, "/usr/bin/secrets-dispatcher")
	f.writeUnits(t, allUnitNames...)
	f.layout.files.place(t)
	f.units.states[backendServiceUnit] = "failed"

	assert.Equal(t, []string{"secrets-dispatcher-backend.service is failed"}, f.problems(t))
}

func TestStatusNamesEachInactiveTakeoverUnit(t *testing.T) {
	f := newStatusFixture(t, "/usr/bin/secrets-dispatcher")
	f.writeUnits(t, allUnitNames...)
	f.layout.files.place(t)
	f.units.states[proxyServiceUnit] = "inactive"
	f.units.states[busSocketUnit] = "failed"

	var err error
	out := captureStdout(t, func() { err = Status(t.Context()) })

	assert.EqualError(t, err, "2 problem(s) found")
	assert.Contains(t, out, "⚠ secrets-dispatcher-bus.socket is failed\n⚠ secrets-dispatcher.service is inactive\n")
}

func TestStatusNotInstalledClean(t *testing.T) {
	f := newStatusFixture(t, "/usr/bin/gnome-keyring-daemon")

	assert.Empty(t, f.problems(t))
}

func TestStatusWarnsOnLeftoverMask(t *testing.T) {
	f := newStatusFixture(t, "/usr/bin/gnome-keyring-daemon")
	applySteps(t, f.layout.files.mask.writeSteps()) // takeover residue without any units

	assert.Equal(t, []string{"leftover D-Bus activation mask but no units installed — run `secrets-dispatcher service uninstall` to restore"}, f.problems(t))
}

func TestStatusRemoteModeOwnerIsProvider(t *testing.T) {
	// Remote topology: the stock provider owning the session name is healthy.
	f := newStatusFixture(t, "/usr/bin/gnome-keyring-daemon")
	f.writeUnits(t, proxyServiceUnit)

	assert.Empty(t, f.problems(t))
}

func TestStatusRemoteModeWarnsOnUnowned(t *testing.T) {
	// Remote topology with the unit active but no owner of
	// org.freedesktop.secrets is a problem, since secret lookups hang.
	f := newStatusFixture(t, "")
	f.writeUnits(t, proxyServiceUnit)
	mockSession(t, 0)

	assert.Equal(t, []string{"org.freedesktop.secrets has no owner — no Secret Service provider is running upstream, so secret lookups will hang"}, f.problems(t))
}

func TestStatusRemoteModeNamesInactiveProxy(t *testing.T) {
	f := newStatusFixture(t, "/usr/bin/gnome-keyring-daemon")
	f.writeUnits(t, proxyServiceUnit)
	f.units.states[proxyServiceUnit] = "failed"

	assert.Equal(t, []string{"secrets-dispatcher.service is failed"}, f.problems(t))
}

func TestStatusReportsUnreachableUserManagerOnce(t *testing.T) {
	f := newStatusFixture(t, "/usr/bin/secrets-dispatcher")
	f.writeUnits(t, allUnitNames...)
	f.layout.files.place(t)
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
