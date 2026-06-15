package planexecute

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/learn-demo/agent-go/internal/runtime/agent"
	"github.com/learn-demo/agent-go/internal/runtime/contextx"
	"github.com/learn-demo/agent-go/internal/runtime/event"
	"github.com/learn-demo/agent-go/internal/runtime/memory"
	"github.com/learn-demo/agent-go/internal/runtime/model"
	"github.com/learn-demo/agent-go/internal/runtime/tool"
)

const (
	defaultMaxRounds   = 3
	defaultTemperature = 0.4
)

type Agent struct {
	// model 是模型运行时，负责需求澄清、研究主题生成、规划、评审和最终总结。
	model model.Model
	// tools 是工具注册表。当前 Plan-Execute 执行阶段先统一调用 web_search。
	tools *tool.Registry
	// logger 输出 Agent 主流程观测日志，保持中文 + emoji 便于控制台追踪。
	logger *slog.Logger
	// maxRounds 限制 plan -> execute -> critique 的最大迭代次数。
	maxRounds int
	// contextPolicy 控制 prompt 上下文预算，避免历史和任务结果无限膨胀。
	contextPolicy contextx.Policy
	// memory 保存和加载会话历史，对齐 Java PlanExecuteAgent 的 AiSessionService。
	memory memory.Store
	// maxHistoryRecords 控制每次请求最多加载多少条历史问答。
	maxHistoryRecords int
	// maxTasksPerRound 限制单轮最多执行多少个任务，避免 planner 一次展开过多搜索。
	maxTasksPerRound int
	// executionToolName 是当前执行器使用的工具名；后续可扩展为从任务中解析工具名。
	executionToolName string
}

type Option func(*Agent)

