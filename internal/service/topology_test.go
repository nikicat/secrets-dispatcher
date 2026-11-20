package service

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/systemd"
)

func TestModeUnmarshalText(t *testing.T) {
	var m Mode
	require.NoError(t, m.UnmarshalText([]byte("full")))
	assert.Equal(t, ModeFull, m)
	assert.EqualError(t, m.UnmarshalText([]byte("bogus")), `mode must be local, remote or full, got "bogus"`)
	require.NoError(t, m.UnmarshalText(nil))
	assert.Empty(t, m, "an empty text is unset")
}

func TestModeTopology(t *testing.T) {
	mockSession(t, 0)
	gopass := newTakeover(new(BackendGopass), Provider{})

	for _, tc := range []struct {
		mode Mode
		want topology
	}{
		{ModeRemote, remote{}},
		{ModeLocal, gopass},
		{ModeFull, full{gopass}},
	} {
		got, err := tc.mode.topology(t.Context(), nil)
		require.NoError(t, err)
		assert.Equal(t, tc.want, got, tc.mode)
	}

	_, err := Mode("bogus").topology(t.Context(), nil)
	assert.ErrorContains(t, err, `mode must be local, remote or full, got "bogus"`)
}

func TestEveryModeHasTopology(t *testing.T) {
	mockSession(t, 0)
	for _, m := range Modes.Values() {
		got, err := m.topology(t.Context(), nil)
		require.NoError(t, err, m)
		assert.NotNil(t, got, m)
	}
}

func TestTopologyServe(t *testing.T) {
	rd := config.RuntimeDir("/run/user/1000/secrets-dispatcher")
	session := config.BusConfig{Bus: config.SessionBus{}}
	sessionUp := config.UpstreamConfig{Bus: config.SessionBus{}}
	private := config.UpstreamConfig{Bus: config.Socket{Path: rd.BackendBusSocket()}}
	clients := config.BusConfig{Bus: config.Sockets{Dir: rd.ClientSockets()}}
	tk := newTakeover(nil, Provider{})

	for _, tc := range []struct {
		name           string
		topology       topology
		wantUpstream   config.UpstreamConfig
		wantDownstream []config.BusConfig
	}{
		{"remote", remote{}, sessionUp, []config.BusConfig{clients}},
		{"local", tk, private, []config.BusConfig{session}},
		{"full", full{tk}, private, []config.BusConfig{session, clients}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream, downstream := tc.topology.serve(rd)
			assert.Equal(t, tc.wantUpstream, upstream)
			assert.Equal(t, tc.wantDownstream, downstream)
		})
	}
}

func TestTopologyUnits(t *testing.T) {
	mockLookPath(t, defaultLookPath)
	tk := newTakeover(nil, Provider{})

	for _, tc := range []struct {
		name          string
		topology      topology
		wantUnits     []systemd.Unit
		wantActivated []systemd.Unit
	}{
		{"remote", remote{}, []systemd.Unit{proxyServiceUnit}, []systemd.Unit{proxyServiceUnit}},
		{"local", tk, allUnitNames, []systemd.Unit{busSocketUnit, proxyServiceUnit}},
		{"full", full{tk}, allUnitNames, []systemd.Unit{busSocketUnit, proxyServiceUnit}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			units, err := tc.topology.units(systemd.NewCommand("/usr/bin/secrets-dispatcher", "serve"))
			require.NoError(t, err)
			assert.ElementsMatch(t, tc.wantUnits, slices.Collect(maps.Keys(units)))
			assert.Equal(t, tc.wantActivated, tc.topology.activated())
		})
	}
}

func TestNewTakeoverBackend(t *testing.T) {
	gk := ownedBy("/usr/bin/gnome-keyring-daemon")
	none := Provider{}

	assert.Equal(t, BackendGopass, newTakeover(nil, none).backend)
	assert.Equal(t, BackendGnomeKeyring, newTakeover(nil, gk).backend)
	assert.Equal(t, BackendGopass, newTakeover(new(BackendGopass), gk).backend)
	opt := Backend{backendCommand("/opt/b")}
	assert.Equal(t, opt, newTakeover(&opt, none).backend)
}

func TestStatusStateInstallation(t *testing.T) {
	installed := func(units ...systemd.Unit) statusState { return statusState{installed: units} }
	none := statusState{}
	assert.Equal(t, uninstalled{none}, none.installation())
	remoteState := installed(proxyServiceUnit)
	assert.Equal(t, remoteInstall{unitsInstalled{remoteState}}, remoteState.installation())
	all := installed(allUnitNames...)
	assert.Equal(t, takeoverInstall{unitsInstalled{all}}, all.installation())
	socket := installed(busSocketUnit)
	assert.Equal(t, takeoverInstall{unitsInstalled{socket}}, socket.installation())
}

func TestUninstalledProblems(t *testing.T) {
	files := tempTakeoverFiles(t)
	assert.Empty(t, uninstalled{}.problems())
	assert.Equal(t, []string{
		"leftover D-Bus activation mask but no units installed — run `secrets-dispatcher service uninstall` to restore",
		"leftover gnome-keyring autostart shadow but no units installed — run `secrets-dispatcher service uninstall` to restore",
	}, uninstalled{statusState{mask: placedFile{files.mask, true}, shadow: placedFile{files.shadow, true}}}.problems())
}

