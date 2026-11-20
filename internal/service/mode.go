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

// Returns the topology that m sets up, with b as a takeover's private backend.
// Detects the provider that a takeover displaces. Errors when m names no mode,
// or when b is set for remote, which has no private backend.
func (m Mode) topology(ctx context.Context, b *Backend) (topology, error) {
	switch m {
	case ModeRemote:
		if b != nil {
			return nil, fmt.Errorf("a backend applies only to %s and %s modes, not %s", ModeLocal, ModeFull, ModeRemote)
		}
		return remote{}, nil
	case ModeLocal:
		return newTakeover(b, DetectProvider(ctx)), nil
	case ModeFull:
		return full{newTakeover(b, DetectProvider(ctx))}, nil
	}
	return nil, Modes.Unknown(m)
}
