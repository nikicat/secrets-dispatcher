// Package approval manages pending secret access requests requiring user approval.
package approval

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
	"github.com/nikicat/secrets-dispatcher/internal/unixsock"
)

// The way a request came in: a bus, the SSH agent socket or a remote. Trust
// rules match on it.
type ClientName string

// Client names the dispatcher gives its own clients. No forwarded bus may take one.
const (
	// The session bus, and the local API socket that the gpg-sign thin client uses.
	ClientLocal ClientName = "local"
	// The SSH agent socket.
	ClientSSHAgent ClientName = "ssh-agent"
	// A request that came over a connection with no unix peer, such as one to the
	// API's TCP listener.
	ClientUnknown ClientName = "unknown"
)

// Returns the client name of the bus forwarded through the given socket, as
// unixsock.Path.Name reads it. Errors when that name is empty or one the
// dispatcher reserves.
func ClientFromSocket(socket unixsock.Path) (ClientName, error) {
	name := ClientName(socket.Name())
	if name == "" {
		return "", fmt.Errorf("socket %s has no client name", socket)
	}
	if slices.Contains([]ClientName{ClientLocal, ClientSSHAgent, ClientUnknown}, name) {
		return "", fmt.Errorf("socket %s takes the reserved client name %q", socket, name)
	}
	return name, nil
}

// Reports whether anything on this machine describes the sender's process. A
// sender on a forwarded bus has neither a process chain nor a unit.
func (s SenderInfo) hasLocalProcess() bool {
	return len(s.ProcessChain) > 0 || s.SystemdUnit != ""
}

// A rule from the config file. Rules take effect on restart, and they match
// anywhere in the caller's process chain (ADR 0005).
type TrustRule struct {
	Name         string        `yaml:"name,omitempty" json:"name,omitempty"`
	Action       RuleAction    `yaml:"action,omitempty" json:"action,omitempty"`
	RequestTypes []RequestType `yaml:"request_types,omitempty" json:"request_types,omitempty"`
	// Matched against the client the request came through. The session bus
	// is ClientLocal, and ClientFromSocket names a forwarded bus. When omitted, a
	// deny rule matches every client and any other rule only the session bus.
	Client           Glob            `yaml:"client,omitempty" json:"client,omitzero"`
	Process          *ProcessMatcher `yaml:"process,omitempty" json:"process,omitempty"`
	Secret           *SecretMatcher  `yaml:"secret,omitempty" json:"secret,omitempty"`
	SearchAttributes map[string]Glob `yaml:"search_attributes,omitempty" json:"search_attributes,omitempty"`
}

var (
	anyClient   = MustGlob("*")
	localClient = MustGlob(string(ClientLocal))
)

// Returns the glob a request's client must match.
func (r *TrustRule) clientGlob() Glob {
	switch {
	case !r.Client.IsZero():
		return r.Client
	case r.Action == RuleDeny:
		return anyClient
	default:
		return localClient
	}
}

// Checks whether the request satisfies every matcher that r sets (ADR 0005).
func (r *TrustRule) matches(info RequestInfo) bool {
	if len(r.RequestTypes) > 0 && !slices.Contains(r.RequestTypes, info.Type) {
		return false
	}
	if !r.clientGlob().Matches(string(info.SenderInfo.Client)) {
		return false
	}
	if r.Process != nil && !r.Process.matches(info.SenderInfo) {
		return false
	}
	if r.Secret != nil && !r.Secret.matches(info.Items, r.Action) {
		return false
	}
	if len(r.SearchAttributes) > 0 && !attributesMatch(r.SearchAttributes, info.SearchAttributes) {
		return false
	}
	return true
}

// The request types a trust rule may name. Signing requests go through
// trusted signers instead.
var ruleRequestTypes = []RequestType{
	RequestTypeGetSecret,
	RequestTypeSearch,
	RequestTypeDelete,
	RequestTypeWrite,
	RequestTypeUnlock,
}

