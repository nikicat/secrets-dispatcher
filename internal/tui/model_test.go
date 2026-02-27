package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/procchain"
)

// makeRequest creates a minimal approval.Request for test use.
func makeRequest(id, path string) *approval.Request {
	now := time.Now()
	return &approval.Request{
		ID:        id,
		Client:    "test-client",
		Type:      approval.RequestTypeGetSecret,
		Items:     []approval.ItemInfo{{Path: path}},
		CreatedAt: now,
		ExpiresAt: now.Add(5 * time.Minute),
		SenderInfo: approval.SenderInfo{
			PID: 1234,
			UID: 1000,
		},
	}
}

// makeGPGRequest creates a minimal gpg_sign approval.Request for test use.
func makeGPGRequest(id, repo, commitMsg string) *approval.Request {
	now := time.Now()
	return &approval.Request{
		ID:        id,
		Client:    "test-git",
		Type:      approval.RequestTypeGPGSign,
		CreatedAt: now,
		ExpiresAt: now.Add(5 * time.Minute),
		GPGSignInfo: &approval.GPGSignInfo{
			RepoName:     repo,
			CommitMsg:    commitMsg,
			Author:       "Test Author <test@example.com>",
			KeyID:        "DEADBEEF",
			ChangedFiles: []string{"README.md", "main.go"},
		},
		SenderInfo: approval.SenderInfo{
			PID: 5678,
			UID: 1000,
		},
	}
}

// newTestModel creates a Model with LockModeNone and captures approve/deny calls.
func newTestModel(t *testing.T, lockMode LockMode) (Model, *[]string, *[]string) {
	t.Helper()
	approved := &[]string{}
	denied := &[]string{}
	cfg := Config{
		LockMode:      lockMode,
		CompanionUser: "secrets-testuser",
		StartTime:     time.Now(),
	}
	approveFn := func(id string) error {
		*approved = append(*approved, id)
		return nil
	}
	denyFn := func(id string) error {
		*denied = append(*denied, id)
		return nil
	}
	m := NewModel(cfg, approveFn, denyFn)
	// Set a window size so View() doesn't return "Initializing...".
	m = updateModel(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	return m, approved, denied
}

// updateModel calls m.Update and returns the updated Model. Panics if the
// updated model is not a Model (indicates a bug in the model's Update).
func updateModel(m Model, msg tea.Msg) Model {
	updated, _ := m.Update(msg)
	return updated.(Model)
}

func TestModel_NewRequestAppearsInList(t *testing.T) {
	m, _, _ := newTestModel(t, LockModeNone)

	req := makeRequest("req-1", "/secrets/github/token")
	m = updateModel(m, NewRequestMsg{Request: req, ProcChain: nil})

	view := m.View()
	if view == "" {
		t.Fatal("View() returned empty string")
	}

	// The list pane should show SECRET badge and path.
	if !containsSubstr(view, "SECRET") {
		t.Errorf("View() does not contain [SECRET] badge; got:\n%s", view)
	}
	// The detail pane should show the secret path.
	if !containsSubstr(view, "/secrets/github/token") {
		t.Errorf("View() does not contain secret path; got:\n%s", view)
	}
}

func TestModel_ApproveKey(t *testing.T) {
	m, approved, _ := newTestModel(t, LockModeNone)

	req := makeRequest("req-approve", "/secrets/test")
	m = updateModel(m, NewRequestMsg{Request: req})

	// Press y — should call approveFunc.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("pressing 'y' should return a command; got nil")
	}

	// Execute the command to trigger the approve call.
	result := cmd()
	if result == nil {
		t.Fatal("approve command returned nil message")
	}
	approveMsg, ok := result.(ApproveResultMsg)
	if !ok {
		t.Fatalf("approve command returned %T, want ApproveResultMsg", result)
	}
	if approveMsg.ID != "req-approve" {
		t.Errorf("ApproveResultMsg.ID = %q, want %q", approveMsg.ID, "req-approve")
	}
	if approveMsg.Err != nil {
		t.Errorf("ApproveResultMsg.Err = %v, want nil", approveMsg.Err)
	}
	if len(*approved) != 1 || (*approved)[0] != "req-approve" {
		t.Errorf("approveFunc called with %v, want [req-approve]", *approved)
	}
}

func TestModel_DenyKey(t *testing.T) {
	m, _, denied := newTestModel(t, LockModeNone)

	req := makeRequest("req-deny", "/secrets/test")
	m = updateModel(m, NewRequestMsg{Request: req})

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if cmd == nil {
		t.Fatal("pressing 'n' should return a command; got nil")
	}

	result := cmd()
	denyMsg, ok := result.(ApproveResultMsg)
	if !ok {
		t.Fatalf("deny command returned %T, want ApproveResultMsg", result)
	}
	if denyMsg.ID != "req-deny" {
		t.Errorf("ApproveResultMsg.ID = %q, want %q", denyMsg.ID, "req-deny")
	}
	if len(*denied) != 1 || (*denied)[0] != "req-deny" {
		t.Errorf("denyFunc called with %v, want [req-deny]", *denied)
	}
}

