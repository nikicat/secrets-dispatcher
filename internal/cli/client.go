// Package cli provides a client for the secrets-dispatcher API.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client communicates with the secrets-dispatcher API.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewClient creates a new API client.
func NewClient(serverAddr, token string) *Client {
	return &Client{
		baseURL: "http://" + serverAddr,
		token:   token,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// SenderInfo contains information about the D-Bus sender process.
type SenderInfo struct {
	Sender   string `json:"sender"`
	PID      uint32 `json:"pid"`
	UID      uint32 `json:"uid"`
	UserName string `json:"user_name"`
	UnitName string `json:"unit_name"`
}

// ItemInfo contains metadata about a secret item.
type ItemInfo struct {
	Path       string            `json:"path"`
	Label      string            `json:"label"`
	Attributes map[string]string `json:"attributes"`
}

// GPGSignInfo carries commit context for a gpg_sign approval request.
// This is an intentional duplication of approval.GPGSignInfo — the cli package
// deliberately does not import internal/approval or internal/api.
type GPGSignInfo struct {
	RepoName     string   `json:"repo_name"`
	CommitMsg    string   `json:"commit_msg"`
	Author       string   `json:"author"`
	Committer    string   `json:"committer"`
	KeyID        string   `json:"key_id"`
	Fingerprint  string   `json:"fingerprint,omitempty"`
	ChangedFiles []string `json:"changed_files"`
	ParentHash   string   `json:"parent_hash,omitempty"`
}

// PendingRequest represents a pending approval request.
type PendingRequest struct {
	ID               string            `json:"id"`
	Client           string            `json:"client"`
	Items            []ItemInfo        `json:"items"`
	Session          string            `json:"session"`
	CreatedAt        time.Time         `json:"created_at"`
	ExpiresAt        time.Time         `json:"expires_at"`
	Type             string            `json:"type"`
	SearchAttributes map[string]string `json:"search_attributes,omitempty"`
	GPGSignInfo      *GPGSignInfo      `json:"gpg_sign_info,omitempty"`
	SenderInfo       SenderInfo        `json:"sender_info"`
}

// HistoryEntry represents a resolved approval request.
type HistoryEntry struct {
	Request    PendingRequest `json:"request"`
	Resolution string         `json:"resolution"`
	ResolvedAt time.Time      `json:"resolved_at"`
}

// PendingResponse is the response from the pending endpoint.
type PendingResponse struct {
	Requests []PendingRequest `json:"requests"`
}

// HistoryResponse is the response from the history endpoint.
type HistoryResponse struct {
	Entries []HistoryEntry `json:"entries"`
}

// ActionResponse is the response from approve/deny endpoints.
type ActionResponse struct {
	Status string `json:"status"`
}

// ErrorResponse is an error response from the API.
type ErrorResponse struct {
	Error string `json:"error"`
}

// List returns all pending requests.
func (c *Client) List() ([]PendingRequest, error) {
	resp, err := c.get("/api/v1/pending")
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
	resp, err := c.get("/api/v1/log")
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

// Approve approves a request by ID (supports partial ID).
func (c *Client) Approve(id string) error {
	fullID, err := c.resolveID(id)
	if err != nil {
		return err
	}
	return c.action(fullID, "approve")
}

// Deny denies a request by ID (supports partial ID).
func (c *Client) Deny(id string) error {
	fullID, err := c.resolveID(id)
	if err != nil {
		return err
	}
	return c.action(fullID, "deny")
}

// Show returns a single request by ID (supports partial ID).
func (c *Client) Show(id string) (*PendingRequest, error) {
	fullID, err := c.resolveID(id)
	if err != nil {
		return nil, err
	}

	requests, err := c.List()
	if err != nil {
		return nil, err
	}

	for _, req := range requests {
		if req.ID == fullID {
			return &req, nil
		}
	}
	return nil, fmt.Errorf("request not found: %s", id)
}

func (c *Client) action(id, action string) error {
	resp, err := c.post("/api/v1/pending/" + id + "/" + action)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}
	return nil
}

func (c *Client) resolveID(partial string) (string, error) {
	requests, err := c.List()
	if err != nil {
		return "", err
	}

	var matches []string
	for _, req := range requests {
		if req.ID == partial {
			return partial, nil // exact match
		}
		if strings.HasPrefix(req.ID, partial) {
			matches = append(matches, req.ID)
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no request found matching: %s", partial)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("ambiguous ID %q matches %d requests", partial, len(matches))
	}
}

func (c *Client) get(path string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	return c.httpClient.Do(req)
}

func (c *Client) post(path string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
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
