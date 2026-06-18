package http

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agentG/internal/runtime/agent"
	"agentG/internal/runtime/event"
	"agentG/internal/runtime/task"
	"agentG/internal/runtime/trace"
	"agentG/internal/runtime/usage"
)

type AgentHandler struct {
	// logger 是 HTTP 入口日志，ChatStream 会基于它派生 request_id 维度的 logger。
	logger *slog.Logger
	// agents 保存不同 agentType 对应的流式 Agent。
	agents map[string]agent.Agent
	// tasks 管理流式任务生命周期，用于同会话互斥和主动停止生成。
	tasks *task.Manager
	// traces 记录 Agent SSE 事件，支持失败排查和离线 replay。
	traces trace.Store
}

func NewAgentHandler(logger *slog.Logger, chatAgent agent.Agent, tasks *task.Manager) *AgentHandler {
	return NewAgentHandlerWithAgents(logger, map[string]agent.Agent{"websearch": chatAgent}, tasks)
}

func NewAgentHandlerWithAgents(logger *slog.Logger, agents map[string]agent.Agent, tasks *task.Manager) *AgentHandler {
	return NewAgentHandlerWithAgentsAndTrace(logger, agents, tasks, nil)
}

func NewAgentHandlerWithAgentsAndTrace(logger *slog.Logger, agents map[string]agent.Agent, tasks *task.Manager, traces trace.Store) *AgentHandler {
	if tasks == nil {
		tasks = task.NewManager(logger)
	}
	return &AgentHandler{
		logger: logger,
		agents: agents,
		tasks:  tasks,
		traces: traces,
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

func (h *AgentHandler) PptxStream(w http.ResponseWriter, r *http.Request) {
	h.streamAgent(w, r, "pptx")
}

func (h *AgentHandler) streamAgent(w http.ResponseWriter, r *http.Request, agentType string) {
	startedAt := time.Now()
	requestID := newRequestID()
	traceID := requestTraceID(r)
	logger := h.logger.With("request_id", requestID, "trace_id", traceID)
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

	recorder, err := h.startTrace(context.Background(), trace.RunMeta{
		TraceID:        traceID,
		RequestID:      requestID,
		ConversationID: conversationID,
		AgentType:      agentType,
		Query:          query,
		RemoteAddr:     r.RemoteAddr,
		StartedAt:      startedAt,
	})
	if err != nil {
		logger.Warn("⚠ Agent Trace 启动失败，将继续执行但无法回放",
			"conversation_id", conversationID,
			"agent_type", agentType,
			"error", err,
		)
		recorder = nil
	}

	taskInfo, err := h.tasks.RegisterWithOptions(context.Background(), conversationID, agentType, task.RegisterOptions{
		Query:     query,
		RequestID: requestID,
		TraceID:   traceID,
	})
	if err != nil {
		logger.Warn("\U000026A0 聊天流请求被拒绝：会话已有任务运行",
			"conversation_id", conversationID,
			"agent_type", agentType,
			"elapsed_ms", elapsedMillis(startedAt),
			"error", err,
		)
		errorEvent := event.Error("TASK_ALREADY_RUNNING", "该会话正在执行中，请稍后再试", err.Error())
		WriteSSEEvent(w, errorEvent)
		if recorder != nil {
			recorder.Record(errorEvent)
		}
		h.finishTrace(logger, recorder, "failed", err)
		return
	}
	records, unsubscribe, _, ok := h.tasks.Subscribe(conversationID, 1)
	if !ok {
		WriteSSEEvent(w, event.Error("TASK_NOT_FOUND", "task is not running", conversationID))
		h.finishTrace(logger, recorder, "failed", nil)
		h.tasks.Remove(taskInfo)
		return
	}
	defer unsubscribe()

	runCtx := usage.WithMetadata(taskInfo.Context(), usage.Metadata{
		RequestID:      requestID,
		TraceID:        traceID,
		ConversationID: conversationID,
		AgentType:      agentType,
	})
	events, err := currentAgent.Run(runCtx, agent.Input{
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
		errorEvent := event.Error("AGENT_START_FAILED", "agent failed to start", err.Error())
		WriteSSEEvent(w, errorEvent)
		if recorder != nil {
			recorder.Record(errorEvent)
		}
		h.finishTrace(logger, recorder, "failed", err)
		unsubscribe()
		h.tasks.Remove(taskInfo)
		return
	}
	taskRecords := h.tasks.ForwardEvents(taskInfo, events)
	go h.observeTaskRun(taskInfo, taskRecords, logger, recorder)

	streamSummary := StreamTaskRecords(w, r, records, logger, conversationID, requestID, StreamOptions{})

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

func (h *AgentHandler) observeTaskRun(taskInfo *task.Info, records <-chan task.EventRecord, logger *slog.Logger, recorder *trace.Recorder) {
	status := "completed"
	var finishErr error
	for record := range records {
		if recorder != nil {
			recorder.Record(record.Event)
		}
		if record.Event.Type == event.TypeError {
			status = "failed"
			finishErr = errors.New(firstNonEmpty(record.Event.Message, record.Event.Content, record.Event.Detail, string(record.Event.Code)))
		}
	}
	if taskInfo.Context().Err() != nil {
		if taskInfo.Snapshot().Stopped {
			status = "stopped"
			finishErr = nil
		} else if status != "failed" {
			status = "cancelled"
			finishErr = taskInfo.Context().Err()
		}
	}
	h.finishTrace(logger, recorder, status, finishErr)
	h.tasks.Remove(taskInfo)
}

func (h *AgentHandler) startTrace(ctx context.Context, meta trace.RunMeta) (*trace.Recorder, error) {
	if h.traces == nil {
		return nil, nil
	}
	return h.traces.Start(ctx, meta)
}

func (h *AgentHandler) finishTrace(logger *slog.Logger, recorder *trace.Recorder, status string, finishErr error) {
	if recorder == nil || !recorder.Enabled() {
		return
	}
	summary, err := recorder.Finish(status, finishErr)
	if err != nil {
		logger.Warn("⚠ Agent Trace 保存失败",
			"trace_id", recorder.TraceID(),
			"status", status,
			"error", err,
		)
		return
	}
	logger.Info("📊 Agent Trace 摘要",
		"trace_id", summary.TraceID,
		"status", summary.Status,
		"event_count", summary.EventCount,
		"first_event_ms", summary.FirstEventMs,
		"elapsed_ms", summary.ElapsedMs,
		"file", summary.FilePath,
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

func (h *AgentHandler) TaskStatus(w http.ResponseWriter, r *http.Request) {
	conversationID := r.URL.Query().Get("conversationId")
	if conversationID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "conversationId is required",
		})
		return
	}
	snapshot, ok := h.tasks.Snapshot(conversationID)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"success":        true,
			"running":        false,
			"conversationId": conversationID,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"running": snapshot.Running,
		"task":    snapshot,
	})
}

func (h *AgentHandler) TaskAttachStream(w http.ResponseWriter, r *http.Request) {
	requestID := newRequestID()
	conversationID := r.URL.Query().Get("conversationId")
	if conversationID == "" {
		WriteSSEEvent(w, event.Error("BAD_REQUEST", "conversationId is required", "missing conversationId parameter"))
		return
	}
	fromSeq := parseIntQuery(r, "from")
	if fromSeq < 1 {
		fromSeq = 1
	}
	records, unsubscribe, snapshot, ok := h.tasks.Subscribe(conversationID, fromSeq)
	if !ok {
		WriteSSEEvent(w, event.Error("TASK_NOT_FOUND", "task is not running", conversationID))
		return
	}
	defer unsubscribe()

	logger := h.logger.With("request_id", requestID, "conversation_id", conversationID, "trace_id", snapshot.TraceID)
	StreamTaskRecords(w, r, records, logger, conversationID, requestID, StreamOptions{})
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
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

func requestTraceID(r *http.Request) string {
	if r == nil {
		return trace.NewID()
	}
	if value := safeURLID(r.URL.Query().Get("traceId")); value != "" {
		return value
	}
	return trace.NewID()
}

func safeURLID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z':
			b.WriteRune(char)
		case char >= 'A' && char <= 'Z':
			b.WriteRune(char)
		case char >= '0' && char <= '9':
			b.WriteRune(char)
		case char == '-' || char == '_':
			b.WriteRune(char)
		}
	}
	return b.String()
}
