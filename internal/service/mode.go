package service

import (
	"fmt"
	"slices"

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
var Modes = []Mode{ModeLocal, ModeRemote, ModeFull}

func (m Mode) String() string { return string(m) }

// Errors when value names no mode.
func (m *Mode) Set(value string) error {
	v := Mode(value)
	if err := v.validate(); err != nil {
		return err
	}
	*m = v
	return nil
}

// Errors when m names no mode, as an empty Mode does.
func (m Mode) validate() error {
	if !slices.Contains(Modes, m) {
		return m.unknown()
	}
	return nil
}

func (m Mode) unknown() error {
	return fmt.Errorf("unknown mode %q (must be %s)", m, config.Alternatives(Modes))
}

// Returns the topology that m sets up, with b as a takeover's private backend.
// Detects the provider that a takeover displaces. Errors when m names no mode,
// or when b is set for remote, which has no private backend.
func (m Mode) topology(b Backend) (topology, error) {
	switch m {
	case ModeRemote:
		if b != "" {
			return nil, fmt.Errorf("a backend applies only to %s and %s modes, not %s", ModeLocal, ModeFull, ModeRemote)
		}
		return remote{}, nil
	case ModeLocal:
		return newTakeover(b, DetectProvider()), nil
	case ModeFull:
		return full{newTakeover(b, DetectProvider())}, nil
	}
	return nil, m.unknown()
}
