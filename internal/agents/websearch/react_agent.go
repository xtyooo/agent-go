package websearch

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"agentG/internal/runtime/agent"
	"agentG/internal/runtime/contextx"
	"agentG/internal/runtime/event"
	"agentG/internal/runtime/memory"
	"agentG/internal/runtime/model"
	reactruntime "agentG/internal/runtime/react"
	"agentG/internal/runtime/tool"
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
	// contextPolicy 控制 prompt 上下文预算和历史裁剪。
	contextPolicy contextx.Policy
	// agentType 写入 Memory 的 agent_type，也用于日志识别。
	agentType string
	// systemPromptBuilder 生成系统提示词。Skills Agent 会复用 ReAct 循环，但替换提示词。
	systemPromptBuilder func(time.Time) string
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
		contextPolicy:     contextx.DefaultPolicy(),
		agentType:         "websearch",
		systemPromptBuilder: func(now time.Time) string {
			return webSearchPrompt(now)
		},
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// WithAgentType 设置 Agent 类型，用于日志和会话持久化。
func WithAgentType(agentType string) Option {
	return func(a *ReactAgent) {
		if strings.TrimSpace(agentType) != "" {
			a.agentType = strings.TrimSpace(agentType)
		}
	}
}

// WithSystemPrompt 设置 ReAct Agent 的系统提示词构造器。
// Skills Agent 会复用 WebSearch 的 tool_calls 主循环，但使用自己的系统提示词。
func WithSystemPrompt(builder func(time.Time) string) Option {
	return func(a *ReactAgent) {
		if builder != nil {
			a.systemPromptBuilder = builder
		}
	}
}

// WithMaxRounds 设置最大 ReAct 推理轮数。
// 如果模型持续返回 tool_calls，达到该轮数后会由 runtime/react 强制进入最终回答。
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

