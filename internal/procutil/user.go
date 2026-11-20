package procutil

import (
	"fmt"
	"strconv"
)

// The id of a user on this machine, as the kernel reports it for a process or
// a file.
type UID uint32

// The id of a group on this machine.
type GID uint32

// The superuser.
const RootUID UID = 0

func (u UID) String() string { return strconv.FormatUint(uint64(u), 10) }

func (g GID) String() string { return strconv.FormatUint(uint64(g), 10) }

// Parses s, a decimal user id as os/user reports one. Errors when s is not one.
func ParseUID(s string) (UID, error) { return parseID[UID]("UID", s) }

// Parses s, a decimal group id as os/user reports one. Errors when s is not one.
func ParseGID(s string) (GID, error) { return parseID[GID]("GID", s) }

func parseID[T ~uint32](kind, s string) (T, error) {
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("parse %s %q: %w", kind, s, err)
	}
	return T(n), nil
}