// Errors when r names a request type that no trust rule may name, or one that
// its action does not support, or when an attribute map holds an empty glob.
func (r TrustRule) Validate() error {
	if r.Secret != nil {
		if err := noEmptyGlob(r.Secret.Attributes); err != nil {
			return fmt.Errorf("secret.attributes: %w", err)
		}
	}
	if err := noEmptyGlob(r.SearchAttributes); err != nil {
		return fmt.Errorf("search_attributes: %w", err)
	}
	for _, rt := range r.RequestTypes {
		if !slices.Contains(ruleRequestTypes, rt) {
			return fmt.Errorf("invalid request_type %q", rt)
		}
	}
	if r.Action == RuleIgnore {
		if len(r.RequestTypes) == 0 {
			return fmt.Errorf("action %q requires non-empty request_types", r.Action)
		}
		for _, rt := range r.RequestTypes {
			if rt != RequestTypeWrite {
				return fmt.Errorf("action %q only supports request_types [%q], got %q", r.Action, RequestTypeWrite, rt)
			}
		}
	}
	return nil
}

// A null map value, such as `service:` in YAML, decodes to the zero Glob
// without reaching Glob.UnmarshalText.
func noEmptyGlob(attrs map[string]Glob) error {
	for k, g := range attrs {
		if g.IsZero() {
			return fmt.Errorf("%s: %w", k, errEmptyGlob)
		}
	}
	return nil
}

// Returns a copy of rules with every empty Action set to RuleApprove.
func WithDefaultActions(rules []TrustRule) []TrustRule {
	out := slices.Clone(rules)
	for i := range out {
		if out[i].Action == "" {
			out[i].Action = RuleApprove
		}
	}
	return out
}

// The name of a repository's top-level directory, as the gpg thin client
// reports it (ADR 0008).
type RepoName string

// An app whose git signing requests are approved without a prompt (ADR 0005).
// An optional field that is left empty does not narrow the match.
type TrustedSigner struct {
	// The app that starts the commit, such as an editor or a deploy script. It may
	// be anywhere in the requester's process chain, above git (ADR 0005).
	ExePath procutil.Path `yaml:"exe_path" json:"exe_path"`
	// The key stays repo_path, so existing config files still load
	// (TRUST-RULES.md, "Trusted signers").
	RepoName RepoName `yaml:"repo_path,omitempty" json:"repo_path,omitempty"`
	// A prefix that every staged file must have. The paths are relative to the
	// repository's top level (ADR 0008).
	FilePrefix string `yaml:"file_prefix,omitempty" json:"file_prefix,omitempty"`
}

// Errors when ExePath is empty.
func (s TrustedSigner) Validate() error {
	if s.ExePath == "" {
		return errors.New("exe_path is required")
	}
	return nil
}

func (s TrustedSigner) matches(chain []procutil.Process, repoName RepoName, changedFiles []string) bool {
	if !slices.ContainsFunc(chain, func(p procutil.Process) bool {
		return p.Exe != nil && *p.Exe == s.ExePath
	}) {
		return false
	}
	if s.RepoName != "" && repoName != s.RepoName {
		return false
	}
	return !slices.ContainsFunc(changedFiles, func(f string) bool {
		return !strings.HasPrefix(f, s.FilePrefix)
	})
}

// What a matching trust rule does with a request (ADR 0005).
type RuleAction string

const (
	RuleApprove RuleAction = "approve"
	// Drops the request silently. TrustRule.Validate allows it only for write
	// requests (ADR 0005).
	RuleIgnore RuleAction = "ignore"
	RuleDeny   RuleAction = "deny"
)

// Errors when text names an unknown action. Empty text leaves the action empty.
func (a *RuleAction) UnmarshalText(text []byte) error {
	switch v := RuleAction(text); v {
	case "", RuleApprove, RuleIgnore, RuleDeny:
		*a = v
	default:
		return fmt.Errorf("action must be %q, %q or %q, got %q", RuleApprove, RuleIgnore, RuleDeny, v)
	}
	return nil
}

// Checks whether the action withholds the secret. A restrictive rule fires when
// any requested item is in its scope, and a permissive one only when every item
// is (ADR 0005).
func (a RuleAction) Restrictive() bool {
	return a == RuleDeny || a == RuleIgnore
}

// Returns how a request ends when a rule with this action decides it (ADR 0005),
// and the error the request then fails with, or nil when the action approves.
func (a RuleAction) outcome() (Resolution, error) {
	switch a {
	case RuleIgnore:
		return ResolutionIgnored, ErrIgnored
	case RuleDeny:
		return ResolutionDenied, ErrDeniedByRule
	}
	return ResolutionAutoApproved, nil
}