func New(model model.Model, tools *tool.Registry, logger *slog.Logger, opts ...Option) *Agent {
	if logger == nil {
		logger = slog.Default()
	}
	a := &Agent{
		model:             model,
		tools:             tools,
		logger:            logger,
		maxRounds:         defaultMaxRounds,
		contextPolicy:     contextx.DefaultPolicy(),
		memory:            memory.NoopStore{},
		maxHistoryRecords: 30,
		maxTasksPerRound:  4,
		executionToolName: "web_search",
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

func WithMaxRounds(maxRounds int) Option {
	return func(a *Agent) {
		if maxRounds > 0 {
			a.maxRounds = maxRounds
		}
	}
}

// WithMemory 接入会话记忆运行时。
// Plan-Execute 会在启动时保存问题，结束时保存最终报告、思考过程、工具和引用。
func WithMemory(store memory.Store, maxHistoryRecords int) Option {
	return func(a *Agent) {
		if store != nil {
			a.memory = store
		}
		if maxHistoryRecords > 0 {
			a.maxHistoryRecords = maxHistoryRecords
		}
	}
}

func WithContextPolicy(policy contextx.Policy) Option {
	return func(a *Agent) {
		a.contextPolicy = policy.Normalize()
	}
}

func (a *Agent) Run(ctx context.Context, input agent.Input) (<-chan event.Event, error) {
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
		cfg := a.runConfig(input)

		logger.Info("\U0001F9ED Plan-Execute Agent 已启动",
			"conversation_id", input.ConversationID,
			"query", input.Query,
			"query_chars", len(input.Query),
			"max_rounds", cfg.maxRounds,
			"temperature", cfg.temperature,
		)

		send := func(evt event.Event) bool {
			select {
			case <-ctx.Done():
				logger.Warn("\U0001F6D1 Plan-Execute 事件发送被取消",
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
				return false
			case events <- evt:
				runRecord.capture(evt, elapsedMillis(startedAt))
				return true
			}
		}

		state := &runState{
			question:        input.Query,
			historyMessages: a.loadHistory(ctx, logger, input.ConversationID),
			references:      make([]tool.SearchResult, 0),
		}
		a.saveRunQuestion(ctx, logger, input, runRecord)
		if !a.run(ctx, send, logger, input, cfg, state) {
			return
		}
		logger.Info("\U0001F3C1 Plan-Execute Agent 已完成",
			"conversation_id", input.ConversationID,
			"round_count", state.round,
			"task_count", len(state.results),
			"reference_count", len(state.references),
			"elapsed_ms", elapsedMillis(startedAt),
		)
	}()
	return events, nil
}

type runConfig struct {
	maxRounds   int
	temperature float64
}

func (a *Agent) runConfig(input agent.Input) runConfig {
	cfg := runConfig{maxRounds: a.maxRounds, temperature: defaultTemperature}
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

func (a *Agent) run(ctx context.Context, send func(event.Event) bool, logger *slog.Logger, input agent.Input, cfg runConfig, state *runState) bool {
	if !send(event.Thinking("🧭 Plan-Execute 深度研究开始\n")) {
		return false
	}

	shouldContinue, ok := a.clarifyRequirement(ctx, send, logger, input, cfg, state)
	if !ok {
		return false
	}
	if !shouldContinue {
		return true
	}
	if !a.generateResearchTopic(ctx, send, logger, input, cfg, state) {
		return false
	}

	for round := 1; round <= cfg.maxRounds; round++ {
		state.round = round
		roundStartedAt := time.Now()
		logger.Info("\U0001F501 Plan-Execute 轮次开始",
			"conversation_id", input.ConversationID,
			"round", round,
			"completed_task_count", len(state.results),
			"critique_feedback", state.critiqueFeedback,
		)

		plan, ok := a.plan(ctx, logger, input, cfg, state)
		if !ok {
			_ = send(event.Error("PLAN_FAILED", "规划阶段失败", "planner returned no valid plan"))
			return false
		}
		if len(plan) == 0 || planDone(plan) {
			logger.Info("\U0001F3AF 规划阶段判定信息充分，进入最终总结",
				"conversation_id", input.ConversationID,
				"round", round,
			)
			break
		}
		if len(plan) > a.maxTasksPerRound {
			logger.Warn("\U000026A0 Plan-Execute 单轮任务过多，已按上限截断",
				"conversation_id", input.ConversationID,
				"round", round,
				"task_count", len(plan),
				"max_tasks_per_round", a.maxTasksPerRound,
			)
			plan = plan[:a.maxTasksPerRound]
		}
		if !send(event.Thinking(fmt.Sprintf("\n✅ 第 %d 轮执行计划已生成，共 %d 个任务\n", round, len(plan)))) {
			return false
		}
		if !send(event.Thinking(renderPlanThinking(plan))) {
			return false
		}

		if !a.executePlan(ctx, send, logger, input, round, plan, state) {
			return false
		}

		if !send(event.Thinking("\n🔍 正在评估当前研究结果...\n")) {
			return false
		}
		passed, feedback, ok := a.critique(ctx, logger, input, cfg, state)
		if !ok {
			_ = send(event.Error("CRITIQUE_FAILED", "评审阶段失败", "critique returned no valid result"))
			return false
		}
		state.critiquePassed = passed
		state.critiqueFeedback = feedback
		logger.Info("\U0001F50D Plan-Execute 轮次评审完成",
			"conversation_id", input.ConversationID,
			"round", round,
			"passed", passed,
			"feedback", feedback,
			"elapsed_ms", elapsedMillis(roundStartedAt),
		)
		if passed {
			if !send(event.Thinking("\n✅ 研究结果评估通过，准备生成最终报告\n")) {
				return false
			}
			break
		}
		if !send(event.Thinking(fmt.Sprintf("\n⚠️ 研究结果评估未通过，原因分析：%s\n--- 准备进入下一轮迭代 ---\n", feedback))) {
			return false
		}
	}

	if !send(event.Thinking("\n📝 正在生成最终研究报告...\n\n")) {
		return false
	}
	if !a.synthesize(ctx, send, logger, input, cfg, state) {
		return false
	}
	if len(state.references) > 0 {
		if !send(event.Reference(tool.MustJSON(state.references), len(state.references))) {
			return false
		}
	}
	_ = send(event.Complete())
	return true
}

func (a *Agent) clarifyRequirement(ctx context.Context, send func(event.Event) bool, logger *slog.Logger, input agent.Input, cfg runConfig, state *runState) (bool, bool) {
	startedAt := time.Now()
	if !send(event.Thinking("\n🔍 正在分析您的需求...\n")) {
		return false, false
	}
	messages := []model.Message{
		{Role: model.RoleSystem, Content: clarifyPrompt(time.Now())},
		{Role: model.RoleUser, Content: input.Query},
	}
	result := contextx.NewBuilder(a.contextPolicy).Build(
		contextx.Section{Name: contextx.SectionSystem, Messages: messages[:1]},
		contextx.Section{Name: contextx.SectionHistory, Messages: state.historyMessages},
		contextx.Section{Name: contextx.SectionCurrent, Messages: messages[1:]},
	)
	logger.Info("\U0001F50D Plan-Execute 需求澄清开始",
		"conversation_id", input.ConversationID,
		"message_count", len(result.Messages),
		"history_message_count", len(state.historyMessages),
		"input_token_estimate", result.Summary.InputTokenEstimate,
	)

	response, ok := a.streamStage(ctx, send, logger, input, cfg, "clarify", result.Messages)
	if !ok {
		_ = send(event.Error("CLARIFY_FAILED", "需求澄清阶段失败", "clarification stream failed"))
		return false, false
	}
	response = strings.TrimSpace(response)
	if !send(event.Thinking("\n✅ 需求分析完成\n")) {
		return false, false
	}

	needsMoreInfo := strings.Contains(response, "【需要补充信息】")
	logger.Info("\U00002705 Plan-Execute 需求澄清完成",
		"conversation_id", input.ConversationID,
		"needs_more_info", needsMoreInfo,
		"response_chars", len(response),
		"elapsed_ms", elapsedMillis(startedAt),
	)
	if needsMoreInfo {
		pauseMessage := "⏸【暂停深入研究】" + strings.TrimSpace(strings.ReplaceAll(response, "【需要补充信息】", ""))
		if !send(event.Text(pauseMessage)) {
			return false, false
		}
		_ = send(event.Complete())
		return false, true
	}
	if !send(event.Thinking("✅ 信息充足，准备生成研究主题\n")) {
		return false, false
	}
	return true, true
}

func (a *Agent) generateResearchTopic(ctx context.Context, send func(event.Event) bool, logger *slog.Logger, input agent.Input, cfg runConfig, state *runState) bool {
	startedAt := time.Now()
	if !send(event.Thinking("📝 正在生成研究主题...\n")) {
		return false
	}
	messages := []model.Message{
		{Role: model.RoleSystem, Content: researchTopicPrompt(time.Now())},
		{Role: model.RoleUser, Content: "<original_question>" + input.Query + "</original_question>"},
	}
	result := contextx.NewBuilder(a.contextPolicy).Build(
		contextx.Section{Name: contextx.SectionSystem, Messages: messages[:1]},
		contextx.Section{Name: contextx.SectionHistory, Messages: state.historyMessages},
		contextx.Section{Name: contextx.SectionCurrent, Messages: messages[1:]},
	)
	logger.Info("\U0001F4DD Plan-Execute 研究主题生成开始",
		"conversation_id", input.ConversationID,
		"message_count", len(result.Messages),
		"input_token_estimate", result.Summary.InputTokenEstimate,
	)

	topic, ok := a.streamStage(ctx, send, logger, input, cfg, "topic", result.Messages)
	if !ok {
		_ = send(event.Error("TOPIC_FAILED", "研究主题生成阶段失败", "topic generation stream failed"))
		return false
	}
	topic = strings.TrimSpace(topic)
	if topic == "" {
		topic = input.Query
	}
	state.refinedResearchTopic = topic
	logger.Info("\U00002705 Plan-Execute 研究主题已生成",
		"conversation_id", input.ConversationID,
		"topic_chars", len(topic),
		"topic_preview", previewText(topic, 120),
		"elapsed_ms", elapsedMillis(startedAt),
	)
	return send(event.Thinking("\n✅ 研究主题已生成\n\n"))
}

func (a *Agent) streamStage(ctx context.Context, send func(event.Event) bool, logger *slog.Logger, input agent.Input, cfg runConfig, stage string, messages []model.Message) (string, bool) {
	startedAt := time.Now()
	stream, err := a.model.Stream(ctx, model.Request{
		Messages:    messages,
		Temperature: cfg.temperature,
		RequestID:   input.RequestID,
	})
	if err != nil {
		logger.Error("\U0000274C Plan-Execute 阶段模型流启动失败",
			"conversation_id", input.ConversationID,
			"stage", stage,
			"elapsed_ms", elapsedMillis(startedAt),
			"error", err,
		)
		return "", false
	}

	var response strings.Builder
	inThink := false
	chunkCount := 0
	thinkingChars := 0
	textChars := 0
	for chunk := range stream {
		if chunk.Err != nil {
			logger.Error("\U0000274C Plan-Execute 阶段模型流读取失败",
				"conversation_id", input.ConversationID,
				"stage", stage,
				"chunk_count", chunkCount,
				"text_chars", textChars,
				"thinking_chars", thinkingChars,
				"elapsed_ms", elapsedMillis(startedAt),
				"error", chunk.Err,
			)
			return response.String(), false
		}
		if chunk.Done {
			break
		}
		chunkCount++
		for _, segment := range parseThinkSegments(chunk.Content, &inThink) {
			if segment.content == "" {
				continue
			}
			if segment.thinking {
				thinkingChars += len(segment.content)
				if !send(event.Thinking(segment.content)) {
					return response.String(), false
				}
				continue
			}
			textChars += len(segment.content)
			response.WriteString(segment.content)
			if !send(event.Thinking(segment.content)) {
				return response.String(), false
			}
		}
	}
	logger.Info("\U00002705 Plan-Execute 阶段模型流读取完成",
		"conversation_id", input.ConversationID,
		"stage", stage,
		"chunk_count", chunkCount,
		"text_chars", textChars,
		"thinking_chars", thinkingChars,
		"elapsed_ms", elapsedMillis(startedAt),
	)
	return response.String(), true
}

func (a *Agent) plan(ctx context.Context, logger *slog.Logger, input agent.Input, cfg runConfig, state *runState) ([]Task, bool) {
	startedAt := time.Now()
	messages := []model.Message{
		{Role: model.RoleSystem, Content: planPrompt(time.Now())},
		{Role: model.RoleUser, Content: planUserPrompt(input.Query, state, a.tools)},
	}
	result := contextx.NewBuilder(a.contextPolicy).Build(
		contextx.Section{Name: contextx.SectionSystem, Messages: messages[:1]},
		contextx.Section{Name: contextx.SectionCurrent, Messages: messages[1:]},
	)
	resp, err := a.model.Generate(ctx, model.Request{
		Messages:    result.Messages,
		Temperature: cfg.temperature,
		RequestID:   input.RequestID,
	})
	if err != nil {
		logger.Error("\U0000274C Plan-Execute 规划模型调用失败",
			"conversation_id", input.ConversationID,
			"elapsed_ms", elapsedMillis(startedAt),
			"error", err,
		)
		return nil, false
	}
	tasks, err := parseTasks(resp.Content)
	if err != nil {
		logger.Error("\U0000274C Plan-Execute 规划 JSON 解析失败",
			"conversation_id", input.ConversationID,
			"content_chars", len(resp.Content),
			"content_preview", previewText(resp.Content, 160),
			"elapsed_ms", elapsedMillis(startedAt),
			"error", err,
		)
		return nil, false
	}
	sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].Order < tasks[j].Order })
	logger.Info("\U0001F4CB Plan-Execute 规划完成",
		"conversation_id", input.ConversationID,
		"task_count", len(tasks),
		"input_token_estimate", result.Summary.InputTokenEstimate,
		"elapsed_ms", elapsedMillis(startedAt),
	)
	return tasks, true
}

