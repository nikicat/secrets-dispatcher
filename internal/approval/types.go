// Package approval manages pending secret access requests requiring user approval.
package approval

// ProcessInfo represents a single process in the process chain.
type ProcessInfo struct {
	Name string   `json:"name"`
	PID  uint32   `json:"pid"`
	Exe  string   `json:"exe,omitempty"`
	Args []string `json:"args,omitempty"`
	CWD  string   `json:"cwd,omitempty"`
}

// TrustRule defines a persistent declarative rule from config for auto-approving, ignoring, or denying requests.
type TrustRule struct {
	Name             string            `json:"name,omitempty"`
	Action           string            `json:"action,omitempty"`
	RequestTypes     []string          `json:"request_types,omitempty"`
	Process          *ProcessMatcher   `json:"process,omitempty"`
	Secret           *SecretMatcher    `json:"secret,omitempty"`
	SearchAttributes map[string]string `json:"search_attributes,omitempty"`
}

// ProcessMatcher matches against sender process attributes.
type ProcessMatcher struct {
	Exe  string `json:"exe,omitempty"`
	Name string `json:"name,omitempty"`
	CWD  string `json:"cwd,omitempty"`
	Unit string `json:"unit,omitempty"`
}

// SecretMatcher matches against secret/item attributes.
type SecretMatcher struct {
	Collection string            `json:"collection,omitempty"`
	Label      string            `json:"label,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// SenderInfo contains information about the D-Bus sender process.
type SenderInfo struct {
	Sender       string        `json:"sender"`                  // D-Bus unique name (":1.123")
	PID          uint32        `json:"pid"`                     // Process ID
	UID          uint32        `json:"uid"`                     // User ID
	UserName     string        `json:"user_name"`               // Username (may be empty if lookup fails)
	UnitName     string        `json:"unit_name"`               // Systemd unit (may be empty)
	ProcessChain []ProcessInfo `json:"process_chain,omitempty"` // Full process chain from requestor to init
	// PeerTrusted reports whether the process that opened the connection is a
	// trusted transport for this request — one whose self-reported, server-
	// unverifiable fields (repo name, changed files, commit object) we can rely on
	// because our own code produced them. Today the only trusted transport is our
	// gpg-sign thin client (the peer's exe is our own binary); the field is named
	// generally so other trusted transports can set it later. It gates the silent
	// trusted-signer path — see Manager.CheckTrustedSigner. Unset for requests that
	// speak the socket protocol directly.
	PeerTrusted bool `json:"peer_trusted,omitempty"`
}
