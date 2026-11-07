// Package approval manages pending secret access requests requiring user approval.
package approval

import "fmt"

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
	Action           RuleAction        `json:"action,omitempty"`
	RequestTypes     []RequestType     `json:"request_types,omitempty"`
	Process          *ProcessMatcher   `json:"process,omitempty"`
	Secret           *SecretMatcher    `json:"secret,omitempty"`
	SearchAttributes map[string]string `json:"search_attributes,omitempty"`
}

// RuleAction is what a matching trust rule does with a request.
type RuleAction string

const (
	RuleApprove RuleAction = "approve"
	// RuleIgnore drops the request silently. Config allows it only for writes.
	RuleIgnore RuleAction = "ignore"
	RuleDeny   RuleAction = "deny"
)

// UnmarshalText parses an action, so that a config file or API body naming an
// unknown action fails to load. An empty action reads as RuleApprove.
func (a *RuleAction) UnmarshalText(text []byte) error {
	switch v := RuleAction(text); v {
	case "":
		*a = RuleApprove
	case RuleApprove, RuleIgnore, RuleDeny:
		*a = v
	default:
		return fmt.Errorf("action must be %q, %q or %q, got %q", RuleApprove, RuleIgnore, RuleDeny, v)
	}
	return nil
}

// Restrictive reports whether the action withholds the secret. A restrictive
// rule fires when any requested item is in its scope, a permissive one only
// when every item is.
func (a RuleAction) Restrictive() bool {
	return a == RuleDeny || a == RuleIgnore
}

// ProcessMatcher matches against sender process attributes.
type ProcessMatcher struct {
	Exe  string `json:"exe,omitempty"`
	Name string `json:"name,omitempty"`
	Args string `json:"args,omitempty"`
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
	InvokerName  string        `json:"invoker_name"`            // Invoker process comm (display); spoofable — NOT the systemd unit
	SystemdUnit  string        `json:"systemd_unit,omitempty"`  // Real systemd unit (from GetUnitByPID); authoritative, matched by the `unit` rule
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
