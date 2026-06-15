package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/learn-demo/agent-go/internal/runtime/agent"
	"github.com/learn-demo/agent-go/internal/runtime/event"
	"github.com/learn-demo/agent-go/internal/runtime/memory"
	"github.com/learn-demo/agent-go/internal/runtime/model"
	"github.com/learn-demo/agent-go/internal/runtime/tool"
)

const defaultMaxRounds = 5
const defaultTemperature = 0.7

type runConfig struct {
	maxRounds   int
	temperature float64
}

type ReactAgent struct {
	// model 是 OpenAI-compatible 模型运行时；Agent 只依赖 model.Model 接口。
	model model.Model
	// tools 是本地工具注册表，对应 Java dodo-agent 中注入的 ToolCallback 列表。
	tools *tool.Registry
	// maxRounds 限制 ReAct 轮数，避免模型无限循环调用工具。
	maxRounds int
	// logger 统一输出 Agent 级观测日志。
	logger *slog.Logger
	// memory 保存和加载会话历史，对应 Java BaseAgent + AiSessionService 的组合能力。
	memory memory.Store
	// maxHistoryRecords 控制每次请求最多加载多少条历史问答。
	maxHistoryRecords int
}

// Option 是 ReactAgent 的可选配置入口。
// 当前主要用于调整教学/调试时的最大 ReAct 轮数，后续也可以扩展模型温度等运行参数。
type Option func(*ReactAgent)

