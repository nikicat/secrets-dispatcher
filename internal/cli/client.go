// Package cli provides a client for the secrets-dispatcher API.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/nikicat/secrets-dispatcher/internal/apiclient"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/config"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
)

type Client struct {
	api apiclient.Client
}

func NewClient(serverAddr config.ListenAddr, token config.AuthToken) *Client {
	return &Client{api: apiclient.New(serverAddr.BaseURL(), token, &http.Client{Timeout: 10 * time.Second})}
}

// The metadata of a secret item.
type ItemInfo struct {
	Path       string                    `json:"path"`
	Label      string                    `json:"label"`
	Attributes dbustypes.Attributes      `json:"attributes"`
	Collection *dbustypes.CollectionName `json:"collection,omitempty"`
}

// Returns the item's label, or its path when it has no label.
func (i ItemInfo) DisplayLabel() string {
	if i.Label != "" {
		return i.Label
	}
	return i.Path
}

type GPGSignInfo struct {
	RepoName     approval.RepoName    `json:"repo_name"`
	Kind         approval.GPGSignKind `json:"kind"`
	CommitMsg    string               `json:"commit_msg"`
	Author       string               `json:"author"`
	Committer    string               `json:"committer"`
	KeyID        approval.GPGKeyID    `json:"key_id"`
	ChangedFiles []string             `json:"changed_files"`
	ParentHash   approval.ObjectHash  `json:"parent_hash,omitempty"`
	TagName      approval.TagName     `json:"tag_name,omitempty"`
	Target       approval.ObjectHash  `json:"target,omitempty"`
	Pushee       approval.RemoteURL   `json:"pushee,omitempty"`
}

type RequestRecord struct {
	ID               approval.RequestID   `json:"id"`
	Client           approval.ClientName  `json:"client"`
	Items            []ItemInfo           `json:"items"`
	Session          dbus.ObjectPath      `json:"session"`
	CreatedAt        time.Time            `json:"created_at"`
	Type             approval.RequestType `json:"type"`
	SearchAttributes dbustypes.Attributes `json:"search_attributes,omitempty"`
	GPGSignInfo      *GPGSignInfo         `json:"gpg_sign_info,omitempty"`
	SenderInfo       sender.Info          `json:"sender_info"`
}

type PendingEntry struct {
	Request   RequestRecord
	ExpiresAt time.Time
}

// The JSON of a PendingEntry.
type pendingEntryJSON struct {
	RequestRecord
	ExpiresAt time.Time `json:"expires_at"`
}

func (e PendingEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(pendingEntryJSON{e.Request, e.ExpiresAt})
}

func (e *PendingEntry) UnmarshalJSON(data []byte) error {
	var w pendingEntryJSON
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	*e = PendingEntry{Request: w.RequestRecord, ExpiresAt: w.ExpiresAt}
	return nil
}

type HistoryEntry struct {
	Request    RequestRecord       `json:"request"`
	Resolution approval.Resolution `json:"resolution"`
	ResolvedAt time.Time           `json:"resolved_at"`
}

type Entry interface {
	Record() RequestRecord
	fmt.Stringer
}

func (e PendingEntry) Record() RequestRecord { return e.Request }
func (e HistoryEntry) Record() RequestRecord { return e.Request }

// The answer of the pending endpoint.
type PendingResponse struct {
	Requests []PendingEntry `json:"requests"`
}

// The answer of the history endpoint.
type HistoryResponse struct {
	Entries []HistoryEntry `json:"entries"`
}

// Returns every pending request.
func (c *Client) Pending(ctx context.Context) ([]PendingEntry, error) {
	result, err := apiclient.GetJSON[PendingResponse](ctx, c.api, "/api/v1/pending")
	return result.Requests, err
}

// Returns the resolved requests.
func (c *Client) History(ctx context.Context) ([]HistoryEntry, error) {
	result, err := apiclient.GetJSON[HistoryResponse](ctx, c.api, "/api/v1/log")
	return result.Entries, err
}

// A request ID as the user types it, whole or only its start.
type IDPrefix string

// Returns the pending request or History entry that the given prefix names,
// as matchID matches it.
func (c *Client) Find(ctx context.Context, prefix IDPrefix) (Entry, error) {
	requests, err := c.Pending(ctx)
	if err != nil {
		return nil, err
	}

	entries, err := c.History(ctx)
	if err != nil {
		return nil, err
	}

	candidates := make([]Entry, 0, len(requests)+len(entries))
	for _, req := range requests {
		candidates = append(candidates, req)
	}
	for _, e := range entries {
		candidates = append(candidates, e)
	}

	return matchID(prefix, candidates)
}

// What the user decides about a pending request.
type Decision interface {
	// Returns the action on the request that makes the decision.
	action() apiclient.Action
	resolution() approval.Resolution
}

// Approves a pending request.
type Approve struct{}

// Denies a pending request.
type Deny struct{}

func (Approve) action() apiclient.Action        { return "approve" }
func (Approve) resolution() approval.Resolution { return approval.ResolutionApproved }
func (Deny) action() apiclient.Action           { return "deny" }
func (Deny) resolution() approval.Resolution    { return approval.ResolutionDenied }

// Resolves the pending request that the given prefix names with the given
// decision, and returns the request's ID. The prefix matches as matchID does.
func (c *Client) Resolve(ctx context.Context, prefix IDPrefix, d Decision) (approval.RequestID, error) {
	requests, err := c.Pending(ctx)
	if err != nil {
		return "", err
	}
	req, err := matchID(prefix, requests)
	if err != nil {
		return "", err
	}

	if err := c.api.PostAction(ctx, req.Request.ID, d.action()); err != nil {
		return "", err
	}
	return req.Request.ID, nil
}

// Returns the entry whose ID is the given prefix, or else the one entry whose
// ID starts with it. Errors when no entry matches, or when several do.
func matchID[T Entry](want IDPrefix, entries []T) (T, error) {
	var zero T
	var matches []T
	for _, entry := range entries {
		id := entry.Record().ID
		if id == approval.RequestID(want) {
			return entry, nil
		}
		if strings.HasPrefix(string(id), string(want)) {
			matches = append(matches, entry)
		}
	}

	switch len(matches) {
	case 0:
		return zero, fmt.Errorf("no request found matching: %s", want)
	case 1:
		return matches[0], nil
	default:
		return zero, fmt.Errorf("ambiguous ID %q matches %d requests", want, len(matches))
	}
}
