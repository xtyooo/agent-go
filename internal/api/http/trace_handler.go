package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/learn-demo/agent-go/internal/runtime/event"
	"github.com/learn-demo/agent-go/internal/runtime/trace"
)

// TraceHandler 提供 trace 查询和 SSE 回放接口。
// 回放只读取已经落盘的事件，不会重新调用模型或工具，适合复现一次失败或演示 Agent 输出过程。
type TraceHandler struct {
	logger *slog.Logger
	store  trace.Store
}

func NewTraceHandler(logger *slog.Logger, store trace.Store) *TraceHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &TraceHandler{logger: logger, store: store}
}

func (h *TraceHandler) GetTrace(w http.ResponseWriter, r *http.Request) {
	traceID := h.traceID(r)
	if traceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "traceId is required",
		})
		return
	}
	run, err := h.load(r, traceID)
	if err != nil {
		h.logger.Warn("⚠ Trace 查询失败", "trace_id", traceID, "error", err)
		writeJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "trace not found",
			"detail":  err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(run); err != nil {
		h.logger.Warn("⚠ Trace JSON 写出失败", "trace_id", traceID, "error", err)
	}
}

func (h *TraceHandler) ReplayStream(w http.ResponseWriter, r *http.Request) {
	startedAt := time.Now()
	traceID := h.traceID(r)
	requestID := newRequestID()
	logger := h.logger.With("request_id", requestID, "trace_id", traceID)
	if traceID == "" {
		WriteSSEEvent(w, event.Error("BAD_REQUEST", "traceId is required", "missing traceId parameter"))
		return
	}

	run, err := h.load(r, traceID)
	if err != nil {
		logger.Warn("⚠ Trace 回放失败：记录不存在", "error", err)
		WriteSSEEvent(w, event.Error("TRACE_NOT_FOUND", "trace not found", err.Error()))
		return
	}

	events := make(chan event.Event)
	go replayEvents(r, run.Events, events, replayDelay(r))

	logger.Info("🎬 Trace 回放流已开始",
		"conversation_id", run.ConversationID,
		"agent_type", run.AgentType,
		"event_count", len(run.Events),
	)
	summary := StreamEvents(w, r, events, logger, run.ConversationID, requestID)
	logger.Info("✅ Trace 回放流已结束",
		"conversation_id", run.ConversationID,
		"agent_type", run.AgentType,
		"event_count", summary.EventCount,
		"elapsed_ms", elapsedMillis(startedAt),
	)
}

func (h *TraceHandler) traceID(r *http.Request) string {
	if fromPath := strings.TrimSpace(chi.URLParam(r, "traceId")); fromPath != "" {
		return fromPath
	}
	return strings.TrimSpace(r.URL.Query().Get("traceId"))
}

func (h *TraceHandler) load(r *http.Request, traceID string) (trace.Run, error) {
	if h.store == nil {
		return trace.Run{}, errTraceDisabled{}
	}
	return h.store.Load(r.Context(), traceID)
}

func replayDelay(r *http.Request) time.Duration {
	if strings.ToLower(strings.TrimSpace(r.URL.Query().Get("timing"))) != "original" {
		return 0
	}
	raw := strings.TrimSpace(r.URL.Query().Get("maxDelayMs"))
	if raw == "" {
		return 500 * time.Millisecond
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 500 * time.Millisecond
	}
	return time.Duration(value) * time.Millisecond
}

func replayEvents(r *http.Request, records []trace.EventRecord, out chan<- event.Event, maxDelay time.Duration) {
	defer close(out)

	var previousOffset int64
	for _, record := range records {
		if maxDelay > 0 {
			delay := time.Duration(record.OffsetMs-previousOffset) * time.Millisecond
			if delay > maxDelay {
				delay = maxDelay
			}
			if delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-r.Context().Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
			previousOffset = record.OffsetMs
		}

		select {
		case <-r.Context().Done():
			return
		case out <- record.Event:
		}
	}
}

type errTraceDisabled struct{}

func (errTraceDisabled) Error() string {
	return "trace store is disabled"
}
