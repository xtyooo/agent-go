package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"agentG/internal/runtime/memory"
	"github.com/go-chi/chi/v5"
)

type SessionHandler struct {
	logger *slog.Logger
	store  memory.Store
}

type sessionSummaryResponse struct {
	SessionID     string `json:"sessionId"`
	SessionName   string `json:"sessionName"`
	Title         string `json:"title"`
	AgentType     string `json:"agentType"`
	FirstQuestion string `json:"firstQuestion"`
	LastQuestion  string `json:"lastQuestion"`
	LastAnswer    string `json:"lastAnswer"`
	MessageCount  int64  `json:"messageCount"`
	CreateTime    string `json:"createTime"`
	UpdateTime    string `json:"updateTime"`
}

type sessionRecordResponse struct {
	ID                int64  `json:"id"`
	SessionID         string `json:"sessionId"`
	SessionName       string `json:"sessionName"`
	AgentType         string `json:"agentType"`
	Question          string `json:"question"`
	Answer            string `json:"answer"`
	Thinking          string `json:"thinking"`
	Tools             string `json:"tools"`
	Reference         string `json:"reference"`
	Recommend         string `json:"recommend"`
	FirstResponseTime int64  `json:"firstResponseTime"`
	TotalResponseTime int64  `json:"totalResponseTime"`
	FileID            string `json:"fileId"`
	CreateTime        string `json:"createTime"`
	UpdateTime        string `json:"updateTime"`
}

func NewSessionHandler(logger *slog.Logger, store memory.Store) *SessionHandler {
	if logger == nil {
		logger = slog.Default()
	}
	if store == nil {
		store = memory.NoopStore{}
	}
	return &SessionHandler{logger: logger, store: store}
}

func (h *SessionHandler) ListSessions(w http.ResponseWriter, r *http.Request) {
	limit := parseIntQuery(r, "limit")
	if limit <= 0 {
		limit = parseIntQuery(r, "pageSize")
	}
	if limit <= 0 {
		limit = 20
	}
	page := parseIntQuery(r, "page")
	offset := parseIntQuery(r, "offset")
	if page > 1 && offset == 0 {
		offset = (page - 1) * limit
	}

	sessions, total, err := h.store.ListSessions(r.Context(), memory.ListSessionsRequest{
		AgentType: r.URL.Query().Get("agentType"),
		Keyword:   r.URL.Query().Get("keyword"),
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		h.logger.Error("query session list failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "query session list failed",
		})
		return
	}

	items := make([]sessionSummaryResponse, 0, len(sessions))
	for _, session := range sessions {
		items = append(items, toSessionSummaryResponse(session))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"total":   total,
		"items":   items,
	})
}

func (h *SessionHandler) GetSession(w http.ResponseWriter, r *http.Request) {
	sessionID := sessionIDFromRequest(r)
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "sessionId is required",
		})
		return
	}

	records, err := h.store.FindBySession(r.Context(), sessionID)
	if err != nil {
		h.logger.Error("query session detail failed", "session_id", sessionID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "query session detail failed",
		})
		return
	}
	if len(records) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "session not found",
		})
		return
	}

	items := make([]sessionRecordResponse, 0, len(records))
	for _, record := range records {
		items = append(items, toSessionRecordResponse(record))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"sessionId": sessionID,
		"items":     items,
	})
}

func (h *SessionHandler) DeleteSession(w http.ResponseWriter, r *http.Request) {
	sessionID := sessionIDFromRequest(r)
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "sessionId is required",
		})
		return
	}

	deleted, err := h.store.DeleteSession(r.Context(), sessionID)
	if err != nil {
		h.logger.Error("delete session failed", "session_id", sessionID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "delete session failed",
		})
		return
	}
	if deleted == 0 {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "session not found",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"sessionId": sessionID,
		"deleted":   deleted,
	})
}

func (h *SessionHandler) RenameSession(w http.ResponseWriter, r *http.Request) {
	sessionID := sessionIDFromRequest(r)
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "sessionId is required",
		})
		return
	}

	var body struct {
		SessionName string `json:"sessionName"`
		Name        string `json:"name"`
		Title       string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "invalid json body",
		})
		return
	}
	name := strings.TrimSpace(body.SessionName)
	if name == "" {
		name = strings.TrimSpace(body.Name)
	}
	if name == "" {
		name = strings.TrimSpace(body.Title)
	}
	if len([]rune(name)) > 120 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "sessionName is too long",
		})
		return
	}

	updated, err := h.store.RenameSession(r.Context(), sessionID, name)
	if err != nil {
		h.logger.Error("rename session failed", "session_id", sessionID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "rename session failed",
		})
		return
	}
	if updated == 0 {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "session not found",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"sessionId":   sessionID,
		"sessionName": name,
	})
}

func sessionIDFromRequest(r *http.Request) string {
	if value := strings.TrimSpace(chi.URLParam(r, "sessionId")); value != "" {
		return value
	}
	return strings.TrimSpace(r.URL.Query().Get("sessionId"))
}

func toSessionSummaryResponse(session memory.SessionSummary) sessionSummaryResponse {
	title := strings.TrimSpace(session.SessionName)
	if title == "" {
		title = strings.TrimSpace(session.FirstQuestion)
	}
	if title == "" {
		title = session.SessionID
	}
	return sessionSummaryResponse{
		SessionID:     session.SessionID,
		SessionName:   session.SessionName,
		Title:         title,
		AgentType:     session.AgentType,
		FirstQuestion: session.FirstQuestion,
		LastQuestion:  session.LastQuestion,
		LastAnswer:    session.LastAnswer,
		MessageCount:  session.MessageCount,
		CreateTime:    formatAPITime(session.CreateTime),
		UpdateTime:    formatAPITime(session.UpdateTime),
	}
}

func toSessionRecordResponse(record memory.SessionRecord) sessionRecordResponse {
	return sessionRecordResponse{
		ID:                record.ID,
		SessionID:         record.SessionID,
		SessionName:       record.SessionName,
		AgentType:         record.AgentType,
		Question:          record.Question,
		Answer:            record.Answer,
		Thinking:          record.Thinking,
		Tools:             record.Tools,
		Reference:         record.Reference,
		Recommend:         record.Recommend,
		FirstResponseTime: record.FirstResponseTime,
		TotalResponseTime: record.TotalResponseTime,
		FileID:            record.FileID,
		CreateTime:        formatAPITime(record.CreateTime),
		UpdateTime:        formatAPITime(record.UpdateTime),
	}
}

func formatAPITime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339)
}
