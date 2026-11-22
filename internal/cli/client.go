// Package cli provides a client for the secrets-dispatcher API.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
)

// Client communicates with the secrets-dispatcher API.
type Client struct {
	baseURL    string
	token      config.AuthToken
	httpClient *http.Client
}

// NewClient creates a new API client.
func NewClient(serverAddr config.ListenAddr, token config.AuthToken) *Client {
	return &Client{
		baseURL: serverAddr.BaseURL(),
		token:   token,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// ItemInfo contains metadata about a secret item.
type ItemInfo struct {
	Path       string            `json:"path"`
	Label      string            `json:"label"`
	Attributes map[string]string `json:"attributes"`
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
	SearchAttributes map[string]string    `json:"search_attributes,omitempty"`
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

// PendingResponse is the response from the pending endpoint.
type PendingResponse struct {
	Requests []PendingEntry `json:"requests"`
}

// HistoryResponse is the response from the history endpoint.
type HistoryResponse struct {
	Entries []HistoryEntry `json:"entries"`
}

// ErrorResponse is an error response from the API.
type ErrorResponse struct {
	Error string `json:"error"`
}

// Returns every pending request.
func (c *Client) Pending() ([]PendingEntry, error) {
	resp, err := c.do(http.MethodGet, "/api/v1/pending")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	var result PendingResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result.Requests, nil
}

// History returns resolved requests.
func (c *Client) History() ([]HistoryEntry, error) {
	resp, err := c.do(http.MethodGet, "/api/v1/log")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	var result HistoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result.Entries, nil
}

// A request ID as the user types it, whole or only its start.
type IDPrefix string

// Returns the pending request or History entry that the given prefix names,
// as matchID matches it.
func (c *Client) Find(prefix IDPrefix) (Entry, error) {
	requests, err := c.Pending()
	if err != nil {
		return nil, err
	}

	entries, err := c.History()
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
	// Returns the last segment of the request's route that makes the decision.
	route() string
	resolution() approval.Resolution
}

// Approves a pending request.
type Approve struct{}

// Denies a pending request.
type Deny struct{}

func (Approve) route() string                   { return "approve" }
func (Approve) resolution() approval.Resolution { return approval.ResolutionApproved }
func (Deny) route() string                      { return "deny" }
func (Deny) resolution() approval.Resolution    { return approval.ResolutionDenied }

// Resolves the pending request that the given prefix names with the given
// decision, and returns the request's ID. The prefix matches as matchID does.
func (c *Client) Resolve(prefix IDPrefix, d Decision) (approval.RequestID, error) {
	requests, err := c.Pending()
	if err != nil {
		return "", err
	}
	req, err := matchID(prefix, requests)
	if err != nil {
		return "", err
	}

	resp, err := c.do(http.MethodPost, "/api/v1/pending/"+string(req.Request.ID)+"/"+d.route())
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", c.parseError(resp)
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

func (c *Client) do(method, path string) (*http.Response, error) {
	req, err := http.NewRequest(method, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	c.token.Authorize(req.Header)
	return c.httpClient.Do(req)
}

func (c *Client) parseError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	var errResp ErrorResponse
	if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
		return fmt.Errorf("%s", errResp.Error)
	}
	return fmt.Errorf("request failed: %s", resp.Status)
}