// Selects requests by the processes in the sender's chain. Only Exe and Unit
// are authoritative (ADR 0005). An omitted field constrains nothing, but no
// ProcessMatcher matches a sender without a local process (ADR 0005).
type ProcessMatcher struct {
	// A process anywhere in the chain must have a matching /proc/PID/exe (ADR 0005).
	Exe Glob `yaml:"exe,omitempty" json:"exe,omitzero"`
	// A process anywhere in the chain must have a matching comm. A process sets
	// its own comm, so this field is advisory (ADR 0005).
	Name Glob `yaml:"name,omitempty" json:"name,omitzero"`
	// A process anywhere in the chain must have a command-line argument that
	// matches. A process can rewrite its own argv, so this field is advisory
	// (ADR 0005).
	Args Glob `yaml:"args,omitempty" json:"args,omitzero"`
	// A process anywhere in the chain must have a matching working directory
	// (ADR 0005).
	CWD Glob `yaml:"cwd,omitempty" json:"cwd,omitzero"`
	// The caller's systemd unit must match. systemd resolves the unit, so the
	// caller cannot forge it (ADR 0005).
	Unit Glob `yaml:"unit,omitempty" json:"unit,omitzero"`
}

func (pm *ProcessMatcher) matches(senderInfo SenderInfo) bool {
	if !senderInfo.hasLocalProcess() {
		return false
	}

	chain := senderInfo.ProcessChain
	if !pm.Exe.IsZero() && !slices.ContainsFunc(chain, func(p procutil.Process) bool {
		return p.Exe != nil && pm.Exe.Matches(string(*p.Exe))
	}) {
		return false
	}
	if !pm.Name.IsZero() && !slices.ContainsFunc(chain, func(p procutil.Process) bool {
		return pm.Name.Matches(p.Name)
	}) {
		return false
	}
	if !pm.Args.IsZero() && !slices.ContainsFunc(chain, func(p procutil.Process) bool {
		return slices.ContainsFunc(p.Args, pm.Args.Matches)
	}) {
		return false
	}
	if !pm.CWD.IsZero() && !slices.ContainsFunc(chain, func(p procutil.Process) bool {
		return p.CWD != nil && pm.CWD.Matches(string(*p.CWD))
	}) {
		return false
	}
	if !pm.Unit.IsZero() && !pm.Unit.Matches(string(senderInfo.SystemdUnit)) {
		return false
	}
	return true
}

// Selects requests by the secret items they name. An omitted field constrains
// nothing, and the fields that are set must all match (ADR 0005).
type SecretMatcher struct {
	// The collection segment of the item's object path must match. That segment
	// is a collection name, or an alias such as default.
	Collection Glob `yaml:"collection,omitempty" json:"collection,omitzero"`
	Label      Glob `yaml:"label,omitempty" json:"label,omitzero"`
	// A missing attribute reads as the empty string.
	Attributes map[string]Glob `yaml:"attributes,omitempty" json:"attributes,omitempty"`
}

// Checks whether the items are in the scope that the given action needs
// (ADR 0005). An empty matcher matches any items, and any other matcher never
// matches an empty list.
func (sm *SecretMatcher) matches(items []ItemInfo, action RuleAction) bool {
	if sm.Collection.IsZero() && sm.Label.IsZero() && len(sm.Attributes) == 0 {
		return true
	}
	if action.Restrictive() {
		return slices.ContainsFunc(items, sm.matchesItem)
	}
	return len(items) > 0 && !slices.ContainsFunc(items, func(it ItemInfo) bool {
		return !sm.matchesItem(it)
	})
}

func (sm *SecretMatcher) matchesItem(item ItemInfo) bool {
	if !sm.Collection.IsZero() && !sm.Collection.Matches(item.Collection()) {
		return false
	}
	if !sm.Label.IsZero() && !sm.Label.Matches(item.Label) {
		return false
	}
	return attributesMatch(sm.Attributes, item.Attributes)
}

func attributesMatch(ruleAttrs map[string]Glob, reqAttrs map[string]string) bool {
	for k, v := range ruleAttrs {
		if !v.Matches(reqAttrs[k]) {
			return false
		}
	}
	return true
}

// The sender of a request, which trust rules match against (ADR 0005).
type SenderInfo struct {
	sender.Info
	// The client the request came through.
	Client ClientName `json:"-"`
	// True if the request came through the gpg-sign thin client (ADR 0008).
	PeerTrusted bool `json:"peer_trusted,omitempty"`
}
