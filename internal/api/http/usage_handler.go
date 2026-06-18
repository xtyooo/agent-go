package http

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agentG/internal/runtime/usage"
)

type UsageHandler struct {
	logger *slog.Logger
	store  usage.Store
}

func NewUsageHandler(logger *slog.Logger, store usage.Store) *UsageHandler {
	if logger == nil {
		logger = slog.Default()
	}
	if store == nil {
		store = usage.NoopStore{}
	}
	return &UsageHandler{logger: logger, store: store}
}

func (h *UsageHandler) Overview(w http.ResponseWriter, r *http.Request) {
	query := usage.Query{
		From:      time.Now().Add(-parseRange(r.URL.Query().Get("range"))),
		To:        time.Now(),
		AgentType: strings.TrimSpace(r.URL.Query().Get("agentType")),
		Model:     strings.TrimSpace(r.URL.Query().Get("model")),
		Interval:  parseInterval(r.URL.Query().Get("interval")),
		Limit:     parseLimit(r.URL.Query().Get("limit"), 100),
	}

	overview, err := h.store.Overview(r.Context(), query)
	if err != nil {
		h.logger.Warn("model usage overview query failed",
			"agent_type", query.AgentType,
			"model", query.Model,
			"error", err,
		)
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "query model usage failed",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    overview,
	})
}

func parseRange(value string) time.Duration {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1h":
		return time.Hour
	case "6h":
		return 6 * time.Hour
	case "7d":
		return 7 * 24 * time.Hour
	case "30d":
		return 30 * 24 * time.Hour
	default:
		return 24 * time.Hour
	}
}

func parseInterval(value string) time.Duration {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "5m":
		return 5 * time.Minute
	case "15m":
		return 15 * time.Minute
	case "30m":
		return 30 * time.Minute
	case "1h":
		return time.Hour
	case "6h":
		return 6 * time.Hour
	case "12h":
		return 12 * time.Hour
	case "1d":
		return 24 * time.Hour
	default:
		return 0
	}
}

func parseLimit(value string, fallback int) int {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit <= 0 {
		return fallback
	}
	if limit > 500 {
		return 500
	}
	return limit
}
