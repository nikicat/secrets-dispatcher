package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/config"
)

func TestInstalledModeRoundTrip(t *testing.T) {
	rd := config.RuntimeDir(fakeRuntimeDir)
	for _, m := range Modes.Values() {
		t.Run(m.String(), func(t *testing.T) {
			served, err := m.served(rd)
			require.NoError(t, err)

			got, err := installedMode(served, rd)
			require.NoError(t, err)
			assert.Equal(t, m, got)
		})
	}
}

func TestInstalledModeOfFirstInstall(t *testing.T) {
	got, err := installedMode(config.TopologyConfig{}, config.RuntimeDir(fakeRuntimeDir))
	require.NoError(t, err)
	assert.Equal(t, DefaultMode, got)
}

func TestInstalledModeRefusesATopologyOfNoMode(t *testing.T) {
	served := config.TopologyConfig{Downstream: []config.BusConfig{{Bus: config.Socket{Path: "/tmp/bus.sock"}}}}
	_, err := installedMode(served, config.RuntimeDir(fakeRuntimeDir))
	assert.ErrorContains(t, err, "none of the install modes")
}