func (a *Agent) executePlan(ctx context.Context, send func(event.Event) bool, logger *slog.Logger, input agent.Input, round int, tasks []Task, state *runState) bool {
	for _, current := range tasks {
		if current.ID == "" {
			continue
		}
		if !a.executeTask(ctx, send, logger, input, round, current, state) {
			return false
		}
	}
	return true
}

func (a *Agent) executeTask(ctx context.Context, send func(event.Event) bool, logger *slog.Logger, input agent.Input, round int, task Task, state *runState) bool {
	startedAt := time.Now()
	query := task.Instruction
	args := map[string]any{"query": query}
	argsJSON := tool.MustJSON(args)

	if !send(event.Thinking(fmt.Sprintf("⚙️ 正在执行任务 %s：%s\n", task.ID, task.Instruction))) {
		return false
	}
	if !send(event.ToolStart(a.executionToolName, task.ID, argsJSON)) {
		return false
	}
	logger.Info("\U0001F527 Plan-Execute 工具任务开始",
		"conversation_id", input.ConversationID,
		"round", round,
		"task_id", task.ID,
		"order", task.Order,
		"tool", a.executionToolName,
		"instruction", task.Instruction,
	)

	result, err := a.tools.Execute(ctx, a.executionToolName, args)
	resultText := ""
	if err != nil {
		resultText = `{ "error": "` + escapeForJSON(err.Error()) + `" }`
		logger.Error("\U0000274C Plan-Execute 工具任务失败",
			"conversation_id", input.ConversationID,
			"round", round,
			"task_id", task.ID,
			"elapsed_ms", elapsedMillis(startedAt),
			"error", err,
		)
	} else {
		a.collectSearchResults(result, state)
		resultText = result.Content
		if result.Data != nil {
			resultText = tool.MustJSON(result.Data)
		}
		logger.Info("\U00002705 Plan-Execute 工具任务完成",
			"conversation_id", input.ConversationID,
			"round", round,
			"task_id", task.ID,
			"result_chars", len(resultText),
			"reference_count", len(state.references),
			"elapsed_ms", elapsedMillis(startedAt),
		)
	}
	if !send(event.ToolEnd(a.executionToolName, task.ID, resultText)) {
		return false
	}
	if err == nil {
		if !send(event.Thinking("执行结果: " + previewText(resultText, 1000) + "\n\n")) {
			return false
		}
	}
	state.results = append(state.results, TaskResult{
		Task:   task,
		Tool:   a.executionToolName,
		Query:  query,
		Result: resultText,
		Error:  errorString(err),
	})
	return true
}

