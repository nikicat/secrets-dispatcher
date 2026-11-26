package service

import (
	"context"
	"fmt"

	"github.com/nikicat/secrets-dispatcher/internal/config"
)

// The topology that an install sets up.
type Mode string

const (
	// Serves secrets to remote hosts and leaves this desktop's Secret Service
	// alone.
	ModeRemote Mode = "remote"
	// Gates this desktop's Secret Service.
	ModeLocal Mode = "local"
	// Does both.
	ModeFull Mode = "full"
)

// Every install mode.
var Modes = config.NewEnum("mode", ModeLocal, ModeRemote, ModeFull)

func (m Mode) String() string { return string(m) }

// Parses an install mode as config.Enum.Parse does.
func (m *Mode) UnmarshalText(text []byte) error {
	var err error
	*m, err = Modes.Parse(text)
	return err
}

// The mode of a first install.
const DefaultMode = ModeLocal

// Returns the mode that m names. A nil m means what a nil Options.Mode means.
// Errors as installedMode does.
func resolveMode(m *Mode, t config.TopologyConfig, rd config.RuntimeDir) (Mode, error) {
	if m != nil {
		return *m, nil
	}
	return installedMode(t, rd)
}

// Returns the mode whose topology serves what t serves, and DefaultMode when t
// serves nothing. Errors when no mode's topology serves it.
func installedMode(t config.TopologyConfig, rd config.RuntimeDir) (Mode, error) {
	if len(t.Downstream) == 0 {
		return DefaultMode, nil
	}
	for _, m := range Modes.Values() {
		served, err := m.served(rd)
		if err != nil {
			return "", err
		}
		if served.Equal(t) {
			return m, nil
		}
	}
	return "", fmt.Errorf("the config serves none of the install modes %s", Modes)
}

// Returns the serve topology that m sets up in rd. Errors when m names no mode.
func (m Mode) served(rd config.RuntimeDir) (config.TopologyConfig, error) {
	t, err := m.build(nil, func() Provider { return Provider{} })
	if err != nil {
		return config.TopologyConfig{}, err
	}
	return t.serve(rd), nil
}

// Returns the topology that Mode.build returns. A takeover displaces the
// provider that DetectProvider finds on the session bus. Errors as Mode.build
// does.
func (m Mode) topology(ctx context.Context, b *Backend) (topology, error) {
	return m.build(b, func() Provider { return DetectProvider(ctx) })
}

// Returns the topology that m sets up, with b as a takeover's private backend
// and detect's result as the provider that a takeover displaces. Calls detect
// only for a takeover. Errors when m names no mode, or when b is set for
// remote, which has no private backend.
func (m Mode) build(b *Backend, detect func() Provider) (topology, error) {
	switch m {
	case ModeRemote:
		if b != nil {
			return nil, fmt.Errorf("a backend applies only to %s and %s modes, not %s", ModeLocal, ModeFull, ModeRemote)
		}
		return remote{}, nil
	case ModeLocal:
		return newTakeover(b, detect()), nil
	case ModeFull:
		return full{newTakeover(b, detect())}, nil
	}
	return nil, Modes.Unknown(m)
}