// New 装配 WebSearch ReactAgent 的运行依赖。
//
// 这里的依赖关系刻意保持简单：
//   - model 负责和 AI 平台通信并返回流式 chunk。
//   - tools 负责把本地工具定义暴露给模型，并按名称执行工具。
//   - logger 负责输出 Agent 主流程观测日志。
//
// Java dodo-agent 里这些能力散落在 ChatClient、ToolCallback、BaseAgent、TaskManager 等对象中；
// Go 版为了学习链路清晰，把核心运行依赖收敛到 ReactAgent 结构体字段。
func New(model model.Model, tools *tool.Registry, logger *slog.Logger, opts ...Option) *ReactAgent {
	if logger == nil {
		logger = slog.Default()
	}

	a := &ReactAgent{
		model:             model,
		tools:             tools,
		maxRounds:         defaultMaxRounds,
		logger:            logger,
		memory:            memory.NoopStore{},
		maxHistoryRecords: 30,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// WithMaxRounds 设置最大 ReAct 推理轮数。
// 如果模型持续返回 tool_calls，达到该轮数后会进入 forceFinalStream，要求模型直接总结。
func WithMaxRounds(maxRounds int) Option {
	return func(a *ReactAgent) {
		if maxRounds > 0 {
			a.maxRounds = maxRounds
		}
	}
}

// WithMemory 接入会话记忆运行时。
// Go 版对应 Java 中 createPersistentChatMemory + sessionService.saveQuestion/updateAnswer。
func WithMemory(store memory.Store, maxHistoryRecords int) Option {
	return func(a *ReactAgent) {
		if store != nil {
			a.memory = store
		}
		if maxHistoryRecords > 0 {
			a.maxHistoryRecords = maxHistoryRecords
		}
	}
}

func (a *ReactAgent) runConfig(input agent.Input) runConfig {
	cfg := runConfig{
		maxRounds:   a.maxRounds,
		temperature: defaultTemperature,
	}
	if input.MaxRounds > 0 {
		cfg.maxRounds = input.MaxRounds
	}
	if input.Temperature != nil {
		cfg.temperature = *input.Temperature
	}
	if cfg.temperature < 0 {
		cfg.temperature = 0
	}
	if cfg.temperature > 2 {
		cfg.temperature = 2
	}
	return cfg
}

// Run 是 WebSearch ReAct Agent 的外部入口。
//
// 这层只做三件事：
//  1. 创建一个事件 channel，作为 Agent 到 HTTP SSE 的输出通道。
//  2. 在 goroutine 中执行完整 ReAct 流程，避免阻塞 HTTP handler。
//  3. 把用户问题包装成 Java dodo-agent 同款初始 messages，然后交给 scheduleRounds。
//
// 这里不要直接调用模型或工具；真正的 ReAct 轮次控制在 scheduleRounds 中完成。
func (a *ReactAgent) Run(ctx context.Context, input agent.Input) (<-chan event.Event, error) {
	events := make(chan event.Event, 16)

	go func() {
		defer close(events)
		startedAt := time.Now()
		runRecord := newRunRecord(input.ConversationID, input.Query, startedAt)
		logger := a.logger
		if input.RequestID != "" {
			logger = logger.With("request_id", input.RequestID)
		}
		defer a.persistRun(ctx, logger, input, runRecord)
		runCfg := a.runConfig(input)

		logger.Info("\U0001F916 WebSearch ReAct Agent 已启动",
			"conversation_id", input.ConversationID,
			"query", input.Query,
			"query_chars", len(input.Query),
			"max_rounds", runCfg.maxRounds,
			"temperature", runCfg.temperature,
		)

		// send 是 Agent 内部唯一的事件出口。
		// 所有 thinking/text/tool/reference/complete 都必须经过这里发给 HTTP SSE。
		// 如果客户端断开，request context 会取消，send 返回 false，Agent 立即停止后续工作。
		send := func(evt event.Event) bool {
			select {
			case <-ctx.Done():
				logger.Warn("\U0001F6D1 Agent 事件发送被取消",
					"conversation_id", input.ConversationID,
					"event_type", evt.Type,
					"elapsed_ms", elapsedMillis(startedAt),
					"error", ctx.Err(),
				)
				return false
			default:
			}
			select {
			case <-ctx.Done():
				logger.Warn("\U0001F6D1 Agent 事件发送被取消",
					"conversation_id", input.ConversationID,
					"event_type", evt.Type,
					"elapsed_ms", elapsedMillis(startedAt),
					"error", ctx.Err(),
				)
				return false
			case events <- evt:
				runRecord.capture(evt, elapsedMillis(startedAt))
				return true
			}
		}

		// 初始消息严格对应 Java WebSearchReactAgent：系统提示词 + 包裹在 <question> 中的用户问题。
		messages := []model.Message{
			{Role: model.RoleSystem, Content: webSearchPrompt(time.Now())},
		}
		messages = a.appendHistory(ctx, logger, input.ConversationID, messages)
		messages = append(messages, model.Message{Role: model.RoleUser, Content: "<question>" + input.Query + "</question>"})
		a.saveRunQuestion(ctx, logger, input, runRecord)

		// agentState 是跨轮次状态，目前主要保存搜索结果。
		// roundState 只存在于单个轮次内；agentState 会一直传到最终输出 reference。
		state := agentState{searchResults: make([]tool.SearchResult, 0)}
		if !a.scheduleRounds(ctx, send, input.ConversationID, input.RequestID, runCfg, messages, &state) {
			logger.Warn("\U000026A0 WebSearch ReAct Agent 未完成即停止",
				"conversation_id", input.ConversationID,
				"elapsed_ms", elapsedMillis(startedAt),
				"error", ctx.Err(),
			)
			return
		}
		logger.Info("\U0001F60A WebSearch ReAct Agent 已完成",
			"conversation_id", input.ConversationID,
			"reference_count", len(state.searchResults),
			"elapsed_ms", elapsedMillis(startedAt),
		)
	}()

	return events, nil
}

// scheduleRounds 是 ReAct 主循环，对应 Java WebSearchReactAgent 的 scheduleRound + finishRound。
//
// 一轮的完整流程是：
//  1. 带着当前 messages 和 tools schema 请求模型流式输出。
//  2. streamRound 边收 content 边发送 text/thinking 事件，同时收集 tool_calls。
//  3. 如果本轮没有 tool_calls，说明模型已经给出最终答案，输出 reference + complete 后结束。
//  4. 如果本轮有 tool_calls，先把 assistant(tool_calls) 放回 messages，再执行工具。
//  5. 把每个工具结果作为 tool message 追加到 messages，然后进入下一轮。
//
// 这个判断和 dodo-agent 保持一致：不是解析自然语言 JSON action，而是依赖模型原生 tool_calls。
func (a *ReactAgent) scheduleRounds(ctx context.Context, send func(event.Event) bool, conversationID string, requestID string, cfg runConfig, messages []model.Message, agentState *agentState) bool {
	logger := a.logger
	if requestID != "" {
		logger = logger.With("request_id", requestID)
	}
	for round := 1; round <= cfg.maxRounds; round++ {
		roundStartedAt := time.Now()
		toolDefs := a.modelTools()
		logger.Info("\U0001F501 ReAct 推理轮次开始",
			"conversation_id", conversationID,
			"round", round,
			"message_count", len(messages),
			"tool_schema_count", len(toolDefs),
		)

		if !send(event.Thinking(fmt.Sprintf("ReAct round %d started\n", round))) {
			return false
		}

		// streamRound 只负责“跑一轮模型流”：它不会执行工具，也不会决定是否继续下一轮。
		// 轮次结束后的模式判断统一放在 scheduleRounds，方便和 Java finishRound 对齐。
		state, ok := a.streamRound(ctx, send, conversationID, requestID, cfg.temperature, round, messages, toolDefs)
		if !ok {
			logger.Warn("\U000026A0 ReAct 推理轮次被中断",
				"conversation_id", conversationID,
				"round", round,
				"elapsed_ms", elapsedMillis(roundStartedAt),
				"error", ctx.Err(),
			)
			return false
		}

		// Java finishRound 的关键语义：本轮没有 tool_call，就把本轮文本视为最终答案。
		if len(state.toolCalls) == 0 {
			logger.Info("\U0001F3AF 当前轮次判定为最终回答",
				"conversation_id", conversationID,
				"round", round,
				"content_chars", state.textBuffer.Len(),
				"final_answer_chars", state.textBuffer.Len(),
				"final_answer_preview", previewText(state.textBuffer.String(), 80),
				"elapsed_ms", elapsedMillis(roundStartedAt),
			)
			return a.finishFinal(send, conversationID, requestID, agentState)
		}

		logger.Info("\U0001F6E0 当前轮次进入工具调用模式",
			"conversation_id", conversationID,
			"round", round,
			"tool_call_count", len(state.toolCalls),
			"content_chars", state.textBuffer.Len(),
			"elapsed_ms", elapsedMillis(roundStartedAt),
		)

		// 先把 assistant tool_calls 放回上下文，再执行工具并追加 tool response。
		messages = append(messages, model.Message{
			Role:      model.RoleAssistant,
			Content:   state.textBuffer.String(),
			ToolCalls: state.toolCalls,
		})

		if round >= cfg.maxRounds {
			// 已经达到最大轮次时不能再执行工具，否则可能继续触发下一轮工具调用。
			// 这里追加一条 user 指令，强制模型基于已有上下文直接总结最终答案。
			logger.Warn("\U000026A0 已达到最大推理轮次，强制进入最终回答",
				"conversation_id", conversationID,
				"round", round,
				"max_rounds", cfg.maxRounds,
			)
			if !send(event.Thinking("Max reasoning rounds reached, forcing final answer\n")) {
				return false
			}
			messages = append(messages, model.Message{
				Role:    model.RoleUser,
				Content: "The maximum reasoning rounds have been reached. Based on the current context, produce the final answer directly. Do not call tools again.",
			})
			return a.forceFinalStream(ctx, send, conversationID, requestID, cfg.temperature, messages, agentState)
		}

		// 工具执行结果必须以 tool role message 追加回上下文。
		// 下一轮模型才能看到“工具已经返回了什么”，并据此继续推理或生成最终答案。
		toolResponses, ok := a.executeToolCalls(ctx, send, conversationID, requestID, round, state.toolCalls, agentState)
		if !ok {
			logger.Warn("\U000026A0 工具执行流程被中断",
				"conversation_id", conversationID,
				"round", round,
				"error", ctx.Err(),
			)
			return false
		}
		messages = append(messages, toolResponses...)
	}

	return a.finishFinal(send, conversationID, requestID, agentState)
}

// streamRound 执行单个 ReAct 轮次里的模型流读取，对应 Java 的 processChunk。
//
// 它的职责边界非常明确：
//   - 看到文本 content：解析 <think> 标签，thinking 片段发 thinking 事件，正文发 text 事件并写入 textBuffer。
//   - 看到 tool_calls：进入工具调用模式，只合并 tool call，不立刻执行工具。
//   - 看到错误或取消：发送 error 事件并返回 ok=false。
//
// 注意：OpenAI-compatible 的 tool_calls 是流式 delta，function.arguments 可能被拆成多个 chunk。
// 因此这里不能把单个 chunk 当作完整工具调用，必须交给 roundState.mergeToolCall 合并。
func (a *ReactAgent) streamRound(ctx context.Context, send func(event.Event) bool, conversationID string, requestID string, temperature float64, round int, messages []model.Message, toolDefs []model.ToolDefinition) (roundState, bool) {
	startedAt := time.Now()
	logger := a.logger
	if requestID != "" {
		logger = logger.With("request_id", requestID)
	}
	logger.Info("\U0001F9E0 开始请求模型流式输出",
		"conversation_id", conversationID,
		"round", round,
		"message_count", len(messages),
		"tool_schema_count", len(toolDefs),
	)

	stream, err := a.model.Stream(ctx, model.Request{
		Temperature: temperature,
		Messages:    messages,
		Tools:       toolDefs,
		RequestID:   requestID,
	})
	if err != nil {
		logger.Error("\U0000274C 模型流启动失败",
			"conversation_id", conversationID,
			"round", round,
			"elapsed_ms", elapsedMillis(startedAt),
			"error", err,
		)
		_ = send(event.Error("LLM_CALL_FAILED", "model stream failed to start", err.Error()))
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
		// 模型层把 HTTP/Scanner/JSON 错误包装到 chunk.Err，Agent 层负责转成 SSE error。
		if chunk.Err != nil {
			logger.Error("\U0000274C 模型流读取失败",
				"conversation_id", conversationID,
				"round", round,
				"chunk_count", chunkCount,
				"content_chars", state.textBuffer.Len(),
				"tool_delta_count", toolDeltaCount,
				"elapsed_ms", elapsedMillis(startedAt),
				"error", chunk.Err,
			)
			_ = send(event.Error("LLM_CALL_FAILED", "model stream failed", chunk.Err.Error()))
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
			// 一旦发现 tool_calls，本轮就被标记为工具模式。
			// 后续即使模型又输出文本，也不应该把这轮当最终答案。
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
		if chunk.Content != "" {
			textChunkCount++
			if firstTextMs < 0 {
				firstTextMs = elapsedMillis(startedAt)
			}
			// dodo-agent 会解析 <think> 标签，把思考过程和答案正文分开推给前端。
			// Go 版保持同样语义，并且 inThink 可以跨 chunk 延续。
			for _, segment := range parseThinkSegments(chunk.Content, &state.inThink) {
				if segment.thinking {
					thinkingChars += len(segment.content)
					if !send(event.Thinking(segment.content)) {
						return state, false
					}
				} else {
					if !send(event.Text(segment.content)) {
						return state, false
					}
					state.textBuffer.WriteString(segment.content)
				}
			}
		}
	}

	logger.Info("\U00002705 模型流读取完成",
		"conversation_id", conversationID,
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

// executeToolCalls 执行本轮模型请求的所有工具调用，对应 Java 的 executeToolCalls。
//
// 每个工具调用都会产生一对前端事件：
//  1. tool_start：告诉前端工具名称、tool_call_id、arguments。
//  2. tool_end：告诉前端工具结果，失败时也以 tool_end 返回错误 JSON，保证模型上下文闭环。
//
// 返回值是要追加到 messages 的 tool role message。顺序保持和模型 tool_calls 顺序一致。
func (a *ReactAgent) executeToolCalls(ctx context.Context, send func(event.Event) bool, conversationID string, requestID string, round int, calls []model.ToolCall, agentState *agentState) ([]model.Message, bool) {
	batchStartedAt := time.Now()
	logger := a.logger
	if requestID != "" {
		logger = logger.With("request_id", requestID)
	}
	logger.Info("\U0001F6E0 开始执行本轮工具调用批次",
		"conversation_id", conversationID,
		"round", round,
		"tool_call_count", len(calls),
	)

	responses := make([]model.Message, 0, len(calls))
	for _, call := range calls {
		callStartedAt := time.Now()
		toolName := call.Name
		argsJSON := call.Arguments
		if strings.Contains(toolName, "search") {
			// 搜索类工具执行前额外发 thinking，让前端能展示“正在搜索”状态。
			query := extractQuery(argsJSON)
			if query != "" {
				if !send(event.Thinking("Searching information: " + query + "\n")) {
					return nil, false
				}
			} else if !send(event.Thinking("Searching related information\n")) {
				return nil, false
			}
		}

		// tool_start 必须早于真正执行工具；这样慢工具也能在前端看到进度。
		if !send(event.ToolStart(toolName, call.ID, argsJSON)) {
			return nil, false
		}

		logger.Info("\U0001F527 工具调用开始",
			"conversation_id", conversationID,
			"round", round,
			"tool", toolName,
			"tool_call_id", call.ID,
			"args_chars", len(argsJSON),
			"args_summary", toolArgsSummary(argsJSON),
		)

		// 模型给出的 arguments 是 JSON 字符串；本地工具运行时使用 map[string]any。
		args := map[string]any{}
		if strings.TrimSpace(argsJSON) != "" {
			if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
				result := `{ "error": "invalid tool arguments: ` + escapeForJSON(err.Error()) + `" }`
				logger.Error("\U0000274C 工具参数解析失败",
					"conversation_id", conversationID,
					"round", round,
					"tool", toolName,
					"tool_call_id", call.ID,
					"args_chars", len(argsJSON),
					"elapsed_ms", elapsedMillis(callStartedAt),
					"error", err,
				)
				if !send(event.ToolEnd(toolName, call.ID, result)) {
					return nil, false
				}
				// 即使参数错误，也要把错误作为 tool response 追加回模型上下文。
				// 否则下一轮模型会认为这个 tool_call 没有对应响应。
				responses = append(responses, toolResponse(call, result))
				continue
			}
		}

		result, err := a.tools.Execute(ctx, toolName, args)
		if err != nil {
			resultText := `{ "error": "` + escapeForJSON(err.Error()) + `" }`
			logger.Error("\U0000274C 工具调用失败",
				"conversation_id", conversationID,
				"round", round,
				"tool", toolName,
				"tool_call_id", call.ID,
				"elapsed_ms", elapsedMillis(callStartedAt),
				"error", err,
			)
			if !send(event.ToolEnd(toolName, call.ID, resultText)) {
				return nil, false
			}
			// 工具运行失败同样返回 tool response，让模型有机会解释失败原因或换策略。
			responses = append(responses, toolResponse(call, resultText))
			continue
		}

		// 搜索工具的结构化结果会被收集到 agentState，最终作为 reference 事件输出。
		a.collectSearchResults(toolName, result, agentState)
		resultText := result.Content
		if result.Data != nil {
			resultText = tool.MustJSON(result.Data)
		}
		logger.Info("\U00002705 工具调用完成",
			"conversation_id", conversationID,
			"round", round,
			"tool", toolName,
			"tool_call_id", call.ID,
			"result_chars", len(resultText),
			"search_reference_count", len(agentState.searchResults),
			"elapsed_ms", elapsedMillis(callStartedAt),
		)
		if !send(event.ToolEnd(toolName, call.ID, resultText)) {
			return nil, false
		}
		// tool response 的 ToolCallID 必须和 assistant tool_call.ID 一致，这是 OpenAI tool 协议要求。
		responses = append(responses, toolResponse(call, resultText))
	}

	logger.Info("\U00002705 本轮工具调用批次完成",
		"conversation_id", conversationID,
		"round", round,
		"tool_response_count", len(responses),
		"reference_count", len(agentState.searchResults),
		"elapsed_ms", elapsedMillis(batchStartedAt),
	)
	return responses, true
}

// forceFinalStream 在达到 maxRounds 后触发，强制模型停止工具调用并输出最终答案。
//
// 正常 ReAct 是“模型决定是否继续调用工具”；但为了防止无限循环，超过最大轮次后：
//   - 不再传 Tools 给模型；
//   - 追加一条 user message，明确要求直接总结；
//   - 流式输出仍然复用 text/thinking 事件协议。
func (a *ReactAgent) forceFinalStream(ctx context.Context, send func(event.Event) bool, conversationID string, requestID string, temperature float64, messages []model.Message, agentState *agentState) bool {
	startedAt := time.Now()
	logger := a.logger
	if requestID != "" {
		logger = logger.With("request_id", requestID)
	}
	logger.Info("\U0001F9E0 开始请求强制最终回答模型流",
		"conversation_id", conversationID,
		"message_count", len(messages),
	)

	stream, err := a.model.Stream(ctx, model.Request{
		Temperature: temperature,
		Messages:    messages,
		RequestID:   requestID,
	})
	if err != nil {
		logger.Error("\U0000274C 强制最终回答模型流启动失败",
			"conversation_id", conversationID,
			"elapsed_ms", elapsedMillis(startedAt),
			"error", err,
		)
		_ = send(event.Error("LLM_CALL_FAILED", "force final stream failed to start", err.Error()))
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
			logger.Error("\U0000274C 强制最终回答模型流读取失败",
				"conversation_id", conversationID,
				"chunk_count", chunkCount,
				"text_chars", textChars,
				"thinking_chars", thinkingChars,
				"elapsed_ms", elapsedMillis(startedAt),
				"error", chunk.Err,
			)
			_ = send(event.Error("LLM_CALL_FAILED", "force final stream failed", chunk.Err.Error()))
			return false
		}
		if chunk.Done {
			break
		}
		if firstChunkMs < 0 {
			firstChunkMs = elapsedMillis(startedAt)
		}
		chunkCount++
		for _, segment := range parseThinkSegments(chunk.Content, &state.inThink) {
			if segment.thinking {
				thinkingChars += len(segment.content)
				if !send(event.Thinking(segment.content)) {
					return false
				}
			} else {
				textChars += len(segment.content)
				finalText.WriteString(segment.content)
				if firstTextMs < 0 {
					firstTextMs = elapsedMillis(startedAt)
				}
				if !send(event.Text(segment.content)) {
					return false
				}
			}
		}
	}

	logger.Info("\U00002705 强制最终回答模型流读取完成",
		"conversation_id", conversationID,
		"chunk_count", chunkCount,
		"text_chars", textChars,
		"thinking_chars", thinkingChars,
		"first_chunk_ms", firstChunkMs,
		"first_text_ms", firstTextMs,
		"final_answer_chars", finalText.Len(),
		"final_answer_preview", previewText(finalText.String(), 80),
		"elapsed_ms", elapsedMillis(startedAt),
	)
	return a.finishFinal(send, conversationID, requestID, agentState)
}

// finishFinal 是 Agent 最终收口点。
//
// 输出顺序保持稳定：
//  1. 如果搜索工具收集到了引用，先发 reference。
//  2. 最后发 complete，通知 HTTP/SSE 层可以正常关闭。
//
// 注意最终答案正文不是在这里发的；正文已经在 streamRound/forceFinalStream 中边生成边发 text。
func (a *ReactAgent) finishFinal(send func(event.Event) bool, conversationID string, requestID string, state *agentState) bool {
	logger := a.logger
	if requestID != "" {
		logger = logger.With("request_id", requestID)
	}
	if len(state.searchResults) > 0 {
		logger.Info("\U0001F4DA 已输出搜索引用", "conversation_id", conversationID, "reference_count", len(state.searchResults))
		if !send(event.Reference(tool.MustJSON(state.searchResults), len(state.searchResults))) {
			return false
		}
	}
	logger.Info("\U0001F3C1 已输出 complete 事件", "conversation_id", conversationID, "reference_count", len(state.searchResults))
	_ = send(event.Complete())
	return true
}

// modelTools 把本地工具注册表转换成模型请求里的 tools schema。
//
// Java dodo-agent 通过 Spring AI 的 ToolCallback 暴露工具；
// Go 版在这里显式生成 OpenAI-compatible 的 tool definition，
// 让模型能够在流式响应中返回原生 tool_calls。
func (a *ReactAgent) modelTools() []model.ToolDefinition {
	if a.tools == nil {
		return nil
	}
	defs := a.tools.Definitions()
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

// appendHistory 从 Memory Runtime 加载最近问答，并插入到系统提示词之后、当前问题之前。
// 这对应 Java BaseAgent.loadChatHistory(conversationId, messages, true, true)。
func (a *ReactAgent) appendHistory(ctx context.Context, logger *slog.Logger, conversationID string, messages []model.Message) []model.Message {
	if strings.TrimSpace(conversationID) == "" || a.memory == nil || a.maxHistoryRecords <= 0 {
		return messages
	}

	startedAt := time.Now()
	records, err := a.memory.FindRecent(ctx, conversationID, a.maxHistoryRecords)
	if err != nil {
		logger.Warn("\U000026A0 会话历史加载失败，将不携带历史继续执行",
			"conversation_id", conversationID,
			"max_history_records", a.maxHistoryRecords,
			"elapsed_ms", elapsedMillis(startedAt),
			"error", err,
		)
		return messages
	}
	if len(records) == 0 {
		logger.Info("\U0001F4DD 未找到可加载的会话历史",
			"conversation_id", conversationID,
			"max_history_records", a.maxHistoryRecords,
			"elapsed_ms", elapsedMillis(startedAt),
		)
		return messages
	}

	historyMessages := memory.HistoryMessages(records)
	if len(historyMessages) == 0 {
		logger.Info("\U0001F4DD 会话历史记录为空，未追加历史消息",
			"conversation_id", conversationID,
			"record_count", len(records),
			"elapsed_ms", elapsedMillis(startedAt),
		)
		return messages
	}

	out := make([]model.Message, 0, len(messages)+1+len(historyMessages))
	out = append(out, messages...)
	out = append(out, model.Message{Role: model.RoleUser, Content: "对话历史："})
	out = append(out, historyMessages...)

	logger.Info("\U0001F4DD 会话历史已加载",
		"conversation_id", conversationID,
		"record_count", len(records),
		"history_message_count", len(historyMessages),
		"max_history_records", a.maxHistoryRecords,
		"elapsed_ms", elapsedMillis(startedAt),
	)
	return out
}

func (a *ReactAgent) saveRunQuestion(ctx context.Context, logger *slog.Logger, input agent.Input, record *runRecord) {
	if a.memory == nil || strings.TrimSpace(input.ConversationID) == "" {
		return
	}

	startedAt := time.Now()
	saved, err := a.memory.SaveQuestion(ctx, memory.SaveQuestionRequest{
		SessionID: input.ConversationID,
		AgentType: "websearch",
		Question:  input.Query,
	})
	if err != nil {
		logger.Warn("\U000026A0 会话问题保存失败",
			"conversation_id", input.ConversationID,
			"query_chars", len(input.Query),
			"elapsed_ms", elapsedMillis(startedAt),
			"error", err,
		)
		return
	}
	if saved.ID <= 0 {
		return
	}
	record.sessionRecordID = saved.ID
	logger.Info("\U0001F4BE 会话问题已保存",
		"conversation_id", input.ConversationID,
		"session_record_id", saved.ID,
		"query_chars", len(input.Query),
		"elapsed_ms", elapsedMillis(startedAt),
	)
}

func (a *ReactAgent) persistRun(ctx context.Context, logger *slog.Logger, input agent.Input, record *runRecord) {
	if a.memory == nil || strings.TrimSpace(input.ConversationID) == "" || record == nil || record.sessionRecordID <= 0 {
		return
	}

	startedAt := time.Now()
	if strings.TrimSpace(record.answer.String()) == "" && ctx.Err() != nil {
		logger.Warn("\U000026A0 请求取消且没有最终回答，跳过会话回答更新",
			"conversation_id", input.ConversationID,
			"session_record_id", record.sessionRecordID,
			"error", ctx.Err(),
		)
		return
	}

	if err := a.memory.UpdateAnswer(context.Background(), memory.UpdateAnswerRequest{
		ID:                record.sessionRecordID,
		Answer:            record.answer.String(),
		Thinking:          record.thinking.String(),
		Tools:             record.toolsString(),
		Reference:         record.reference,
		Recommend:         record.recommend,
		FirstResponseTime: record.firstResponseMs,
		TotalResponseTime: elapsedMillis(record.startedAt),
	}); err != nil {
		logger.Warn("\U000026A0 会话回答保存失败",
			"conversation_id", input.ConversationID,
			"session_record_id", record.sessionRecordID,
			"answer_chars", record.answer.Len(),
			"thinking_chars", record.thinking.Len(),
			"elapsed_ms", elapsedMillis(startedAt),
			"error", err,
		)
		return
	}

	logger.Info("\U0001F4BE 会话结果已保存",
		"conversation_id", input.ConversationID,
		"session_record_id", record.sessionRecordID,
		"answer_chars", record.answer.Len(),
		"thinking_chars", record.thinking.Len(),
		"tools", record.toolsString(),
		"has_reference", record.reference != "",
		"first_response_ms", record.firstResponseMs,
		"total_response_ms", elapsedMillis(record.startedAt),
		"elapsed_ms", elapsedMillis(startedAt),
	)
}

// collectSearchResults 从搜索工具的结构化 Data 中提取引用。
// 只有工具名包含 search 且 Data.results 存在时才会收集，避免普通工具污染 reference 输出。
func (a *ReactAgent) collectSearchResults(toolName string, result tool.Result, state *agentState) {
	if !strings.Contains(toolName, "search") || result.Data == nil {
		return
	}
	raw, ok := result.Data["results"]
	if !ok {
		return
	}
	state.searchResults = append(state.searchResults, searchResultsFromAny(raw)...)
}

// toolResponse 把工具执行结果包装成模型协议要求的 tool role message。
// 这是下一轮模型能“看见工具结果”的关键消息。
func toolResponse(call model.ToolCall, content string) model.Message {
	return model.Message{
		Role:       model.RoleTool,
		Content:    content,
		Name:       call.Name,
		ToolCallID: call.ID,
	}
}

type roundMode string

const (
	// roundModeUnknown 表示当前轮没有发现工具调用，通常会走最终答案分支。
	roundModeUnknown roundMode = "unknown"
	// roundModeToolCall 表示本轮模型输出了 tool_calls，需要执行工具后进入下一轮。
	roundModeToolCall roundMode = "tool_call"
)

// roundState 保存单个 ReAct 轮次内的临时状态。
// 它对应 Java dodo-agent 的 RoundState：mode/textBuffer/toolCalls/inThink。
type roundState struct {
	// mode 标记当前轮是否进入工具调用模式。
	mode roundMode
	// textBuffer 只收集非 thinking 的正文；如果没有 tool_call，它就是最终答案。
	textBuffer strings.Builder
	// toolCalls 保存本轮合并后的模型原生工具调用。
	toolCalls []model.ToolCall
	// inThink 表示上一个 chunk 是否停留在 <think> 标签内部，用于跨 chunk 解析。
	inThink bool
}

func (s roundState) modeForLog() roundMode {
	if s.mode == "" {
		return roundModeUnknown
	}
	return s.mode
}

func (s *roundState) mergeToolCall(incoming model.ToolCall) {
	for i := range s.toolCalls {
		existing := &s.toolCalls[i]
		if sameToolCall(*existing, incoming) {
			if incoming.ID != "" {
				existing.ID = incoming.ID
			}
			if incoming.Name != "" {
				existing.Name = incoming.Name
			}
			// OpenAI-compatible 流式响应会把 function.arguments 拆成多个 delta，这里按顺序拼回完整 JSON。
			existing.Arguments += incoming.Arguments
			return
		}
	}
	s.toolCalls = append(s.toolCalls, incoming)
}

// sameToolCall 判断两个流式 delta 是否属于同一个工具调用。
// 有 ID 时优先用 ID；早期 delta 可能没有 ID，因此回退到 index。
func sameToolCall(left, right model.ToolCall) bool {
	if left.ID != "" && right.ID != "" {
		return left.ID == right.ID
	}
	return left.Index == right.Index
}

type agentState struct {
	// searchResults 跨轮次保存搜索结果，最终在 complete 前输出 reference 事件。
	searchResults []tool.SearchResult
}

// runRecord 保存一次 Agent Run 的可持久化结果。
// Java dodo-agent 是在 Flux.doOnNext/doFinally 中收集这些字段；Go 版在 send 事件出口统一收集。
type runRecord struct {
	conversationID    string
	question          string
	sessionRecordID   int64
	startedAt         time.Time
	answer            strings.Builder
	thinking          strings.Builder
	reference         string
	recommend         string
	firstResponseMs   int64
	tools             map[string]struct{}
	completedNormally bool
}

func newRunRecord(conversationID, question string, startedAt time.Time) *runRecord {
	return &runRecord{
		conversationID:  conversationID,
		question:        question,
		startedAt:       startedAt,
		firstResponseMs: -1,
		tools:           make(map[string]struct{}),
	}
}

func (r *runRecord) capture(evt event.Event, elapsedMs int64) {
	if r == nil {
		return
	}
	if r.firstResponseMs < 0 && (evt.Type == event.TypeText || evt.Type == event.TypeThinking || evt.Type == event.TypeToolStart || evt.Type == event.TypeReference) {
		r.firstResponseMs = elapsedMs
	}

	switch evt.Type {
	case event.TypeText:
		r.answer.WriteString(evt.Content)
	case event.TypeThinking:
		r.thinking.WriteString(evt.Content)
	case event.TypeToolStart, event.TypeToolEnd:
		if evt.ToolName != "" {
			r.tools[evt.ToolName] = struct{}{}
		}
	case event.TypeReference:
		r.reference = evt.Content
	case event.TypeRecommend:
		r.recommend = evt.Content
	case event.TypeComplete:
		r.completedNormally = true
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

type thinkSegment struct {
	// thinking=true 表示该片段来自 <think> 标签内部。
	thinking bool
	// content 是已经剥离 think 标签后的文本片段。
	content string
}

// parseThinkSegments 把模型文本按 <think> 标签切成 thinking/text 两类片段。
// inThink 是指针，因为 <think> 和 </think> 可能分布在不同流式 chunk 中。
func parseThinkSegments(chunk string, inThink *bool) []thinkSegment {
	if chunk == "" {
		return nil
	}

	const startTag = "<think"
	const endTag = "</think"

	var segments []thinkSegment
	currentInThink := *inThink
	index := 0

	for index < len(chunk) {
		start := strings.Index(chunk[index:], startTag)
		end := strings.Index(chunk[index:], endTag)
		if start >= 0 {
			start += index
		}
		if end >= 0 {
			end += index
		}

		if start < 0 && end < 0 {
			segments = append(segments, thinkSegment{thinking: currentInThink, content: chunk[index:]})
			break
		}

		next := start
		isStart := true
		if next < 0 || (end >= 0 && end < next) {
			next = end
			isStart = false
		}

		if next > index {
			segments = append(segments, thinkSegment{thinking: currentInThink, content: chunk[index:next]})
		}

		tagEnd := strings.Index(chunk[next:], ">")
		if tagEnd < 0 {
			currentInThink = isStart
			break
		}
		currentInThink = isStart
		index = next + tagEnd + 1
	}

	*inThink = currentInThink
	return segments
}

// extractQuery 只用于搜索工具的 thinking 提示，不参与工具执行本身。
func extractQuery(argsJSON string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}
	value, ok := args["query"]
	if !ok {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

// searchResultsFromAny 把工具返回的 results 字段转换成统一 SearchResult 切片。
// mock 工具和真实 web_search 工具可能返回不同的动态类型，因此这里做兼容转换。
func searchResultsFromAny(value any) []tool.SearchResult {
	switch typed := value.(type) {
	case []tool.SearchResult:
		return typed
	case []any:
		results := make([]tool.SearchResult, 0, len(typed))
		for _, item := range typed {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			results = append(results, tool.SearchResult{
				Title:   fmt.Sprint(itemMap["title"]),
				URL:     fmt.Sprint(itemMap["url"]),
				Content: fmt.Sprint(itemMap["content"]),
			})
		}
		return results
	default:
		return nil
	}
}

// escapeForJSON 用于把错误文本安全塞进简短 JSON 字符串。
func escapeForJSON(value string) string {
	payload, err := json.Marshal(value)
	if err != nil {
		return value
	}
	trimmed := string(payload)
	return strings.Trim(trimmed, `"`)
}

// toolArgsSummary 给日志输出短参数摘要，避免完整 arguments 太长或包含敏感内容。
func toolArgsSummary(argsJSON string) string {
	if strings.TrimSpace(argsJSON) == "" {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "invalid_json"
	}
	preferred := []string{"query", "timezone", "city"}
	parts := make([]string, 0, len(preferred))
	for _, key := range preferred {
		if value, ok := args[key]; ok {
			parts = append(parts, key+"="+previewText(fmt.Sprint(value), 60))
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, ",")
	}
	return fmt.Sprintf("keys=%d", len(args))
}

// previewText 压缩空白并截断文本，用于日志中的最终回答预览。
func previewText(value string, limit int) string {
	normalized := strings.Join(strings.Fields(value), " ")
	if limit <= 0 || len([]rune(normalized)) <= limit {
		return normalized
	}
	runes := []rune(normalized)
	return string(runes[:limit]) + "..."
}

func elapsedMillis(startedAt time.Time) int64 {
	return time.Since(startedAt).Milliseconds()
}

func webSearchPrompt(now time.Time) string {
	return fmt.Sprintf(`You are xty, an intelligent Q&A assistant.

Current system time:
%s

Core thinking principles:
1. Identify the subject, time dimension, and core event in the user question.
2. Use the search tool when information needs verification or may be current.
3. Filter search results so the answer matches the user's time requirements.

Final answer rules:
- Output natural language only.
- Do not include tool call formats in the final answer.
- Once enough information is available, do not call tools again.

Output specifications:
- Prefer structured lists, tables, and categories where useful.
- Keep the answer clear, readable, and comprehensive.

Mandatory requirements:
1. Tool calls must be emitted only through the model tool_call field.
2. If no tool call is emitted in a round, that round is treated as the final answer.
3. Do not output intermediate parser artifacts.`, now.Format(time.RFC3339))
}

var _ agent.Agent = (*ReactAgent)(nil)