func (a *Agent) critique(ctx context.Context, logger *slog.Logger, input agent.Input, cfg runConfig, state *runState) (bool, string, bool) {
	startedAt := time.Now()
	messages := []model.Message{
		{Role: model.RoleSystem, Content: critiquePrompt(time.Now())},
		{Role: model.RoleUser, Content: state.researchContext()},
	}
	resp, err := a.model.Generate(ctx, model.Request{
		Messages:    messages,
		Temperature: cfg.temperature,
		RequestID:   input.RequestID,
	})
	if err != nil {
		logger.Error("\U0000274C Plan-Execute 评审模型调用失败",
			"conversation_id", input.ConversationID,
			"elapsed_ms", elapsedMillis(startedAt),
			"error", err,
		)
		return false, "", false
	}
	critique, err := parseCritique(resp.Content)
	if err != nil {
		logger.Error("\U0000274C Plan-Execute 评审 JSON 解析失败",
			"conversation_id", input.ConversationID,
			"content_preview", previewText(resp.Content, 160),
			"elapsed_ms", elapsedMillis(startedAt),
			"error", err,
		)
		return false, "", false
	}
	return critique.Passed, critique.Feedback, true
}

func (a *Agent) synthesize(ctx context.Context, send func(event.Event) bool, logger *slog.Logger, input agent.Input, cfg runConfig, state *runState) bool {
	startedAt := time.Now()
	logger.Info("\U0001F9FE Plan-Execute 开始最终总结",
		"conversation_id", input.ConversationID,
		"task_result_count", len(state.results),
	)
	stream, err := a.model.Stream(ctx, model.Request{
		Messages: []model.Message{
			{Role: model.RoleSystem, Content: summarizePrompt(time.Now())},
			{Role: model.RoleUser, Content: state.researchContext()},
		},
		Temperature: cfg.temperature,
		RequestID:   input.RequestID,
	})
	if err != nil {
		logger.Error("\U0000274C Plan-Execute 总结模型流启动失败",
			"conversation_id", input.ConversationID,
			"elapsed_ms", elapsedMillis(startedAt),
			"error", err,
		)
		_ = send(event.Error("SUMMARY_FAILED", "总结模型流启动失败", err.Error()))
		return false
	}

	chunkCount := 0
	textChars := 0
	thinkingChars := 0
	inThink := false
	for chunk := range stream {
		if chunk.Err != nil {
			logger.Error("\U0000274C Plan-Execute 总结模型流读取失败",
				"conversation_id", input.ConversationID,
				"chunk_count", chunkCount,
				"text_chars", textChars,
				"thinking_chars", thinkingChars,
				"elapsed_ms", elapsedMillis(startedAt),
				"error", chunk.Err,
			)
			_ = send(event.Error("SUMMARY_FAILED", "总结模型流读取失败", chunk.Err.Error()))
			return false
		}
		if chunk.Done {
			break
		}
		if chunk.Content == "" {
			continue
		}
		chunkCount++
		for _, segment := range parseThinkSegments(chunk.Content, &inThink) {
			if segment.content == "" {
				continue
			}
			if segment.thinking {
				thinkingChars += len(segment.content)
				if !send(event.Thinking(segment.content)) {
					return false
				}
				continue
			}
			textChars += len(segment.content)
			if !send(event.Text(segment.content)) {
				return false
			}
		}
	}
	logger.Info("\U00002705 Plan-Execute 最终总结完成",
		"conversation_id", input.ConversationID,
		"chunk_count", chunkCount,
		"text_chars", textChars,
		"thinking_chars", thinkingChars,
		"elapsed_ms", elapsedMillis(startedAt),
	)
	return true
}