func TestRemoteProblems(t *testing.T) {
	files := tempTakeoverFiles(t)
	healthy := statusState{
		provider:  ownedBy("/usr/bin/gnome-keyring-daemon"),
		installed: []systemd.Unit{proxyServiceUnit},
		states:    reportedStates{proxyServiceUnit: "active"},
	}
	assert.Empty(t, remoteInstall{unitsInstalled{healthy}}.problems())

	assert.Equal(t, []string{
		"remote topology installed but local takeover state present (mask/demotion) — run `secrets-dispatcher service uninstall`, then reinstall",
		string(proxyServiceUnit) + " is missing while other units are installed — reinstall or uninstall",
		"org.freedesktop.secrets has no owner — no Secret Service provider is running upstream, so secret lookups will hang",
	}, remoteInstall{unitsInstalled{statusState{
		provider:  Provider{},
		mask:      placedFile{files.mask, true},
		installed: []systemd.Unit{backendServiceUnit},
		states:    reportedStates{backendServiceUnit: "active"},
	}}}.problems())

	assert.Equal(t, []string{
		string(proxyServiceUnit) + " is failed",
		"org.freedesktop.secrets has no owner — no Secret Service provider is running upstream, so secret lookups will hang",
	}, remoteInstall{unitsInstalled{statusState{
		provider:  Provider{},
		installed: []systemd.Unit{proxyServiceUnit},
		states:    reportedStates{proxyServiceUnit: "failed"},
	}}}.problems())
}

func TestTakeoverProblems(t *testing.T) {
	files := tempTakeoverFiles(t)
	takeoverUnits := []systemd.Unit{busSocketUnit, backendServiceUnit, proxyServiceUnit}
	healthy := statusState{
		provider:  ownedBy("/usr/bin/secrets-dispatcher"),
		mask:      placedFile{files.mask, true},
		installed: takeoverUnits,
		states:    reportedStates{busSocketUnit: "active", backendServiceUnit: "active", proxyServiceUnit: "active"},
	}
	assert.Empty(t, takeoverInstall{unitsInstalled{healthy}}.problems())

	owner := ownedBy("/usr/bin/gnome-keyring-daemon")
	assert.Equal(t, []string{
		"installed for takeover but NOT in front — the owner is " + owner.String() + " (re-grab? try `systemctl --user restart " + string(proxyServiceUnit) + "`)",
		"D-Bus activation of org.freedesktop.secrets is not masked — the stock provider can be activated back in front",
		"gnome-keyring demotion is inconsistent (drop-in: true, autostart shadow: false) — reinstall or uninstall to converge",
		string(busSocketUnit) + " is failed",
		string(proxyServiceUnit) + " is inactive",
	}, takeoverInstall{unitsInstalled{statusState{
		provider:  owner,
		dropIn:    placedFile{files.dropIn, true},
		installed: takeoverUnits,
		states:    reportedStates{busSocketUnit: "failed", backendServiceUnit: "active", proxyServiceUnit: "inactive"},
	}}}.problems())

	unreachable := healthy
	unreachable.states = unreachableManager{errors.New("no bus")}
	assert.Equal(t, []string{"no bus"}, takeoverInstall{unitsInstalled{unreachable}}.problems(),
		"an unreachable manager is one problem, not one per unit")
}

// Returns the ExecStart= lines of each unit file, in file order.
func execStarts(units unitFiles) map[systemd.Unit][]string {
	lines := map[systemd.Unit][]string{}
	for u, file := range units {
		for line := range strings.Lines(file.content) {
			if strings.HasPrefix(line, "ExecStart=") {
				lines[u] = append(lines[u], strings.TrimSuffix(line, "\n"))
			}
		}
	}
	return lines
}

func TestTopologyUnitsExecStart(t *testing.T) {
	mockLookPath(t, defaultLookPath)
	execStart := systemd.NewCommand("/usr/bin/secrets-dispatcher", "serve", "--config", "/home/u/.config/secrets-dispatcher/config.yaml")
	want := "ExecStart=/usr/bin/secrets-dispatcher serve --config /home/u/.config/secrets-dispatcher/config.yaml"

	units, err := remote{}.units(execStart)
	require.NoError(t, err)
	assert.Equal(t, map[systemd.Unit][]string{proxyServiceUnit: {want}}, execStarts(units))

	units, err = newTakeover(new(BackendGnomeKeyring), Provider{}).units(execStart)
	require.NoError(t, err)
	assert.Equal(t, map[systemd.Unit][]string{
		busServiceUnit:     {"ExecStart=/usr/bin/dbus-daemon --session --nofork --nopidfile --address=systemd:"},
		backendServiceUnit: {"ExecStart=/usr/bin/gnome-keyring-daemon --foreground --components=secrets --control-directory=%t/secrets-dispatcher/keyring"},
		proxyServiceUnit:   {want},
	}, execStarts(units))
}

func TestTopologyUnitsQuoteSpacedPaths(t *testing.T) {
	mockLookPath(t, func(name string) (string, error) { return "/opt/my tools/" + name, nil })
	execStart := systemd.NewCommand("/opt/my tools/secrets-dispatcher", "serve", "--config", "/home/u/my config.yaml")

	units, err := newTakeover(new(BackendGopass), Provider{}).units(execStart)
	require.NoError(t, err)
	assert.Equal(t, map[systemd.Unit][]string{
		busServiceUnit:     {`ExecStart="/opt/my tools/dbus-daemon" --session --nofork --nopidfile --address=systemd:`},
		backendServiceUnit: {`ExecStart="/opt/my tools/gopass-secret" service --bus-address unix:path=%t/secrets-dispatcher/backend-bus.sock`},
		proxyServiceUnit:   {`ExecStart="/opt/my tools/secrets-dispatcher" serve --config "/home/u/my config.yaml"`},
	}, execStarts(units))
}
