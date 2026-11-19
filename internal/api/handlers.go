package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/proxy"
)

// ClientProvider is an interface for getting connected client information.
type ClientProvider interface {
	Clients() []proxy.ClientInfo
}

// Handlers provides HTTP handlers for the REST API.
type Handlers struct {
	resolver         *Resolver
	clientProvider   ClientProvider
	trimProcessChain bool
	auth             *Auth
	testMode         bool
}

func NewHandlers(cfg ServerConfig, provider ClientProvider) *Handlers {
	return &Handlers{
		resolver:         cfg.Resolver,
		clientProvider:   provider,
		auth:             cfg.Auth,
		trimProcessChain: cfg.TrimProcessChain,
		testMode:         cfg.TestMode,
	}
}

// HandleStatus handles GET /api/v1/status.
func (h *Handlers) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	writeJSON(w, StatusResponse{
		Running:      true,
		Clients:      h.clientProvider.Clients(),
		PendingCount: h.resolver.Manager.PendingCount(),
	})
}

// HandlePendingList handles GET /api/v1/pending.
func (h *Handlers) HandlePendingList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp := PendingListResponse{Requests: pendingRequests(h.resolver.Manager)}
	writeJSON(w, resp)
}

// HandleApprove handles POST /api/v1/pending/{id}/approve.
func (h *Handlers) HandleApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := extractRequestID(r.URL.Path, "/api/v1/pending/", "/approve")
	if id == "" {
		writeError(w, "invalid request path", http.StatusBadRequest)
		return
	}

	if err := h.resolver.Approve(id); err != nil {
		if err == approval.ErrNotFound {
			writeError(w, "request not found or expired", http.StatusNotFound)
			return
		}
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, ActionResponse{Status: "approved"})
}

// HandleApproveAndAutoApprove handles POST /api/v1/pending/{id}/approve-and-auto-approve.
func (h *Handlers) HandleApproveAndAutoApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := extractRequestID(r.URL.Path, "/api/v1/pending/", "/approve-and-auto-approve")
	if id == "" {
		writeError(w, "invalid request path", http.StatusBadRequest)
		return
	}

	if err := h.resolver.ApproveAndAutoApprove(id); err != nil {
		if err == approval.ErrNotFound {
			writeError(w, "request not found or expired", http.StatusNotFound)
			return
		}
		if err == approval.ErrNotAutoApprovable {
			writeError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, ActionResponse{Status: "approved"})
}

// HandleDeny handles POST /api/v1/pending/{id}/deny.
func (h *Handlers) HandleDeny(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := extractRequestID(r.URL.Path, "/api/v1/pending/", "/deny")
	if id == "" {
		writeError(w, "invalid request path", http.StatusBadRequest)
		return
	}

	if err := h.resolver.Deny(id); err != nil {
		if err == approval.ErrNotFound {
			writeError(w, "request not found or expired", http.StatusNotFound)
			return
		}
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, ActionResponse{Status: "denied"})
}

// HandleCancel handles POST /api/v1/pending/{id}/cancel.
func (h *Handlers) HandleCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := extractRequestID(r.URL.Path, "/api/v1/pending/", "/cancel")
	if id == "" {
		writeError(w, "invalid request path", http.StatusBadRequest)
		return
	}

	if err := h.resolver.Manager.Cancel(id); err != nil {
		if err == approval.ErrNotFound {
			writeError(w, "request not found or expired", http.StatusNotFound)
			return
		}
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, ActionResponse{Status: "cancelled"})
}

// HandleAutoApproveList handles GET /api/v1/auto-approve.
func (h *Handlers) HandleAutoApproveList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rules := h.resolver.Manager.ListAutoApproveRules()
	if rules == nil {
		rules = []approval.AutoApproveRule{}
	}
	writeJSON(w, rules)
}

// HandleAutoApproveCreate handles POST /api/v1/auto-approve.
func (h *Handlers) HandleAutoApproveCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RequestID string `json:"request_id"`
	}
	if !decodePost(w, r, &req) {
		return
	}

	if err := h.resolver.AutoApprove(req.RequestID); err != nil {
		if err == approval.ErrNotFound {
			writeError(w, "cancelled request not found", http.StatusNotFound)
			return
		}
		if err == approval.ErrNotAutoApprovable {
			writeError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, ActionResponse{Status: "created"})
}

// HandleAutoApproveDelete handles DELETE /api/v1/auto-approve/{id}.
func (h *Handlers) HandleAutoApproveDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/v1/auto-approve/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, "invalid rule ID", http.StatusBadRequest)
		return
	}

	if err := h.resolver.Manager.RemoveAutoApproveRule(id); err != nil {
		if err == approval.ErrNotFound {
			writeError(w, "rule not found", http.StatusNotFound)
			return
		}
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, ActionResponse{Status: "deleted"})
}

// HandleLog handles GET /api/v1/log.
func (h *Handlers) HandleLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp := HistoryResponse{Entries: historyEntries(h.resolver.Manager)}
	writeJSON(w, resp)
}