func (a *Agent) collectSearchResults(result tool.Result, state *runState) {
	if result.Data == nil {
		return
	}
	raw, ok := result.Data["results"]
	if !ok {
		return
	}
	state.references = append(state.references, searchResultsFromAny(raw)...)
}

func (a *Agent) loadHistory(ctx context.Context, logger *slog.Logger, conversationID string) []model.Message {
	if strings.TrimSpace(conversationID) == "" || a.memory == nil || a.maxHistoryRecords <= 0 {
		return nil
	}

	startedAt := time.Now()
	records, err := a.memory.FindRecent(ctx, conversationID, a.maxHistoryRecords)
	if err != nil {
		logger.Warn("\U000026A0 Plan-Execute 会话历史加载失败，将不携带历史继续执行",
			"conversation_id", conversationID,
			"max_history_records", a.maxHistoryRecords,
			"elapsed_ms", elapsedMillis(startedAt),
			"error", err,
		)
		return nil
	}
	historyMessages := memory.HistoryMessages(records)
	logger.Info("\U0001F4DD Plan-Execute 会话历史已读取",
		"conversation_id", conversationID,
		"record_count", len(records),
		"history_message_count", len(historyMessages),
		"max_history_records", a.maxHistoryRecords,
		"elapsed_ms", elapsedMillis(startedAt),
	)
	return historyMessages
}

