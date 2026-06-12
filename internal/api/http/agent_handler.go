package http

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/learn-demo/agent-go/internal/runtime/agent"
	"github.com/learn-demo/agent-go/internal/runtime/event"
)

type AgentHandler struct {
	// logger 是 HTTP 入口日志，ChatStream 会基于它派生 request_id 维度的 logger。
	logger *slog.Logger
	// agent 是实际处理请求的流式 Agent，目前由 websearch.ReactAgent 实现。
	agent agent.Agent
}

func NewAgentHandler(logger *slog.Logger, chatAgent agent.Agent) *AgentHandler {
	return &AgentHandler{
		logger: logger,
		agent:  chatAgent,
	}
}

func (h *AgentHandler) ChatStream(w http.ResponseWriter, r *http.Request) {
	startedAt := time.Now()
	requestID := newRequestID()
	logger := h.logger.With("request_id", requestID)
	query := r.URL.Query().Get("query")
	conversationID := r.URL.Query().Get("conversationId")
	temperature, hasTemperature := parseFloatQuery(r, "temperature")
	maxRounds := parseIntQuery(r, "maxTurns")

	if query == "" {
		logger.Warn("\U000026A0 聊天流请求被拒绝：缺少 query 参数", "reason", "missing_query", "remote_addr", r.RemoteAddr)
		WriteSSEEvent(w, event.Error("BAD_REQUEST", "query is required", "missing query parameter"))
		return
	}
	if conversationID == "" {
		logger.Warn("\U000026A0 聊天流请求被拒绝：缺少 conversationId 参数", "reason", "missing_conversation_id", "query_chars", len(query), "remote_addr", r.RemoteAddr)
		WriteSSEEvent(w, event.Error("BAD_REQUEST", "conversationId is required", "missing conversationId parameter"))
		return
	}

	logger.Info("\U0001F680 聊天流请求已接收",
		"conversation_id", conversationID,
		"query", query,
		"query_chars", len(query),
		"remote_addr", r.RemoteAddr,
	)

	events, err := h.agent.Run(r.Context(), agent.Input{
		Query:          query,
		ConversationID: conversationID,
		RequestID:      requestID,
		Temperature:    temperaturePtr(temperature, hasTemperature),
		MaxRounds:      maxRounds,
	})
	if err != nil {
		logger.Error("\U0000274C Agent 流启动失败",
			"conversation_id", conversationID,
			"elapsed_ms", elapsedMillis(startedAt),
			"error", err,
		)
		WriteSSEEvent(w, event.Error("AGENT_START_FAILED", "agent failed to start", err.Error()))
		return
	}

	streamSummary := StreamEvents(w, r, events, logger, conversationID, requestID)

	logger.Info("\U0001F3C1 聊天流请求已结束",
		"conversation_id", conversationID,
		"event_count", streamSummary.EventCount,
		"event_text_count", streamSummary.TypeCounts[event.TypeText],
		"event_thinking_count", streamSummary.TypeCounts[event.TypeThinking],
		"event_tool_start_count", streamSummary.TypeCounts[event.TypeToolStart],
		"event_tool_end_count", streamSummary.TypeCounts[event.TypeToolEnd],
		"event_reference_count", streamSummary.TypeCounts[event.TypeReference],
		"event_error_count", streamSummary.TypeCounts[event.TypeError],
		"event_complete_count", streamSummary.TypeCounts[event.TypeComplete],
		"first_event_ms", streamSummary.FirstEventMs,
		"elapsed_ms", elapsedMillis(startedAt),
	)
}

func parseFloatQuery(r *http.Request, name string) (float64, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, false
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func parseIntQuery(r *http.Request, name string) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return value
}

func temperaturePtr(value float64, ok bool) *float64 {
	if !ok {
		return nil
	}
	return &value
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return time.Now().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(b[:])
}
