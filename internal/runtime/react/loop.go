package react

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"agentG/internal/runtime/event"
	"agentG/internal/runtime/model"
	"agentG/internal/runtime/tool"
)

type Params struct {
	ConversationID string
	RequestID      string
	Temperature    float64
	MaxRounds      int
}

type Hooks struct {
	BeforeTool func(ctx context.Context, call model.ToolCall, emit func(event.Event) bool) bool
	AfterTool  func(ctx context.Context, call model.ToolCall, result tool.Result)
	BeforeDone func(emit func(event.Event) bool) bool
}

type Loop struct {
	model          model.Model
	tools          *tool.Registry
	maxRounds      int
	logger         *slog.Logger
	hooks          Hooks
	stageProviders []StageOutputProvider
}

type Option func(*Loop)

func New(model model.Model, tools *tool.Registry, logger *slog.Logger, opts ...Option) *Loop {
	if logger == nil {
		logger = slog.Default()
	}
	l := &Loop{
		model:     model,
		tools:     tools,
		maxRounds: 5,
		logger:    logger,
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

func WithMaxRounds(maxRounds int) Option {
	return func(l *Loop) {
		if maxRounds > 0 {
			l.maxRounds = maxRounds
		}
	}
}

func WithHooks(hooks Hooks) Option {
	return func(l *Loop) {
		l.hooks = hooks
	}
}

func WithStageProviders(providers ...StageOutputProvider) Option {
	return func(l *Loop) {
		l.stageProviders = append(l.stageProviders, providers...)
	}
}

func (l *Loop) Stream(ctx context.Context, params Params, messages []model.Message, emit func(event.Event) bool) bool {
	if emit == nil {
		emit = func(event.Event) bool { return true }
	}
	if params.MaxRounds <= 0 {
		params.MaxRounds = l.maxRounds
	}
	if params.Temperature < 0 {
		params.Temperature = 0
	}
	if params.Temperature > 2 {
		params.Temperature = 2
	}

	logger := l.logger
	if params.RequestID != "" {
		logger = logger.With("request_id", params.RequestID)
	}

	if !l.emitStageOutputs(ctx, emit, StageAfterStart, StageContext{
		Params:   params,
		Messages: messages,
	}) {
		return false
	}

	for round := 1; round <= params.MaxRounds; round++ {
		roundStartedAt := time.Now()
		toolDefs := l.modelTools()
		logger.Info("React round started",
			"conversation_id", params.ConversationID,
			"round", round,
			"message_count", len(messages),
			"tool_schema_count", len(toolDefs),
		)

		if !emit(event.Thinking(fmt.Sprintf("ReAct round %d started\n", round))) {
			return false
		}

		state, ok := l.streamRound(ctx, emit, params, round, messages, toolDefs)
		if !ok {
			logger.Warn("React round interrupted",
				"conversation_id", params.ConversationID,
				"round", round,
				"elapsed_ms", elapsedMillis(roundStartedAt),
				"error", ctx.Err(),
			)
			return false
		}

		if len(state.toolCalls) == 0 {
			logger.Info("React round produced final answer",
				"conversation_id", params.ConversationID,
				"round", round,
				"content_chars", state.textBuffer.Len(),
				"final_answer_preview", previewText(state.textBuffer.String(), 80),
				"elapsed_ms", elapsedMillis(roundStartedAt),
			)
			return l.finish(ctx, emit, params, messages)
		}

		messages = append(messages, model.Message{
			Role:      model.RoleAssistant,
			Content:   state.textBuffer.String(),
			ToolCalls: state.toolCalls,
		})

		if round >= params.MaxRounds {
			logger.Warn("React max rounds reached, forcing final answer",
				"conversation_id", params.ConversationID,
				"round", round,
				"max_rounds", params.MaxRounds,
			)
			if !emit(event.Thinking("Max reasoning rounds reached, forcing final answer\n")) {
				return false
			}
			messages = append(messages, model.Message{
				Role:    model.RoleUser,
				Content: "The maximum reasoning rounds have been reached. Based on the current context, produce the final answer directly. Do not call tools again.",
			})
			return l.forceFinalStream(ctx, emit, params, messages)
		}

		toolResponses, ok := l.executeToolCalls(ctx, emit, params, round, state.toolCalls)
		if !ok {
			logger.Warn("Tool execution interrupted",
				"conversation_id", params.ConversationID,
				"round", round,
				"error", ctx.Err(),
			)
			return false
		}
		messages = append(messages, toolResponses...)
	}

	return l.finish(ctx, emit, params, messages)
}

func (l *Loop) streamRound(ctx context.Context, emit func(event.Event) bool, params Params, round int, messages []model.Message, toolDefs []model.ToolDefinition) (roundState, bool) {
	startedAt := time.Now()
	logger := l.logger
	if params.RequestID != "" {
		logger = logger.With("request_id", params.RequestID)
	}
	logger.Info("Model stream round started",
		"conversation_id", params.ConversationID,
		"round", round,
		"message_count", len(messages),
		"tool_schema_count", len(toolDefs),
	)

	stream, err := l.model.Stream(ctx, model.Request{
		Temperature: params.Temperature,
		Messages:    messages,
		Tools:       toolDefs,
		RequestID:   params.RequestID,
	})
	if err != nil {
		logger.Error("Model stream failed to start",
			"conversation_id", params.ConversationID,
			"round", round,
			"elapsed_ms", elapsedMillis(startedAt),
			"error", err,
		)
		_ = emit(event.Error("LLM_CALL_FAILED", "model stream failed to start", err.Error()))
		return roundState{}, false
	}

	state := roundState{}
	chunkCount := 0
	textChunkCount := 0
	thinkingChars := 0
	toolDeltaCount := 0
	firstChunkMs := int64(-1)
	firstTextMs := int64(-1)
	firstToolDeltaMs := int64(-1)
	for chunk := range stream {
		if chunk.Err != nil {
			logger.Error("Model stream failed",
				"conversation_id", params.ConversationID,
				"round", round,
				"chunk_count", chunkCount,
				"content_chars", state.textBuffer.Len(),
				"tool_delta_count", toolDeltaCount,
				"elapsed_ms", elapsedMillis(startedAt),
				"error", chunk.Err,
			)
			_ = emit(event.Error("LLM_CALL_FAILED", "model stream failed", chunk.Err.Error()))
			return state, false
		}
		if chunk.Done {
			break
		}
		if firstChunkMs < 0 {
			firstChunkMs = elapsedMillis(startedAt)
		}
		chunkCount++
		if len(chunk.ToolCalls) > 0 {
			state.mode = roundModeToolCall
			toolDeltaCount += len(chunk.ToolCalls)
			if firstToolDeltaMs < 0 {
				firstToolDeltaMs = elapsedMillis(startedAt)
			}
			for _, incoming := range chunk.ToolCalls {
				state.mergeToolCall(incoming)
			}
			continue
		}
		if chunk.ReasoningContent != "" {
			thinkingChars += len(chunk.ReasoningContent)
			if !emit(event.Thinking(chunk.ReasoningContent)) {
				return state, false
			}
		}
		if chunk.Content == "" {
			continue
		}
		textChunkCount++
		if firstTextMs < 0 {
			firstTextMs = elapsedMillis(startedAt)
		}
		for _, segment := range ParseThinkSegments(chunk.Content, &state.inThink) {
			if segment.Thinking {
				thinkingChars += len(segment.Content)
				if !emit(event.Thinking(segment.Content)) {
					return state, false
				}
				continue
			}
			if !emit(event.Text(segment.Content)) {
				return state, false
			}
			state.textBuffer.WriteString(segment.Content)
		}
	}

	logger.Info("Model stream round completed",
		"conversation_id", params.ConversationID,
		"round", round,
		"chunk_count", chunkCount,
		"text_chunk_count", textChunkCount,
		"content_chars", state.textBuffer.Len(),
		"thinking_chars", thinkingChars,
		"tool_delta_count", toolDeltaCount,
		"tool_call_count", len(state.toolCalls),
		"mode", state.modeForLog(),
		"first_chunk_ms", firstChunkMs,
		"first_text_ms", firstTextMs,
		"first_tool_delta_ms", firstToolDeltaMs,
		"elapsed_ms", elapsedMillis(startedAt),
	)
	return state, true
}

func (l *Loop) executeToolCalls(ctx context.Context, emit func(event.Event) bool, params Params, round int, calls []model.ToolCall) ([]model.Message, bool) {
	batchStartedAt := time.Now()
	logger := l.logger
	if params.RequestID != "" {
		logger = logger.With("request_id", params.RequestID)
	}
	logger.Info("Tool batch started",
		"conversation_id", params.ConversationID,
		"round", round,
		"tool_call_count", len(calls),
	)

	responses := make([]model.Message, 0, len(calls))
	for _, call := range calls {
		callStartedAt := time.Now()
		toolName := call.Name
		argsJSON := call.Arguments
		if l.hooks.BeforeTool != nil && !l.hooks.BeforeTool(ctx, call, emit) {
			return nil, false
		}

		if !emit(event.ToolStart(toolName, call.ID, argsJSON)) {
			return nil, false
		}

		logger.Info("Tool call started",
			"conversation_id", params.ConversationID,
			"round", round,
			"tool", toolName,
			"tool_call_id", call.ID,
			"args_chars", len(argsJSON),
			"args_summary", ToolArgsSummary(argsJSON),
		)

		args := map[string]any{}
		if strings.TrimSpace(argsJSON) != "" {
			if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
				result := `{ "error": "invalid tool arguments: ` + EscapeForJSON(err.Error()) + `" }`
				logger.Error("Tool arguments invalid",
					"conversation_id", params.ConversationID,
					"round", round,
					"tool", toolName,
					"tool_call_id", call.ID,
					"args_chars", len(argsJSON),
					"elapsed_ms", elapsedMillis(callStartedAt),
					"error", err,
				)
				if !emit(event.ToolEnd(toolName, call.ID, result)) {
					return nil, false
				}
				responses = append(responses, ToolResponse(call, result))
				continue
			}
		}

		result, err := l.tools.Execute(ctx, toolName, args)
		if err != nil {
			resultText := `{ "error": "` + EscapeForJSON(err.Error()) + `" }`
			logger.Error("Tool call failed",
				"conversation_id", params.ConversationID,
				"round", round,
				"tool", toolName,
				"tool_call_id", call.ID,
				"elapsed_ms", elapsedMillis(callStartedAt),
				"error", err,
			)
			if !emit(event.ToolEnd(toolName, call.ID, resultText)) {
				return nil, false
			}
			responses = append(responses, ToolResponse(call, resultText))
			continue
		}

		if l.hooks.AfterTool != nil {
			l.hooks.AfterTool(ctx, call, result)
		}
		resultText := result.Content
		if result.Data != nil {
			resultText = tool.MustJSON(result.Data)
		}
		logger.Info("Tool call completed",
			"conversation_id", params.ConversationID,
			"round", round,
			"tool", toolName,
			"tool_call_id", call.ID,
			"result_chars", len(resultText),
			"elapsed_ms", elapsedMillis(callStartedAt),
		)
		if !emit(event.ToolEnd(toolName, call.ID, resultText)) {
			return nil, false
		}
		if !l.emitStageOutputs(ctx, emit, StageAfterToolEnd, StageContext{
			Params:     params,
			Round:      round,
			ToolCall:   &call,
			ToolResult: &result,
		}) {
			return nil, false
		}
		responses = append(responses, ToolResponse(call, resultText))
	}

	logger.Info("Tool batch completed",
		"conversation_id", params.ConversationID,
		"round", round,
		"tool_response_count", len(responses),
		"elapsed_ms", elapsedMillis(batchStartedAt),
	)
	return responses, true
}

func (l *Loop) forceFinalStream(ctx context.Context, emit func(event.Event) bool, params Params, messages []model.Message) bool {
	startedAt := time.Now()
	logger := l.logger
	if params.RequestID != "" {
		logger = logger.With("request_id", params.RequestID)
	}
	logger.Info("Force final stream started",
		"conversation_id", params.ConversationID,
		"message_count", len(messages),
	)

	stream, err := l.model.Stream(ctx, model.Request{
		Temperature: params.Temperature,
		Messages:    messages,
		RequestID:   params.RequestID,
	})
	if err != nil {
		logger.Error("Force final stream failed to start",
			"conversation_id", params.ConversationID,
			"elapsed_ms", elapsedMillis(startedAt),
			"error", err,
		)
		_ = emit(event.Error("LLM_CALL_FAILED", "force final stream failed to start", err.Error()))
		return false
	}

	state := roundState{}
	chunkCount := 0
	textChars := 0
	thinkingChars := 0
	firstChunkMs := int64(-1)
	firstTextMs := int64(-1)
	var finalText strings.Builder
	for chunk := range stream {
		if chunk.Err != nil {
			logger.Error("Force final stream failed",
				"conversation_id", params.ConversationID,
				"chunk_count", chunkCount,
				"text_chars", textChars,
				"thinking_chars", thinkingChars,
				"elapsed_ms", elapsedMillis(startedAt),
				"error", chunk.Err,
			)
			_ = emit(event.Error("LLM_CALL_FAILED", "force final stream failed", chunk.Err.Error()))
			return false
		}
		if chunk.Done {
			break
		}
		if firstChunkMs < 0 {
			firstChunkMs = elapsedMillis(startedAt)
		}
		chunkCount++
		if chunk.ReasoningContent != "" {
			thinkingChars += len(chunk.ReasoningContent)
			if !emit(event.Thinking(chunk.ReasoningContent)) {
				return false
			}
		}
		for _, segment := range ParseThinkSegments(chunk.Content, &state.inThink) {
			if segment.Thinking {
				thinkingChars += len(segment.Content)
				if !emit(event.Thinking(segment.Content)) {
					return false
				}
				continue
			}
			textChars += len(segment.Content)
			finalText.WriteString(segment.Content)
			if firstTextMs < 0 {
				firstTextMs = elapsedMillis(startedAt)
			}
			if !emit(event.Text(segment.Content)) {
				return false
			}
		}
	}

	logger.Info("Force final stream completed",
		"conversation_id", params.ConversationID,
		"chunk_count", chunkCount,
		"text_chars", textChars,
		"thinking_chars", thinkingChars,
		"first_chunk_ms", firstChunkMs,
		"first_text_ms", firstTextMs,
		"final_answer_chars", finalText.Len(),
		"final_answer_preview", previewText(finalText.String(), 80),
		"elapsed_ms", elapsedMillis(startedAt),
	)
	return l.finish(ctx, emit, params, messages)
}

func (l *Loop) finish(ctx context.Context, emit func(event.Event) bool, params Params, messages []model.Message) bool {
	if !l.emitStageOutputs(ctx, emit, StageBeforeDone, StageContext{
		Params:   params,
		Messages: messages,
	}) {
		return false
	}
	if l.hooks.BeforeDone != nil && !l.hooks.BeforeDone(emit) {
		return false
	}
	return emit(event.Complete())
}

func (l *Loop) emitStageOutputs(ctx context.Context, emit func(event.Event) bool, timing StageTiming, stageContext StageContext) bool {
	for _, provider := range l.stageProviders {
		if provider == nil || provider.Timing() != timing {
			continue
		}
		data, err := provider.Produce(ctx, stageContext)
		if err != nil {
			return emit(event.Error("STAGE_OUTPUT_FAILED", "stage output failed", err.Error()))
		}
		if data == nil {
			continue
		}
		if !emit(event.StageOutput(provider.Name(), string(timing), data)) {
			return false
		}
	}
	return true
}

func (l *Loop) modelTools() []model.ToolDefinition {
	if l.tools == nil {
		return nil
	}
	defs := l.tools.Definitions()
	out := make([]model.ToolDefinition, 0, len(defs))
	for _, def := range defs {
		out = append(out, model.ToolDefinition{
			Name:        def.Name,
			Description: def.Description,
			Schema:      def.Schema,
		})
	}
	return out
}
