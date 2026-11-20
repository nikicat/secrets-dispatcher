package service

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/procutil"
)

// Returns the position of the first change with op whose target contains
// targetSubstr, or -1 when there is none.
func changeIndex(changes []Change, op, targetSubstr string) int {
	for i, c := range changes {
		if c.Op == op && strings.Contains(c.Target, targetSubstr) {
			return i
		}
	}
	return -1
}

func TestPlanLocalGnomeKeyringMirrorsInstallOrder(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockLookPath(t, defaultLookPath)
	mockSession(t, 4242)
	mockExe(t, func(procutil.PID) (procutil.Path, bool) {
		return "/usr/bin/gnome-keyring-daemon", true
	})

	changes, err := Plan(t.Context(), Options{Mode: ModeLocal, Start: true})
	require.NoError(t, err)

	config := changeIndex(changes, "write", "config.yaml")
	mask := changeIndex(changes, "write", dbusServiceFile)
	dropIn := changeIndex(changes, "write", gkDropInName)
	shadow := changeIndex(changes, "write", gkAutostartName)
	sigterm := changeIndex(changes, "signal", "pid 4242")
	proxyUnit := changeIndex(changes, "write", string(proxyServiceUnit))
	start := changeIndex(changes, "run", opStart.commandFor(proxyServiceUnit))

	for name, idx := range map[string]int{
		"config": config, "mask": mask, "drop-in": dropIn, "shadow": shadow,
		"sigterm": sigterm, "proxy unit": proxyUnit, "start": start,
	} {
		require.GreaterOrEqual(t, idx, 0, "missing %s change in plan: %v", name, changes)
	}

	// Execution order must mirror Install: config, then mask, then demotion,
	// then the SIGTERM to the displaced owner, then units, then start.
	assert.Less(t, config, mask)
	assert.Less(t, mask, dropIn)
	assert.Less(t, dropIn, shadow)
	assert.Less(t, shadow, sigterm)
	assert.Less(t, sigterm, proxyUnit)
	assert.Less(t, proxyUnit, start)

	// A plan must not touch the filesystem.
	entries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "Plan created files")
}

func TestPlanSurfacesResolvedBackend(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockLookPath(t, defaultLookPath)
	mockSession(t, 4242)
	mockExe(t, func(procutil.PID) (procutil.Path, bool) {
		return "/usr/bin/gnome-keyring-daemon", true
	})

	changes, err := Plan(t.Context(), Options{Mode: ModeLocal})
	require.NoError(t, err)

	idx := changeIndex(changes, "write", string(backendServiceUnit))
	require.GreaterOrEqual(t, idx, 0)
	assert.Equal(t, "private backend: /usr/bin/gnome-keyring-daemon --foreground --components=secrets --control-directory=%t/secrets-dispatcher/keyring", changes[idx].Note)
}

func TestPlanRemoteMinimal(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockSession(t, 0)

	changes, err := Plan(t.Context(), Options{Mode: ModeRemote})
	require.NoError(t, err)

	assert.GreaterOrEqual(t, changeIndex(changes, "write", string(proxyServiceUnit)), 0)
	assert.Equal(t, -1, changeIndex(changes, "write", dbusServiceFile), "remote mode must not mask")
	assert.Equal(t, -1, changeIndex(changes, "signal", ""), "remote mode must not signal anyone")
	for _, c := range changes {
		assert.NotContains(t, c.Target, "bus.socket", "remote mode must not install the private bus")
	}
}

func TestPlanRemoteReversesTakeoverState(t *testing.T) {
	files := tempTakeoverFiles(t)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockSession(t, 0)

	// Simulate a previous takeover: our mask, drop-in, and autostart shadow.
	files.place(t)

	changes, err := Plan(t.Context(), Options{Mode: ModeRemote})
	require.NoError(t, err)

	assert.GreaterOrEqual(t, changeIndex(changes, "remove", dbusServiceFile), 0)
	assert.GreaterOrEqual(t, changeIndex(changes, "remove", gkDropInName), 0)
	assert.GreaterOrEqual(t, changeIndex(changes, "remove", gkAutostartName), 0)
	assert.GreaterOrEqual(t, changeIndex(changes, "run", opTryRestart.commandFor(gkServiceUnit)), 0)

	// Reading state must not modify it.
	assert.True(t, files.mask.file.Exists())
}

func TestPlanMirrorsInstall(t *testing.T) {
	// The contract Plan promises: for the same options and starting state,
	// every file Plan says it writes/removes is exactly what Install then
	// writes/removes.
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockLookPath(t, defaultLookPath)
	mockSession(t, 4242)
	mockExe(t, func(procutil.PID) (procutil.Path, bool) {
		return "/usr/bin/gnome-keyring-daemon", true
	})
	mockUnits(t)
	shortOwnerWait(t)

	opts := Options{Mode: ModeLocal, Start: true}
	changes, err := Plan(t.Context(), opts)
	require.NoError(t, err)

	require.NoError(t, Install(t.Context(), opts))

	for _, c := range changes {
		if c.Op == "write" {
			assert.FileExists(t, c.Target, "planned write did not happen: %s", c)
		}
	}
}

// The dry run lists the takeover files' changes, in Install's order.
func TestPlanTakeoverFileChanges(t *testing.T) {
	files := tempTakeoverFiles(t)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	mockLookPath(t, defaultLookPath)
	mockSession(t, 4242)
	mockExe(t, func(procutil.PID) (procutil.Path, bool) { return "/usr/bin/gnome-keyring-daemon", true })

	mask, dropIn, shadow := files.mask.file.String(), files.dropIn.file.String(), files.shadow.file.String()
	maskBackup, shadowBackup := files.mask.backup().String(), files.shadow.backup().String()
	require.NoError(t, files.mask.file.Write("foreign"))
	require.NoError(t, files.shadow.file.Write("foreign"))
	changes, err := Plan(t.Context(), Options{Mode: ModeLocal})
	require.NoError(t, err)
	var got []string
	for _, c := range changes[1:9] {
		got = append(got, c.Op+"|"+c.Target+"|"+c.Note)
	}
	assert.Equal(t, []string{
		"backup|" + mask + "|-> " + maskBackup,
		"write|" + mask + "|mask D-Bus activation of org.freedesktop.secrets",
		"write|" + dropIn + "|demote gnome-keyring to pkcs11-only (secrets move to the private backend)",
		"backup|" + shadow + "|-> " + shadowBackup,
		"write|" + shadow + "|hide the gnome-keyring secrets autostart kicker (Hidden=true)",
		"run|systemctl --user daemon-reload|",
		"run|systemctl --user try-restart gnome-keyring-daemon.service|restart without the secrets component",
		"signal|SIGTERM -> pid 4242 (gnome-keyring)|current owner of org.freedesktop.secrets",
	}, got)

	// Plan remote after a takeover. The foreign mask from above is backed up
	// when place writes ours. The shadow is removed first, so it has no backup.
	require.NoError(t, files.shadow.file.Remove())
	files.place(t)
	changes, err = Plan(t.Context(), Options{Mode: ModeRemote})
	require.NoError(t, err)
	got = nil
	for _, c := range changes[1:6] {
		got = append(got, c.Op+"|"+c.Target+"|"+c.Note)
	}
	assert.Equal(t, []string{
		"restore|" + maskBackup + "|-> " + mask,
		"remove|" + dropIn + "|gnome-keyring demotion drop-in",
		"remove|" + shadow + "|gnome-keyring autostart shadow",
		"run|systemctl --user daemon-reload|",
		"run|systemctl --user try-restart gnome-keyring-daemon.service|restart with all components",
	}, got)
}