func (a *Agent) saveRunQuestion(ctx context.Context, logger *slog.Logger, input agent.Input, record *runRecord) {
	if a.memory == nil || strings.TrimSpace(input.ConversationID) == "" || record == nil {
		return
	}

	startedAt := time.Now()
	saved, err := a.memory.SaveQuestion(ctx, memory.SaveQuestionRequest{
		SessionID: input.ConversationID,
		AgentType: "plan-execute",
		Question:  input.Query,
	})
	if err != nil {
		logger.Warn("\U000026A0 Plan-Execute 会话问题保存失败",
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
	logger.Info("\U0001F4BE Plan-Execute 会话问题已保存",
		"conversation_id", input.ConversationID,
		"session_record_id", saved.ID,
		"query_chars", len(input.Query),
		"elapsed_ms", elapsedMillis(startedAt),
	)
}

func (a *Agent) persistRun(ctx context.Context, logger *slog.Logger, input agent.Input, record *runRecord) {
	if a.memory == nil || strings.TrimSpace(input.ConversationID) == "" || record == nil || record.sessionRecordID <= 0 {
		return
	}
	if strings.TrimSpace(record.answer.String()) == "" && strings.TrimSpace(record.thinking.String()) == "" && ctx.Err() != nil {
		logger.Warn("\U000026A0 Plan-Execute 请求取消且没有可保存内容，跳过会话回答更新",
			"conversation_id", input.ConversationID,
			"session_record_id", record.sessionRecordID,
			"error", ctx.Err(),
		)
		return
	}

	startedAt := time.Now()
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
		logger.Warn("\U000026A0 Plan-Execute 会话结果保存失败",
			"conversation_id", input.ConversationID,
			"session_record_id", record.sessionRecordID,
			"answer_chars", record.answer.Len(),
			"thinking_chars", record.thinking.Len(),
			"elapsed_ms", elapsedMillis(startedAt),
			"error", err,
		)
		return
	}

	logger.Info("\U0001F4BE Plan-Execute 会话结果已保存",
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

func renderPlanThinking(tasks []Task) string {
	if len(tasks) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n📋 执行计划表：\n")
	for _, task := range tasks {
		if strings.TrimSpace(task.ID) == "" {
			continue
		}
		b.WriteString("  🟠 ")
		b.WriteString(task.Instruction)
		b.WriteString("\n")
	}
	return b.String()
}

var _ agent.Agent = (*Agent)(nil)
