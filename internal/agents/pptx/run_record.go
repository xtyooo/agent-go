package pptx

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"time"

	"agentG/internal/runtime/agent"
	"agentG/internal/runtime/event"
	"agentG/internal/runtime/memory"
)

type runRecord struct {
	conversationID  string
	question        string
	startedAt       time.Time
	sessionRecordID int64
	answer          strings.Builder
	thinking        strings.Builder
	tools           map[string]struct{}
	firstResponseMs int64
	totalResponseMs int64
}

func newRunRecord(conversationID string, question string, startedAt time.Time) *runRecord {
	return &runRecord{
		conversationID: conversationID,
		question:       question,
		startedAt:      startedAt,
		tools:          make(map[string]struct{}),
	}
}

func (r *runRecord) capture(evt event.Event, elapsedMs int64) {
	if r == nil {
		return
	}
	if r.firstResponseMs == 0 {
		r.firstResponseMs = elapsedMs
	}
	switch evt.Type {
	case event.TypeText:
		r.answer.WriteString(evt.Content)
	case event.TypeThinking:
		r.thinking.WriteString(evt.Content)
	case event.TypeToolStart:
		r.addTool(evt.ToolName)
	}
}

func (r *runRecord) addTool(name string) {
	if r == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name != "" {
		r.tools[name] = struct{}{}
	}
}

func (r *runRecord) toolsString() string {
	if r == nil || len(r.tools) == 0 {
		return ""
	}
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

func (a *Agent) saveRunQuestion(ctx context.Context, logger *slog.Logger, input agent.Input, record *runRecord) {
	if a.memory == nil || strings.TrimSpace(input.ConversationID) == "" || record == nil {
		return
	}
	startedAt := time.Now()
	saved, err := a.memory.SaveQuestion(ctx, memory.SaveQuestionRequest{
		SessionID: input.ConversationID,
		AgentType: "pptx",
		Question:  input.Query,
	})
	if err != nil {
		logger.Warn("⚠ PPTBuilder 会话问题保存失败",
			"conversation_id", input.ConversationID,
			"elapsed_ms", elapsedMillis(startedAt),
			"error", err,
		)
		return
	}
	record.sessionRecordID = saved.ID
	logger.Info("💾 PPTBuilder 会话问题已保存",
		"conversation_id", input.ConversationID,
		"session_record_id", saved.ID,
		"elapsed_ms", elapsedMillis(startedAt),
	)
}

func (a *Agent) persistRun(ctx context.Context, logger *slog.Logger, input agent.Input, record *runRecord) {
	if a.memory == nil || record == nil || record.sessionRecordID <= 0 {
		return
	}
	startedAt := time.Now()
	if err := a.memory.UpdateAnswer(ctx, memory.UpdateAnswerRequest{
		ID:                record.sessionRecordID,
		Answer:            record.answer.String(),
		Thinking:          record.thinking.String(),
		Tools:             record.toolsString(),
		FirstResponseTime: record.firstResponseMs,
		TotalResponseTime: elapsedMillis(record.startedAt),
	}); err != nil {
		logger.Warn("⚠ PPTBuilder 会话结果保存失败",
			"conversation_id", input.ConversationID,
			"session_record_id", record.sessionRecordID,
			"elapsed_ms", elapsedMillis(startedAt),
			"error", err,
		)
		return
	}
	logger.Info("💾 PPTBuilder 会话结果已保存",
		"conversation_id", input.ConversationID,
		"session_record_id", record.sessionRecordID,
		"answer_chars", record.answer.Len(),
		"thinking_chars", record.thinking.Len(),
		"tools", record.toolsString(),
		"elapsed_ms", elapsedMillis(startedAt),
	)
}
