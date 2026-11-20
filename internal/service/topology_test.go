package service

import (
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/systemd"
)

func TestModeTopology(t *testing.T) {
	t.Setenv("XDG_CURRENT_DESKTOP", "")
	mockExecOutput(t, noopExecOutput)
	gopass := newTakeover(BackendGopass, Provider{Kind: ProviderNone})

	for _, tc := range []struct {
		mode Mode
		want topology
	}{
		{ModeRemote, remote{}},
		{ModeLocal, gopass},
		{ModeFull, full{gopass}},
	} {
		got, err := tc.mode.topology("")
		require.NoError(t, err)
		assert.Equal(t, tc.want, got, tc.mode)
	}

	_, err := Mode("bogus").topology("")
	assert.ErrorContains(t, err, `unknown mode "bogus"`)
}

func TestEveryModeHasTopology(t *testing.T) {
	t.Setenv("XDG_CURRENT_DESKTOP", "")
	mockExecOutput(t, noopExecOutput)
	for _, m := range Modes {
		got, err := m.topology("")
		require.NoError(t, err, m)
		assert.NotNil(t, got, m)
	}
}

func TestTopologyServe(t *testing.T) {
	rd := config.RuntimeDir("/run/user/1000/secrets-dispatcher")
	session := config.BusConfig{Type: config.BusSessionBus}
	private := config.BusConfig{Type: config.BusSocket, Path: rd.BackendBusSocket()}
	clients := config.BusConfig{Type: config.BusSockets, Path: rd.ClientSockets()}
	tk := newTakeover("", Provider{Kind: ProviderNone})

	for _, tc := range []struct {
		name           string
		topology       topology
		wantUpstream   config.BusConfig
		wantDownstream []config.BusConfig
	}{
		{"remote", remote{}, session, []config.BusConfig{clients}},
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
	tk := newTakeover("", Provider{Kind: ProviderNone})

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
			units, err := tc.topology.units("/usr/bin/secrets-dispatcher serve")
			require.NoError(t, err)
			assert.ElementsMatch(t, tc.wantUnits, slices.Collect(maps.Keys(units)))
			assert.Equal(t, tc.wantActivated, tc.topology.activated())
		})
	}
}

func TestNewTakeoverBackend(t *testing.T) {
	gk := Provider{Kind: ProviderGnomeKeyring}
	none := Provider{Kind: ProviderNone}

	assert.Equal(t, BackendGopass, newTakeover("", none).backend)
	assert.Equal(t, BackendGnomeKeyring, newTakeover("", gk).backend)
	assert.Equal(t, BackendGopass, newTakeover(BackendGopass, gk).backend)
	assert.Equal(t, Backend("/opt/b"), newTakeover("/opt/b", none).backend)
}

func TestInstallationOf(t *testing.T) {
	assert.Equal(t, uninstalled{}, installationOf(nil))
	assert.Equal(t, remoteInstall{}, installationOf([]systemd.Unit{proxyServiceUnit}))
	assert.Equal(t, takeoverInstall{}, installationOf(allUnitNames))
	assert.Equal(t, takeoverInstall{}, installationOf([]systemd.Unit{busSocketUnit}))
}

func TestUninstalledProblems(t *testing.T) {
	assert.Empty(t, uninstalled{}.problems(statusState{}))
	assert.Equal(t, []string{
		"leftover D-Bus activation mask but no units installed — run `secrets-dispatcher service uninstall` to restore",
		"leftover gnome-keyring autostart shadow but no units installed — run `secrets-dispatcher service uninstall` to restore",
	}, uninstalled{}.problems(statusState{masked: true, shadowed: true}))
}

func TestRemoteProblems(t *testing.T) {
	healthy := statusState{
		provider: Provider{Kind: ProviderGnomeKeyring},
		units:    map[systemd.Unit]unitState{proxyServiceUnit: "active"},
	}
	assert.Empty(t, remoteInstall{}.problems(healthy))

	assert.Equal(t, []string{
		"remote topology installed but local takeover state present (mask/demotion) — run `service uninstall`, then reinstall",
		string(proxyServiceUnit) + " is missing while other units are installed — reinstall or uninstall",
		"org.freedesktop.secrets has no owner — no Secret Service provider is running upstream, so secret lookups will hang",
	}, remoteInstall{}.problems(statusState{
		provider: Provider{Kind: ProviderNone},
		masked:   true,
		units:    map[systemd.Unit]unitState{backendServiceUnit: "active"},
	}))

	assert.Equal(t, []string{
		string(proxyServiceUnit) + " is failed",
		"org.freedesktop.secrets has no owner — no Secret Service provider is running upstream, so secret lookups will hang",
	}, remoteInstall{}.problems(statusState{
		provider: Provider{Kind: ProviderNone},
		units:    map[systemd.Unit]unitState{proxyServiceUnit: "failed"},
	}))
}

func TestTakeoverProblems(t *testing.T) {
	allActive := map[systemd.Unit]unitState{busSocketUnit: "active", backendServiceUnit: "active", proxyServiceUnit: "active"}
	healthy := statusState{
		provider: Provider{Kind: ProviderDispatcher},
		masked:   true,
		units:    allActive,
	}
	assert.Empty(t, takeoverInstall{}.problems(healthy))

	owner := Provider{Kind: ProviderGnomeKeyring}
	assert.Equal(t, []string{
		"installed for takeover but NOT in front — the owner is " + owner.String() + " (re-grab? try `systemctl --user restart " + string(proxyServiceUnit) + "`)",
		"D-Bus activation of org.freedesktop.secrets is not masked — the stock provider can be activated back in front",
		"gnome-keyring demotion is inconsistent (drop-in: true, autostart shadow: false) — reinstall or uninstall to converge",
		string(busSocketUnit) + " is failed",
		string(proxyServiceUnit) + " is inactive",
	}, takeoverInstall{}.problems(statusState{
		provider: owner,
		demoted:  true,
		units:    map[systemd.Unit]unitState{busSocketUnit: "failed", backendServiceUnit: "active", proxyServiceUnit: "inactive"},
	}))
}
