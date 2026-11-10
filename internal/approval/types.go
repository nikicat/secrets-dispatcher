// Package approval manages pending secret access requests requiring user approval.
package approval

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// Client names the dispatcher gives its own clients. No forwarded bus may take one.
const (
	// The session bus.
	ClientLocal = "local"
	// The SSH agent socket.
	ClientSSHAgent = "ssh-agent"
	// A client that was given no name.
	ClientUnknown = "unknown"
)

// Returns the client name of the bus forwarded through socketPath, the file's
// name without ".sock". Errors when that name is empty or one the dispatcher
// reserves.
func ClientFromSocket(socketPath string) (string, error) {
	name := strings.TrimSuffix(filepath.Base(socketPath), ".sock")
	if name == "" {
		return "", fmt.Errorf("socket %s has no client name", socketPath)
	}
	if slices.Contains([]string{ClientLocal, ClientSSHAgent, ClientUnknown}, name) {
		return "", fmt.Errorf("socket %s takes the reserved client name %q", socketPath, name)
	}
	return name, nil
}

// Reports whether anything on this machine describes the sender's process. A
// sender on a forwarded bus has neither a process chain nor a unit.
func (s SenderInfo) hasLocalProcess() bool {
	return len(s.ProcessChain) > 0 || s.SystemdUnit != ""
}

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
	Name         string        `json:"name,omitempty"`
	Action       RuleAction    `json:"action,omitempty"`
	RequestTypes []RequestType `json:"request_types,omitempty"`
	// A glob matched against the client the request came through. The session bus
	// is ClientLocal, and ClientFromSocket names a forwarded bus. When empty, a
	// deny rule matches every client and any other rule only the session bus.
	Client           string            `json:"client,omitempty"`
	Process          *ProcessMatcher   `json:"process,omitempty"`
	Secret           *SecretMatcher    `json:"secret,omitempty"`
	SearchAttributes map[string]string `json:"search_attributes,omitempty"`
}

// Returns the glob a request's client must match.
func (r *TrustRule) clientGlob() string {
	switch {
	case r.Client != "":
		return r.Client
	case r.Action == RuleDeny:
		return "*"
	default:
		return ClientLocal
	}
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
	Client       string        `json:"-"`                       // The client the request came through, matched by TrustRule.Client
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