func TestModel_CursorNavigation(t *testing.T) {
	m, _, _ := newTestModel(t, LockModeNone)

	req1 := makeRequest("req-1", "/secrets/first")
	req2 := makeRequest("req-2", "/secrets/second")
	m = updateModel(m, NewRequestMsg{Request: req1})
	m = updateModel(m, NewRequestMsg{Request: req2})

	// Cursor starts at 0 (first item: req-1). Detail shows first secret.
	view := m.View()
	if !containsSubstr(view, "/secrets/first") {
		t.Errorf("after add: detail pane should show first request; view:\n%s", view)
	}

	// Move cursor down — detail should update to second item.
	m = updateModel(m, tea.KeyMsg{Type: tea.KeyDown})
	view = m.View()
	if !containsSubstr(view, "/secrets/second") {
		t.Errorf("after down: detail pane should show second request; view:\n%s", view)
	}

	// Move cursor up — back to first.
	m = updateModel(m, tea.KeyMsg{Type: tea.KeyUp})
	view = m.View()
	if !containsSubstr(view, "/secrets/first") {
		t.Errorf("after up: detail pane should show first request; view:\n%s", view)
	}
}

func TestModel_ResolvedMovesToHistory(t *testing.T) {
	m, _, _ := newTestModel(t, LockModeNone)

	req := makeRequest("req-history", "/secrets/history")
	m = updateModel(m, NewRequestMsg{Request: req})

	// Resolve the request.
	m = updateModel(m, RequestResolvedMsg{ID: "req-history", Resolution: approval.ResolutionApproved})

	view := m.View()
	// The list should show the history section with "Recent".
	if !containsSubstr(view, "Recent") {
		t.Errorf("after resolve: list pane should show Recent section; view:\n%s", view)
	}
	// The outcome should appear.
	if !containsSubstr(view, "approved") {
		t.Errorf("after resolve: list should show 'approved' outcome; view:\n%s", view)
	}
}

func TestModel_TickContinues(t *testing.T) {
	m, _, _ := newTestModel(t, LockModeNone)

	_, cmd := m.Update(TickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("TickMsg should return a continuation tick command; got nil")
	}
	// Execute cmd — it should produce another TickMsg (eventually, via tea.Tick).
	// We can only verify a non-nil command was returned since tea.Tick uses real time.
}

func TestModel_LockModeNone_YWorksWithoutLock(t *testing.T) {
	m, approved, _ := newTestModel(t, LockModeNone)

	req := makeRequest("req-nolock", "/secrets/nolock")
	m = updateModel(m, NewRequestMsg{Request: req})

	// vtLocked is false; y should still work with LockModeNone.
	if m.vtLocked {
		t.Fatal("vtLocked should be false initially")
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("y should produce a command in LockModeNone even without VT lock")
	}
	cmd() // trigger the approve call
	if len(*approved) != 1 {
		t.Errorf("approveFunc should be called once; got %v", *approved)
	}
}

func TestModel_LockModeManual_YBlockedWithoutLock(t *testing.T) {
	m, approved, _ := newTestModel(t, LockModeManual)

	req := makeRequest("req-manual", "/secrets/manual")
	m = updateModel(m, NewRequestMsg{Request: req})

	// vtLocked is false; y should NOT trigger approval in LockModeManual.
	if m.vtLocked {
		t.Fatal("vtLocked should be false initially")
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd != nil {
		// Execute cmd to see if it triggers approve anyway.
		cmd()
	}
	if len(*approved) != 0 {
		t.Errorf("approveFunc should NOT be called when vtLocked=false in LockModeManual; got %v", *approved)
	}
}

func TestModel_GPGSignRequestDetails(t *testing.T) {
	m, _, _ := newTestModel(t, LockModeNone)

	req := makeGPGRequest("req-gpg", "my-repo", "fix: auth flow\n\nMore detail here.")
	m = updateModel(m, NewRequestMsg{Request: req})

	view := m.View()
	// Detail pane should show GPG-specific fields.
	if !containsSubstr(view, "SIGN") {
		t.Errorf("View() does not contain [SIGN] badge; view:\n%s", view)
	}
	if !containsSubstr(view, "my-repo") {
		t.Errorf("View() does not contain repo name 'my-repo'; view:\n%s", view)
	}
	if !containsSubstr(view, "DEADBEEF") {
		t.Errorf("View() does not contain key ID 'DEADBEEF'; view:\n%s", view)
	}
}

func TestModel_ProcChainRendered(t *testing.T) {
	m, _, _ := newTestModel(t, LockModeNone)

	chain := []procchain.ProcInfo{
		{PID: 1234, PPid: 1200, Name: "git", CWD: "/home/user/repo"},
		{PID: 1200, PPid: 900, Name: "bash", CWD: "/home/user"},
	}
	req := makeRequest("req-chain", "/secrets/test")
	m = updateModel(m, NewRequestMsg{Request: req, ProcChain: chain})

	view := m.View()
	// Detail pane should show process chain.
	if !containsSubstr(view, "git") {
		t.Errorf("View() does not contain process name 'git'; view:\n%s", view)
	}
	if !containsSubstr(view, "bash") {
		t.Errorf("View() does not contain process name 'bash'; view:\n%s", view)
	}
}

func TestModel_NoRequestShowsIdle(t *testing.T) {
	m, _, _ := newTestModel(t, LockModeNone)
	// No requests added — detail pane should show idle state.
	view := m.View()
	if !containsSubstr(view, "secrets-dispatcher") {
		t.Errorf("idle detail pane should show 'secrets-dispatcher'; view:\n%s", view)
	}
}

// containsSubstr is a helper to check for a substring in rendered output,
// stripping ANSI escape sequences for comparison.
func containsSubstr(s, substr string) bool {
	return stripANSI(s) != "" && containsPlain(stripANSI(s), substr)
}

func containsPlain(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && searchString(s, substr))
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// stripANSI removes ANSI escape sequences from s for plain-text assertion.
func stripANSI(s string) string {
	var result []byte
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			// Skip until 'm' (CSI sequence terminator).
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			i = j + 1
			continue
		}
		result = append(result, s[i])
		i++
	}
	return string(result)
}
