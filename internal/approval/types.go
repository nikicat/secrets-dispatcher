// Package approval manages pending secret access requests requiring user approval.
package approval

// SenderInfo contains information about the D-Bus sender process.
type SenderInfo struct {
	Sender   string `json:"sender"`    // D-Bus unique name (":1.123")
	PID      uint32 `json:"pid"`       // Process ID
	UID      uint32 `json:"uid"`       // User ID
	UserName string `json:"user_name"` // Username (may be empty if lookup fails)
	UnitName string `json:"unit_name"` // Systemd unit (may be empty)
}
