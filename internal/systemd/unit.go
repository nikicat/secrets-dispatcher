// Package systemd holds what the packages that install systemd units share.
package systemd

import "path/filepath"

// The name of a systemd unit, such as "secrets-dispatcher.service".
type Unit string

// Returns the path of u's unit file in the unit directory dir.
func (u Unit) Path(dir string) string { return filepath.Join(dir, string(u)) }
