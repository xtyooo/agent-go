package http

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/learn-demo/agent-go/internal/runtime/agent"
	"github.com/learn-demo/agent-go/internal/runtime/event"
	"github.com/learn-demo/agent-go/internal/runtime/task"
)

type AgentHandler struct {
	// logger 是 HTTP 入口日志，ChatStream 会基于它派生 request_id 维度的 logger。
	logger *slog.Logger
	// agents 保存不同 agentType 对应的流式 Agent。
	agents map[string]agent.Agent
	// tasks 管理流式任务生命周期，用于同会话互斥和主动停止生成。
	tasks *task.Manager
}

func NewAgentHandler(logger *slog.Logger, chatAgent agent.Agent, tasks *task.Manager) *AgentHandler {
	return NewAgentHandlerWithAgents(logger, map[string]agent.Agent{"websearch": chatAgent}, tasks)
}

func NewAgentHandlerWithAgents(logger *slog.Logger, agents map[string]agent.Agent, tasks *task.Manager) *AgentHandler {
	if tasks == nil {
		tasks = task.NewManager(logger)
	}
	return &AgentHandler{
		logger: logger,
		agents: agents,
		tasks:  tasks,
	}
}

func (h *AgentHandler) ChatStream(w http.ResponseWriter, r *http.Request) {
	h.streamAgent(w, r, "websearch")
}

func (h *AgentHandler) DeepStream(w http.ResponseWriter, r *http.Request) {
	h.streamAgent(w, r, "plan-execute")
}

func (h *AgentHandler) SkillsStream(w http.ResponseWriter, r *http.Request) {
	h.streamAgent(w, r, "skills")
}

func (h *AgentHandler) streamAgent(w http.ResponseWriter, r *http.Request, agentType string) {
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
	currentAgent, ok := h.agents[agentType]
	if !ok || currentAgent == nil {
		logger.Error("\U0000274C Agent 未注册", "agent_type", agentType)
		WriteSSEEvent(w, event.Error("AGENT_NOT_FOUND", "agent is not registered", agentType))
		return
	}

	logger.Info("\U0001F680 聊天流请求已接收",
		"conversation_id", conversationID,
		"agent_type", agentType,
		"query", query,
		"query_chars", len(query),
		"remote_addr", r.RemoteAddr,
	)

	taskInfo, err := h.tasks.Register(r.Context(), conversationID, agentType)
	if err != nil {
		logger.Warn("\U000026A0 聊天流请求被拒绝：会话已有任务运行",
			"conversation_id", conversationID,
			"agent_type", agentType,
			"elapsed_ms", elapsedMillis(startedAt),
			"error", err,
		)
		WriteSSEEvent(w, event.Error("TASK_ALREADY_RUNNING", "该会话正在执行中，请稍后再试", err.Error()))
		return
	}
	defer h.tasks.Remove(taskInfo)

	events, err := currentAgent.Run(taskInfo.Context(), agent.Input{
		Query:          query,
		ConversationID: conversationID,
		RequestID:      requestID,
		Temperature:    temperaturePtr(temperature, hasTemperature),
		MaxRounds:      maxRounds,
	})
	if err != nil {
		logger.Error("\U0000274C Agent 流启动失败",
			"conversation_id", conversationID,
			"agent_type", agentType,
			"elapsed_ms", elapsedMillis(startedAt),
			"error", err,
		)
		WriteSSEEvent(w, event.Error("AGENT_START_FAILED", "agent failed to start", err.Error()))
		return
	}
	events = h.tasks.WrapEvents(taskInfo, events)

	streamSummary := StreamEvents(w, r, events, logger, conversationID, requestID)

	logger.Info("\U0001F3C1 聊天流请求已结束",
		"conversation_id", conversationID,
		"agent_type", agentType,
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

func (h *AgentHandler) StopAgent(w http.ResponseWriter, r *http.Request) {
	startedAt := time.Now()
	requestID := newRequestID()
	logger := h.logger.With("request_id", requestID)
	conversationID := r.URL.Query().Get("conversationId")
	if conversationID == "" {
		logger.Warn("\U000026A0 停止任务请求被拒绝：缺少 conversationId 参数", "remote_addr", r.RemoteAddr)
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "conversationId is required",
		})
		return
	}

	success := h.tasks.Stop(conversationID)
	status := http.StatusOK
	message := "任务停止信号已发送"
	if !success {
		status = http.StatusNotFound
		message = "未找到运行中的任务"
	}

	logger.Info("\U0001F6D1 停止任务请求已处理",
		"conversation_id", conversationID,
		"success", success,
		"elapsed_ms", elapsedMillis(startedAt),
	)
	writeJSON(w, status, map[string]any{
		"success":        success,
		"conversationId": conversationID,
		"message":        message,
	})
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

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		_, _ = w.Write([]byte(`{"success":false,"message":"encode response failed"}`))
	}
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return time.Now().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(b[:])
}
