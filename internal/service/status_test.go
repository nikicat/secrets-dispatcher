package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// statusFixture builds the file state of a topology and wires the exec fakes:
// busctl reports pid 4242 owning the name, systemctl is-active reports
// unitStates (default "active"), readlink classifies the owner as ownerExe.
type statusFixture struct {
	tmpDir     string
	unitsDir   string
	ownerExe   string
	unitStates map[string]string
}

func newStatusFixture(t *testing.T, ownerExe string) *statusFixture {
	t.Helper()
	f := &statusFixture{
		tmpDir:     t.TempDir(),
		ownerExe:   ownerExe,
		unitStates: map[string]string{},
	}
	f.unitsDir = filepath.Join(f.tmpDir, "systemd", "user")
	t.Setenv("XDG_CONFIG_HOME", f.tmpDir)
	t.Setenv("XDG_DATA_HOME", f.tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockExecOutput(t, func(name string, args ...string) ([]byte, error) {
		if name == "systemctl" {
			unit := args[len(args)-1]
			if state, ok := f.unitStates[unit]; ok {
				return []byte(state + "\n"), nil
			}
			return []byte("active\n"), nil
		}
		return []byte("u 4242\n"), nil
	})
	mockReadlink(t, func(string) (string, error) {
		return f.ownerExe, nil
	})
	return f
}

func (f *statusFixture) writeUnits(t *testing.T, names ...string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(f.unitsDir, 0755))
	for _, name := range names {
		require.NoError(t, os.WriteFile(filepath.Join(f.unitsDir, name), []byte("x"), 0644))
	}
}

func (f *statusFixture) writeMask(t *testing.T) {
	t.Helper()
	dir := filepath.Join(f.tmpDir, "dbus-1", "services")
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, dbusServiceFile), []byte(dbusActivationMask), 0644))
}

func (f *statusFixture) writeDropIn(t *testing.T) {
	t.Helper()
	dir := filepath.Join(f.unitsDir, gkDropInDirName)
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, gkDropInName), []byte("x"), 0644))
}

func (f *statusFixture) writeShadow(t *testing.T) {
	t.Helper()
	dir := filepath.Join(f.tmpDir, "autostart")
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, gkAutostartName), []byte(gkAutostartShadow), 0644))
}

func TestStatusHealthyTakeover(t *testing.T) {
	f := newStatusFixture(t, "/usr/bin/secrets-dispatcher")
	f.writeUnits(t, allUnitNames...)
	f.writeMask(t)
	f.writeDropIn(t)
	f.writeShadow(t)

	assert.NoError(t, Status())
}

func TestStatusWarnsOnRegrab(t *testing.T) {
	f := newStatusFixture(t, "/usr/bin/gnome-keyring-daemon")
	f.writeUnits(t, allUnitNames...)
	f.writeMask(t)
	f.writeDropIn(t)
	f.writeShadow(t)

	assert.ErrorContains(t, Status(), "problem")
}

func TestStatusWarnsOnMissingMask(t *testing.T) {
	f := newStatusFixture(t, "/usr/bin/secrets-dispatcher")
	f.writeUnits(t, allUnitNames...)
	f.writeDropIn(t)
	f.writeShadow(t)

	assert.ErrorContains(t, Status(), "problem")
}

func TestStatusWarnsOnInconsistentDemotion(t *testing.T) {
	f := newStatusFixture(t, "/usr/bin/secrets-dispatcher")
	f.writeUnits(t, allUnitNames...)
	f.writeMask(t)
	f.writeDropIn(t) // drop-in without the autostart shadow

	assert.ErrorContains(t, Status(), "problem")
}

func TestStatusWarnsOnDeadBackend(t *testing.T) {
	f := newStatusFixture(t, "/usr/bin/secrets-dispatcher")
	f.writeUnits(t, allUnitNames...)
	f.writeMask(t)
	f.writeDropIn(t)
	f.writeShadow(t)
	f.unitStates["secrets-dispatcher-backend.service"] = "failed"

	assert.ErrorContains(t, Status(), "problem")
}

func TestStatusNotInstalledClean(t *testing.T) {
	newStatusFixture(t, "/usr/bin/gnome-keyring-daemon")

	assert.NoError(t, Status())
}

func TestStatusWarnsOnLeftoverMask(t *testing.T) {
	f := newStatusFixture(t, "/usr/bin/gnome-keyring-daemon")
	f.writeMask(t) // takeover residue without any units

	assert.ErrorContains(t, Status(), "problem")
}

func TestStatusRemoteModeOwnerIsProvider(t *testing.T) {
	// Remote topology: the stock provider owning the session name is healthy.
	f := newStatusFixture(t, "/usr/bin/gnome-keyring-daemon")
	f.writeUnits(t, unitFileName)

	assert.NoError(t, Status())
}

func TestStatusRemoteModeWarnsOnUnowned(t *testing.T) {
	// Remote topology, unit active — but org.freedesktop.secrets has no owner,
	// so secret lookups hang while the unit still looks healthy. Regression for
	// the doctor reporting "✓ no problems found" in exactly this state (the
	// real incident that broke secret access after a stray remote-mode install).
	f := newStatusFixture(t, "")
	f.writeUnits(t, unitFileName)
	// Override the exec fakes so busctl reports the name as unowned (the
	// GetConnectionUnixProcessID call fails), while systemctl still says active.
	mockExecOutput(t, func(name string, _ ...string) ([]byte, error) {
		if name == "systemctl" {
			return []byte("active\n"), nil
		}
		return nil, errors.New("The connection does not exist")
	})

	assert.ErrorContains(t, Status(), "problem")
}