// WithContextPolicy 设置上下文预算策略。
// 它用于裁剪历史消息，避免长会话无限膨胀。
func WithContextPolicy(policy contextx.Policy) Option {
	return func(a *ReactAgent) {
		a.contextPolicy = policy.Normalize()
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

// Run 是 ReAct Agent 的外部入口。
//
// 这层只做三件事：
//  1. 创建一个事件 channel，作为 Agent 到 HTTP SSE 的输出通道。
//  2. 在 goroutine 中执行完整 ReAct 流程，避免阻塞 HTTP handler。
//  3. 把用户问题包装成 Java dodo-agent 同款初始 messages，然后交给 runtime/react loop。
//
// 这里不要直接调用模型或工具；真正的 ReAct 轮次控制在 runtime/react 中完成。
func (a *ReactAgent) Run(ctx context.Context, input agent.Input) (<-chan event.Event, error) {
	events := make(chan event.Event, 16)
	go a.runAsync(ctx, input, events)
	return events, nil
}

func (a *ReactAgent) runAsync(ctx context.Context, input agent.Input, events chan event.Event) {
	defer close(events)
	startedAt := time.Now()
	runRecord := newRunRecord(input.ConversationID, input.Query, startedAt)
	logger := a.logger
	if input.RequestID != "" {
		logger = logger.With("request_id", input.RequestID)
	}
	defer a.persistRun(ctx, logger, input, runRecord)
	runCfg := a.runConfig(input)

	logger.Info("\U0001F916 ReAct Agent 已启动",
		"conversation_id", input.ConversationID,
		"agent_type", a.agentType,
		"query", input.Query,
		"query_chars", len(input.Query),
		"max_rounds", runCfg.maxRounds,
		"temperature", runCfg.temperature,
	)

	sender := event.NewSender(event.SenderConfig{
		Ctx: ctx,
		Out: events,
		OnCancel: func(evt event.Event, err error) {
			logger.Warn("🚫 Agent 事件发送被取消",
				"conversation_id", input.ConversationID,
				"event_type", evt.Type,
				"elapsed_ms", elapsedMillis(startedAt),
				"error", err,
			)
		},
		AfterSend: func(evt event.Event) {
			runRecord.capture(evt, elapsedMillis(startedAt))
		},
	})
	if !sender.Send(event.AgentStart(a.agentType, input.ConversationID, input.RequestID)) {
		return
	}

	systemMessages := []model.Message{
		{Role: model.RoleSystem, Content: a.systemPromptBuilder(time.Now())},
	}
	historyMessages := a.loadHistory(ctx, logger, input.ConversationID)
	currentMessages := []model.Message{{Role: model.RoleUser, Content: "<question>" + input.Query + "</question>"}}
	contextResult := a.buildInitialContext(logger, input.ConversationID, systemMessages, historyMessages, currentMessages)
	messages := contextResult.Messages
	a.saveRunQuestion(ctx, logger, input, runRecord)

	state := agentState{searchResults: make([]tool.SearchResult, 0)}
	loop := reactruntime.New(a.model, a.tools, logger,
		reactruntime.WithMaxRounds(runCfg.maxRounds),
		reactruntime.WithStageProviders(a.referenceStageProvider(&state)),
		reactruntime.WithHooks(reactruntime.Hooks{
			BeforeTool: func(ctx context.Context, call model.ToolCall, emit func(event.Event) bool) bool {
				if !strings.Contains(call.Name, "search") {
					return true
				}
				query := reactruntime.ExtractQuery(call.Arguments)
				if query != "" {
					return emit(event.Thinking("Searching information: " + query + "\n"))
				}
				return emit(event.Thinking("Searching related information\n"))
			},
			AfterTool: func(ctx context.Context, call model.ToolCall, result tool.Result) {
				a.collectSearchResults(call.Name, result, &state)
			},
			BeforeDone: func(emit func(event.Event) bool) bool {
				if len(state.searchResults) == 0 {
					return true
				}
				logger.Info("Search references emitted",
					"conversation_id", input.ConversationID,
					"reference_count", len(state.searchResults),
				)
				return emit(event.Reference(tool.MustJSON(state.searchResults), len(state.searchResults)))
			},
		}),
	)
	if !loop.Stream(ctx, reactruntime.Params{
		ConversationID: input.ConversationID,
		RequestID:      input.RequestID,
		Temperature:    runCfg.temperature,
		MaxRounds:      runCfg.maxRounds,
	}, messages, sender.Send) {
		logger.Warn("\U000026A0 ReAct Agent 未完成即停止",
			"conversation_id", input.ConversationID,
			"agent_type", a.agentType,
			"elapsed_ms", elapsedMillis(startedAt),
			"error", ctx.Err(),
		)
		return
	}
	logger.Info("\U0001F60A ReAct Agent 已完成",
		"conversation_id", input.ConversationID,
		"agent_type", a.agentType,
		"reference_count", len(state.searchResults),
		"elapsed_ms", elapsedMillis(startedAt),
	)
}

func (a *ReactAgent) loadHistory(ctx context.Context, logger *slog.Logger, conversationID string) []model.Message {
	if strings.TrimSpace(conversationID) == "" || a.memory == nil || a.maxHistoryRecords <= 0 {
		return nil
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
		return nil
	}
	if len(records) == 0 {
		logger.Info("\U0001F4DD 未找到可加载的会话历史",
			"conversation_id", conversationID,
			"max_history_records", a.maxHistoryRecords,
			"elapsed_ms", elapsedMillis(startedAt),
		)
		return nil
	}

	historyMessages := memory.HistoryMessages(records)
	if len(historyMessages) == 0 {
		logger.Info("\U0001F4DD 会话历史记录为空，未追加历史消息",
			"conversation_id", conversationID,
			"record_count", len(records),
			"elapsed_ms", elapsedMillis(startedAt),
		)
		return nil
	}

	logger.Info("\U0001F4DD 会话历史已读取",
		"conversation_id", conversationID,
		"record_count", len(records),
		"history_message_count", len(historyMessages),
		"max_history_records", a.maxHistoryRecords,
		"elapsed_ms", elapsedMillis(startedAt),
	)
	return historyMessages
}

func (a *ReactAgent) buildInitialContext(logger *slog.Logger, conversationID string, systemMessages []model.Message, historyMessages []model.Message, currentMessages []model.Message) contextx.BuildResult {
	builder := contextx.NewBuilder(a.contextPolicy)
	result := builder.Build(
		contextx.Section{Name: contextx.SectionSystem, Messages: systemMessages},
		contextx.Section{Name: contextx.SectionHistory, Messages: historyMessages},
		contextx.Section{Name: contextx.SectionCurrent, Messages: currentMessages},
	)
	summary := result.Summary
	logger.Info("\U0001F9EE 上下文预算已应用",
		"conversation_id", conversationID,
		"message_count", len(result.Messages),
		"input_token_estimate", summary.InputTokenEstimate,
		"max_input_tokens", summary.Policy.MaxInputTokens,
		"reserved_output_tokens", summary.Policy.ReservedOutputTokens,
		"system_token_estimate", summary.SystemTokenEstimate,
		"history_token_estimate", summary.HistoryTokenEstimate,
		"history_token_before_trim", summary.HistoryTokenBeforeTrim,
		"history_message_input", summary.HistoryMessageInput,
		"history_message_kept", summary.HistoryMessageKept,
		"history_message_dropped", summary.HistoryMessageDropped,
		"current_token_estimate", summary.CurrentTokenEstimate,
	)
	return result
}

func (a *ReactAgent) saveRunQuestion(ctx context.Context, logger *slog.Logger, input agent.Input, record *runRecord) {
	if a.memory == nil || strings.TrimSpace(input.ConversationID) == "" {
		return
	}

	startedAt := time.Now()
	saved, err := a.memory.SaveQuestion(ctx, memory.SaveQuestionRequest{
		SessionID: input.ConversationID,
		AgentType: a.agentType,
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

func (a *ReactAgent) referenceStageProvider(state *agentState) reactruntime.StageOutputProvider {
	return reactruntime.StageOutputProviderFunc{
		ProviderName:   "reference",
		ProviderTiming: reactruntime.StageBeforeDone,
		Fn: func(ctx context.Context, stage reactruntime.StageContext) (any, error) {
			if state == nil || len(state.searchResults) == 0 {
				return nil, nil
			}
			return state.searchResults, nil
		},
	}
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