// convertSenderInfo converts approval.SenderInfo to api.SenderInfo.
func convertSenderInfo(s approval.SenderInfo) SenderInfo {
	info := SenderInfo{
		Sender:      s.Sender,
		PID:         s.PID,
		UID:         s.UID,
		UserName:    s.UserName,
		InvokerName: s.InvokerName,
		SystemdUnit: s.SystemdUnit,
	}
	if len(s.ProcessChain) > 0 {
		info.ProcessChain = make([]ProcessInfo, len(s.ProcessChain))
		for i, p := range s.ProcessChain {
			info.ProcessChain[i] = ProcessInfo{
				Name: p.Name,
				PID:  p.PID,
				Exe:  p.Exe,
				Args: p.Args,
				CWD:  p.CWD,
			}
		}
	}
	return info
}

// convertHistoryEntry converts an approval.HistoryEntry to an API HistoryEntry.
func convertHistoryEntry(entry approval.HistoryEntry) HistoryEntry {
	return HistoryEntry{
		Request:    convertRequest(entry.Request),
		Resolution: string(entry.Resolution),
		ResolvedAt: entry.ResolvedAt,
	}
}

// extractRequestID extracts the request ID from a path like /api/v1/pending/{id}/action.
func extractRequestID(path, prefix, suffix string) string {
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return ""
	}
	id := path[len(prefix) : len(path)-len(suffix)]
	// Basic validation - should be non-empty and not contain slashes
	if id == "" || strings.Contains(id, "/") {
		return ""
	}
	return id
}

func writeJSON(w http.ResponseWriter, v any) {
	if err := respondJSON(w, http.StatusOK, v); err != nil {
		writeError(w, "failed to encode response", http.StatusInternalServerError)
	}
}

func writeError(w http.ResponseWriter, message string, code int) {
	respondJSON(w, code, ErrorResponse{Error: message})
}

// Writes v as a JSON response with the given status code. Errors when v cannot
// be encoded, and then writes nothing.
func respondJSON(w http.ResponseWriter, code int, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(append(body, '\n'))
	return nil
}

// Decodes a POSTed JSON body into body, or writes the error response and returns false.
func decodePost(w http.ResponseWriter, r *http.Request, body any) bool {
	if r.Method != http.MethodPost {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	if err := json.NewDecoder(r.Body).Decode(body); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return false
	}
	return true
}

// HandleAuth handles POST /api/v1/auth for JWT to session cookie exchange.
func (h *Handlers) HandleAuth(w http.ResponseWriter, r *http.Request) {
	var req AuthRequest
	if !decodePost(w, r, &req) {
		return
	}

	if req.Token == "" {
		writeError(w, "missing token", http.StatusBadRequest)
		return
	}

	// Validate the JWT
	claims, err := h.auth.ValidateJWT(req.Token)
	if err != nil {
		writeError(w, "invalid or expired token", http.StatusUnauthorized)
		return
	}

	if !h.auth.consumeJTI(jti(claims.Jti), time.Unix(claims.Exp, 0)) {
		writeError(w, "invalid or expired token", http.StatusUnauthorized)
		return
	}

	// Set the session cookie
	if err := h.auth.SetSessionCookie(w); err != nil {
		writeError(w, "failed to create session", http.StatusInternalServerError)
		return
	}

	writeJSON(w, ActionResponse{Status: "authenticated"})
}

// HandleTestInjectHistory handles POST /api/v1/test/history for injecting test history entries.
// Only available when test mode is enabled.
func (h *Handlers) HandleTestInjectHistory(w http.ResponseWriter, r *http.Request) {
	if !h.testMode {
		writeError(w, "not found", http.StatusNotFound)
		return
	}

	var entry HistoryEntry
	if !decodePost(w, r, &entry) {
		return
	}

	// Convert API HistoryEntry to approval.HistoryEntry
	items := make([]approval.ItemInfo, len(entry.Request.Items))
	for i, item := range entry.Request.Items {
		items[i] = approval.ItemInfo{
			Path:       item.Path,
			Label:      item.Label,
			Attributes: item.Attributes,
		}
	}

	approvalSender := approval.SenderInfo{
		Sender:      entry.Request.SenderInfo.Sender,
		PID:         entry.Request.SenderInfo.PID,
		UID:         entry.Request.SenderInfo.UID,
		UserName:    entry.Request.SenderInfo.UserName,
		InvokerName: entry.Request.SenderInfo.InvokerName,
	}
	if len(entry.Request.SenderInfo.ProcessChain) > 0 {
		approvalSender.ProcessChain = make([]approval.ProcessInfo, len(entry.Request.SenderInfo.ProcessChain))
		for i, p := range entry.Request.SenderInfo.ProcessChain {
			approvalSender.ProcessChain[i] = approval.ProcessInfo{
				Name: p.Name,
				PID:  p.PID,
				Exe:  p.Exe,
				Args: p.Args,
				CWD:  p.CWD,
			}
		}
	}

	approvalEntry := approval.HistoryEntry{
		Request: &approval.Request{
			ID:               entry.Request.ID,
			Client:           entry.Request.Client,
			Items:            items,
			Session:          entry.Request.Session,
			CreatedAt:        entry.Request.CreatedAt,
			ExpiresAt:        entry.Request.ExpiresAt,
			Type:             approval.RequestType(entry.Request.Type),
			SearchAttributes: entry.Request.SearchAttributes,
			SenderInfo:       approvalSender,
		},
		Resolution: approval.Resolution(entry.Resolution),
		ResolvedAt: entry.ResolvedAt,
	}

	h.resolver.Manager.AddHistoryEntry(approvalEntry)

	writeJSON(w, ActionResponse{Status: "created"})
}
