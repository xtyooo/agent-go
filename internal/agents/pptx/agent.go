package pptx

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"agentG/internal/runtime/agent"
	"agentG/internal/runtime/event"
	"agentG/internal/runtime/memory"
	"agentG/internal/runtime/model"
	"agentG/internal/runtime/tool"
)

const (
	defaultTemperature = 0.4
	defaultSearchTool  = "web_search"
	maxPPTSearchChars  = 360
)

type Agent struct {
	model             model.Model
	tools             *tool.Registry
	store             Store
	memory            memory.Store
	logger            *slog.Logger
	templates         []Template
	searchToolName    string
	maxHistoryRecords int
}

type Option func(*Agent)

func New(model model.Model, tools *tool.Registry, logger *slog.Logger, opts ...Option) *Agent {
	if logger == nil {
		logger = slog.Default()
	}
	a := &Agent{
		model:             model,
		tools:             tools,
		store:             NewMemoryStore(),
		memory:            memory.NoopStore{},
		logger:            logger,
		templates:         defaultTemplates(),
		searchToolName:    defaultSearchTool,
		maxHistoryRecords: 30,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

func WithStore(store Store) Option {
	return func(a *Agent) {
		if store != nil {
			a.store = store
		}
	}
}

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

func WithSearchTool(name string) Option {
	return func(a *Agent) {
		if strings.TrimSpace(name) != "" {
			a.searchToolName = strings.TrimSpace(name)
		}
	}
}

func (a *Agent) Store() Store {
	if a == nil {
		return nil
	}
	return a.store
}

func (a *Agent) Run(ctx context.Context, input agent.Input) (<-chan event.Event, error) {
	events := make(chan event.Event, 16)
	go a.runAsync(ctx, input, events)
	return events, nil
}

func (a *Agent) runAsync(ctx context.Context, input agent.Input, events chan event.Event) {
	defer close(events)

	startedAt := time.Now()
	logger := a.logger
	if input.RequestID != "" {
		logger = logger.With("request_id", input.RequestID)
	}

	record := newRunRecord(input.ConversationID, input.Query, startedAt)
	defer a.persistRun(context.Background(), logger, input, record)

	send := event.NewSender(event.SenderConfig{
		Ctx: ctx,
		Out: events,
		OnCancel: func(evt event.Event, err error) {
			logger.Warn("🛑 PPTBuilder 事件发送被取消",
				"conversation_id", input.ConversationID,
				"event_type", evt.Type,
				"elapsed_ms", elapsedMillis(startedAt),
				"error", err,
			)
		},
		AfterSend: func(evt event.Event) {
			record.capture(evt, elapsedMillis(startedAt))
		},
	})

	logger.Info("📊 PPTBuilder Agent 已启动",
		"conversation_id", input.ConversationID,
		"query", input.Query,
		"query_chars", len(input.Query),
	)

	a.saveRunQuestion(ctx, logger, input, record)

	intent, inst, err := a.resolveIntent(ctx, input)
	if err != nil {
		logger.Error("❌ PPTBuilder 意图识别失败",
			"conversation_id", input.ConversationID,
			"elapsed_ms", elapsedMillis(startedAt),
			"error", err,
		)
		_ = send.Send(event.Error("PPT_INTENT_FAILED", "PPT 意图识别失败", err.Error()))
		return
	}

	logger.Info("🧭 PPTBuilder 意图识别完成",
		"conversation_id", input.ConversationID,
		"intent", intent,
		"ppt_inst_id", inst.ID,
		"status", inst.Status,
	)

	if !a.execute(ctx, send.Send, logger, input, record, intent, inst) {
		return
	}

	logger.Info("🏁 PPTBuilder Agent 已完成",
		"conversation_id", input.ConversationID,
		"ppt_inst_id", inst.ID,
		"elapsed_ms", elapsedMillis(startedAt),
	)
}

func (a *Agent) resolveIntent(ctx context.Context, input agent.Input) (Intent, Instance, error) {
	latest, ok, err := a.store.Latest(ctx, input.ConversationID)
	if err != nil {
		return "", Instance{}, err
	}
	if !ok {
		inst, err := a.store.Create(ctx, input.ConversationID, input.Query)
		return IntentCreate, inst, err
	}

	query := strings.ToLower(strings.TrimSpace(input.Query))
	if shouldResume(latest, query) {
		latest, err = a.store.Update(ctx, latest.ID, func(inst *Instance) {
			inst.ErrorMsg = ""
		})
		return IntentResume, latest, err
	}
	if latest.Status == StatusSuccess && looksLikeModify(query) {
		return IntentModify, latest, nil
	}

	inst, err := a.store.Create(ctx, input.ConversationID, input.Query)
	return IntentCreate, inst, err
}

func shouldResume(inst Instance, query string) bool {
	if strings.TrimSpace(inst.ErrorMsg) != "" {
		return true
	}
	for _, keyword := range []string{"继续", "重试", "resume", "retry", "继续执行", "继续生成"} {
		if strings.Contains(query, keyword) {
			return true
		}
	}
	if inst.Status != StatusSuccess && inst.Status != StatusInit && inst.Status != "" {
		for _, keyword := range []string{"新建", "重新", "重新生成", "new", "create new"} {
			if strings.Contains(query, keyword) {
				return false
			}
		}
		return true
	}
	return false
}

func looksLikeModify(query string) bool {
	for _, keyword := range []string{"修改", "调整", "优化", "改一下", "更新", "替换", "modify", "update"} {
		if strings.Contains(query, keyword) {
			return true
		}
	}
	return false
}

func (a *Agent) execute(ctx context.Context, send func(event.Event) bool, logger *slog.Logger, input agent.Input, record *runRecord, intent Intent, inst Instance) bool {
	if intent == IntentModify {
		return a.executeModify(ctx, send, logger, input, record, inst)
	}
	if intent == IntentResume {
		if !send(event.Thinking(fmt.Sprintf("🔁 正在从状态 %s（%s）继续执行 PPT 生成...\n", inst.Status, statusDesc(inst.Status)))) {
			return false
		}
	} else if !send(event.Thinking("📊 开始创建新的 PPT...\n")) {
		return false
	}

	return a.continueStateMachine(ctx, send, logger, input, record, inst)
}

func (a *Agent) continueStateMachine(ctx context.Context, send func(event.Event) bool, logger *slog.Logger, input agent.Input, record *runRecord, inst Instance) bool {
	for {
		latest, ok, err := a.store.Get(ctx, inst.ID)
		if err != nil || !ok {
			_ = send(event.Error("PPT_INSTANCE_NOT_FOUND", "PPT 实例不存在", errorText(err)))
			return false
		}
		inst = latest
		if err := ctx.Err(); err != nil {
			logger.Warn("🛑 PPTBuilder 状态机收到取消信号",
				"conversation_id", input.ConversationID,
				"ppt_inst_id", inst.ID,
				"status", inst.Status,
				"error", err,
			)
			return false
		}

		switch inst.Status {
		case "", StatusInit, StatusRequirement:
			var ok bool
			inst, ok = a.requirement(ctx, send, logger, input, inst)
			if !ok {
				return false
			}
		case StatusSearch:
			var ok bool
			inst, ok = a.search(ctx, send, logger, input, record, inst)
			if !ok {
				return false
			}
		case StatusTemplate:
			var ok bool
			inst, ok = a.template(ctx, send, logger, input, inst)
			if !ok {
				return false
			}
		case StatusOutline:
			var ok bool
			inst, ok = a.outline(ctx, send, logger, input, inst)
			if !ok {
				return false
			}
		case StatusSchema:
			var ok bool
			inst, ok = a.schema(ctx, send, logger, input, inst, "")
			if !ok {
				return false
			}
		case StatusRender:
			var ok bool
			inst, ok = a.render(ctx, send, logger, input, inst)
			if !ok {
				return false
			}
		case StatusSuccess:
			return a.success(ctx, send, logger, input, record, inst, false)
		case StatusFailed:
			return a.failed(send, inst)
		default:
			_ = send(event.Error("PPT_UNKNOWN_STATUS", "PPT 状态异常", string(inst.Status)))
			return false
		}
	}
}

func (a *Agent) requirement(ctx context.Context, send func(event.Event) bool, logger *slog.Logger, input agent.Input, inst Instance) (Instance, bool) {
	startedAt := time.Now()
	if !send(event.Thinking("🔎 正在分析您的 PPT 需求...\n")) {
		return inst, false
	}

	resp, err := a.model.Generate(ctx, model.Request{
		Messages: []model.Message{
			{Role: model.RoleSystem, Content: requirementPrompt(time.Now())},
			{Role: model.RoleUser, Content: "<question>" + input.Query + "</question>"},
		},
		Temperature: defaultTemperature,
		RequestID:   input.RequestID,
	})
	if err != nil {
		return a.fail(ctx, send, inst, StatusRequirement, "需求分析失败: "+err.Error())
	}

	requirement := stripThinkTags(resp.Content)
	if strings.Contains(requirement, "【暂停生成PPT】") {
		updated, _ := a.store.Update(ctx, inst.ID, func(current *Instance) {
			current.Status = StatusRequirement
			current.Requirement = requirement
			current.ErrorMsg = "需要补充信息"
		})
		_ = send(event.Text(strings.TrimSpace(strings.ReplaceAll(requirement, "【暂停生成PPT】", ""))))
		_ = send(event.Complete())
		logger.Info("⏸ PPTBuilder 需求信息不足，暂停生成",
			"conversation_id", input.ConversationID,
			"ppt_inst_id", inst.ID,
			"requirement_chars", len(requirement),
			"elapsed_ms", elapsedMillis(startedAt),
		)
		return updated, false
	}

	updated, err := a.store.Update(ctx, inst.ID, func(current *Instance) {
		current.Status = StatusSearch
		current.Requirement = strings.TrimSpace(strings.ReplaceAll(requirement, "【开始生成PPT】", ""))
		current.ErrorMsg = ""
	})
	if err != nil {
		_ = send(event.Error("PPT_STORE_FAILED", "PPT 需求保存失败", err.Error()))
		return inst, false
	}
	logger.Info("✅ PPTBuilder 需求分析完成",
		"conversation_id", input.ConversationID,
		"ppt_inst_id", inst.ID,
		"requirement_chars", len(updated.Requirement),
		"next_status", updated.Status,
		"elapsed_ms", elapsedMillis(startedAt),
	)
	return updated, send(event.Thinking("✅ 需求已确认，开始收集相关信息\n"))
}

func (a *Agent) search(ctx context.Context, send func(event.Event) bool, logger *slog.Logger, input agent.Input, record *runRecord, inst Instance) (Instance, bool) {
	startedAt := time.Now()
	if !send(event.Thinking("🌐 正在收集 PPT 相关信息...\n")) {
		return inst, false
	}

	searchInfo := "未执行联网搜索，使用用户需求直接生成。"
	if a.tools != nil {
		searchQuery := buildPPTSearchQuery(inst.Requirement, input.Query)
		args := map[string]any{"query": searchQuery}
		if send(event.ToolStart(a.searchToolName, fmt.Sprintf("ppt-search-%d", inst.ID), tool.MustJSON(args))) {
			logger.Info("🔎 PPTBuilder 搜索词已生成",
				"conversation_id", input.ConversationID,
				"ppt_inst_id", inst.ID,
				"requirement_chars", len([]rune(inst.Requirement)),
				"search_query", searchQuery,
				"search_query_chars", len([]rune(searchQuery)),
				"truncated", len([]rune(strings.TrimSpace(inst.Requirement))) > len([]rune(searchQuery)),
			)
			result, err := a.tools.Execute(ctx, a.searchToolName, args)
			if err != nil {
				searchInfo = "搜索失败，降级使用用户需求生成：" + err.Error()
				_ = send(event.ToolEnd(a.searchToolName, fmt.Sprintf("ppt-search-%d", inst.ID), tool.MustJSON(map[string]any{"error": err.Error()})))
				logger.Warn("⚠ PPTBuilder 搜索失败，降级继续",
					"conversation_id", input.ConversationID,
					"ppt_inst_id", inst.ID,
					"tool", a.searchToolName,
					"elapsed_ms", elapsedMillis(startedAt),
					"error", err,
				)
			} else {
				record.addTool(a.searchToolName)
				searchInfo = result.Content
				if result.Data != nil {
					searchInfo = tool.MustJSON(result.Data)
				}
				_ = send(event.ToolEnd(a.searchToolName, fmt.Sprintf("ppt-search-%d", inst.ID), searchInfo))
			}
		}
	}

	updated, err := a.store.Update(ctx, inst.ID, func(current *Instance) {
		current.Status = StatusTemplate
		current.SearchInfo = searchInfo
		current.ErrorMsg = ""
	})
	if err != nil {
		_ = send(event.Error("PPT_STORE_FAILED", "PPT 搜索结果保存失败", err.Error()))
		return inst, false
	}
	logger.Info("✅ PPTBuilder 信息收集完成",
		"conversation_id", input.ConversationID,
		"ppt_inst_id", inst.ID,
		"search_chars", len(searchInfo),
		"next_status", updated.Status,
		"elapsed_ms", elapsedMillis(startedAt),
	)
	return updated, send(event.Thinking("✅ 相关信息收集完成，开始选择模板\n"))
}

func (a *Agent) template(ctx context.Context, send func(event.Event) bool, logger *slog.Logger, input agent.Input, inst Instance) (Instance, bool) {
	startedAt := time.Now()
	if !send(event.Thinking("🎨 正在选择 PPT 模板...\n")) {
		return inst, false
	}
	fallback := a.templates[0]
	resp, err := a.model.Generate(ctx, model.Request{
		Messages:    []model.Message{{Role: model.RoleUser, Content: templateSelectionPrompt(inst.Requirement, a.templates)}},
		Temperature: defaultTemperature,
		RequestID:   input.RequestID,
	})
	templateCode, reason := fallback.Code, "默认模板"
	if err == nil {
		templateCode, reason = parseTemplateSelection(resp.Content, fallback)
	} else {
		logger.Warn("⚠ PPTBuilder 模板选择模型调用失败，使用默认模板",
			"conversation_id", input.ConversationID,
			"ppt_inst_id", inst.ID,
			"error", err,
		)
	}
	tmpl := a.findTemplate(templateCode)
	if tmpl.Code == "" {
		tmpl = fallback
	}

	updated, err := a.store.Update(ctx, inst.ID, func(current *Instance) {
		current.Status = StatusOutline
		current.TemplateCode = tmpl.Code
		current.ErrorMsg = ""
	})
	if err != nil {
		_ = send(event.Error("PPT_STORE_FAILED", "PPT 模板保存失败", err.Error()))
		return inst, false
	}
	logger.Info("✅ PPTBuilder 模板选择完成",
		"conversation_id", input.ConversationID,
		"ppt_inst_id", inst.ID,
		"template_code", tmpl.Code,
		"reason", reason,
		"next_status", updated.Status,
		"elapsed_ms", elapsedMillis(startedAt),
	)
	return updated, send(event.Thinking(fmt.Sprintf("✅ 已选择模板：%s，开始生成大纲\n", tmpl.Name)))
}

func (a *Agent) outline(ctx context.Context, send func(event.Event) bool, logger *slog.Logger, input agent.Input, inst Instance) (Instance, bool) {
	startedAt := time.Now()
	if !send(event.Thinking("🧱 正在生成 PPT 大纲...\n")) {
		return inst, false
	}
	tmpl := a.findTemplate(inst.TemplateCode)
	resp, err := a.model.Generate(ctx, model.Request{
		Messages:    []model.Message{{Role: model.RoleUser, Content: outlinePrompt(inst.Requirement, tmpl.Schema, tmpl.Name, inst.SearchInfo)}},
		Temperature: defaultTemperature,
		RequestID:   input.RequestID,
	})
	if err != nil {
		return a.fail(ctx, send, inst, StatusOutline, "大纲生成失败: "+err.Error())
	}

	updated, err := a.store.Update(ctx, inst.ID, func(current *Instance) {
		current.Status = StatusSchema
		current.Outline = stripThinkTags(resp.Content)
		current.ErrorMsg = ""
	})
	if err != nil {
		_ = send(event.Error("PPT_STORE_FAILED", "PPT 大纲保存失败", err.Error()))
		return inst, false
	}
	logger.Info("✅ PPTBuilder 大纲生成完成",
		"conversation_id", input.ConversationID,
		"ppt_inst_id", inst.ID,
		"outline_chars", len(updated.Outline),
		"next_status", updated.Status,
		"elapsed_ms", elapsedMillis(startedAt),
	)
	return updated, send(event.Thinking("✅ 大纲生成完成，开始设计 PPT 详细内容\n"))
}

func (a *Agent) schema(ctx context.Context, send func(event.Event) bool, logger *slog.Logger, input agent.Input, inst Instance, modifyPrompt string) (Instance, bool) {
	startedAt := time.Now()
	if !send(event.Thinking("🧩 正在生成 PPT Schema...\n")) {
		return inst, false
	}
	tmpl := a.findTemplate(inst.TemplateCode)
	prompt := schemaPrompt(tmpl.Schema, inst.Outline)
	if modifyPrompt != "" {
		prompt = modifyPrompt
	}
	resp, err := a.model.Generate(ctx, model.Request{
		Messages:    []model.Message{{Role: model.RoleUser, Content: prompt}},
		Temperature: defaultTemperature,
		RequestID:   input.RequestID,
	})
	if err != nil {
		return a.fail(ctx, send, inst, StatusSchema, "Schema 生成失败: "+err.Error())
	}
	schema := extractJSON(stripThinkTags(resp.Content))
	if !strings.HasPrefix(schema, "{") {
		schema = fallbackSchema(inst, tmpl)
	}

	updated, err := a.store.Update(ctx, inst.ID, func(current *Instance) {
		current.Status = StatusRender
		current.PPTSchema = schema
		current.ErrorMsg = ""
	})
	if err != nil {
		_ = send(event.Error("PPT_STORE_FAILED", "PPT Schema 保存失败", err.Error()))
		return inst, false
	}
	logger.Info("✅ PPTBuilder Schema 生成完成",
		"conversation_id", input.ConversationID,
		"ppt_inst_id", inst.ID,
		"schema_chars", len(updated.PPTSchema),
		"next_status", updated.Status,
		"elapsed_ms", elapsedMillis(startedAt),
	)
	return updated, send(event.Thinking("✅ PPT 内容设计完成，开始渲染 PPT\n"))
}

func (a *Agent) render(ctx context.Context, send func(event.Event) bool, logger *slog.Logger, input agent.Input, inst Instance) (Instance, bool) {
	startedAt := time.Now()
	if !send(event.Thinking("🖨 正在渲染 PPT（当前 Go 版使用 mock 渲染产物）...\n")) {
		return inst, false
	}
	fileURL := fmt.Sprintf("mock://ppt/%s/%d.pptx", input.ConversationID, inst.ID)
	updated, err := a.store.Update(ctx, inst.ID, func(current *Instance) {
		current.Status = StatusSuccess
		current.FileURL = fileURL
		current.ErrorMsg = ""
	})
	if err != nil {
		_ = send(event.Error("PPT_STORE_FAILED", "PPT 渲染结果保存失败", err.Error()))
		return inst, false
	}
	logger.Info("✅ PPTBuilder 渲染完成",
		"conversation_id", input.ConversationID,
		"ppt_inst_id", inst.ID,
		"file_url", fileURL,
		"elapsed_ms", elapsedMillis(startedAt),
	)
	return updated, send(event.Thinking("✅ PPT 渲染完成，开始生成总结\n"))
}

func (a *Agent) success(ctx context.Context, send func(event.Event) bool, logger *slog.Logger, input agent.Input, record *runRecord, inst Instance, modify bool) bool {
	startedAt := time.Now()
	pageCount := strings.Count(inst.PPTSchema, `"pageType"`)
	if pageCount == 0 {
		pageCount = a.findTemplate(inst.TemplateCode).SlideCount
	}
	prompt := summaryPrompt(inst.Requirement, inst.FileURL, pageCount, modify)
	stream, err := a.model.Stream(ctx, model.Request{
		Messages:    []model.Message{{Role: model.RoleUser, Content: prompt}},
		Temperature: defaultTemperature,
		RequestID:   input.RequestID,
	})
	if err != nil {
		_ = send(event.Text(fallbackSummary(inst, pageCount, modify)))
		_ = send(event.Complete())
		return true
	}

	chunkCount := 0
	for chunk := range stream {
		if chunk.Err != nil {
			logger.Warn("⚠ PPTBuilder 总结流读取失败，使用兜底总结",
				"conversation_id", input.ConversationID,
				"ppt_inst_id", inst.ID,
				"chunk_count", chunkCount,
				"elapsed_ms", elapsedMillis(startedAt),
				"error", chunk.Err,
			)
			_ = send(event.Text(fallbackSummary(inst, pageCount, modify)))
			_ = send(event.Complete())
			return true
		}
		if chunk.Done {
			break
		}
		if chunk.Content == "" {
			continue
		}
		chunkCount++
		if !send(event.Text(chunk.Content)) {
			return false
		}
	}
	logger.Info("✅ PPTBuilder 总结输出完成",
		"conversation_id", input.ConversationID,
		"ppt_inst_id", inst.ID,
		"chunk_count", chunkCount,
		"elapsed_ms", elapsedMillis(startedAt),
	)
	_ = send(event.Complete())
	return true
}

func (a *Agent) failed(send func(event.Event) bool, inst Instance) bool {
	msg := strings.TrimSpace(inst.ErrorMsg)
	if msg == "" {
		msg = "PPT 生成失败，状态机进入 FAILED。"
	}
	_ = send(event.Error("PPT_FAILED", "PPT 生成遇到问题", msg))
	_ = send(event.Complete())
	return true
}

func (a *Agent) fail(ctx context.Context, send func(event.Event) bool, inst Instance, status Status, reason string) (Instance, bool) {
	updated, _ := a.store.Update(ctx, inst.ID, func(current *Instance) {
		current.Status = status
		current.ErrorMsg = reason
	})
	_ = send(event.Error("PPT_STAGE_FAILED", "PPT 阶段执行失败", reason))
	_ = send(event.Complete())
	return updated, false
}

func (a *Agent) executeModify(ctx context.Context, send func(event.Event) bool, logger *slog.Logger, input agent.Input, record *runRecord, inst Instance) bool {
	if strings.TrimSpace(inst.PPTSchema) == "" {
		_ = send(event.Text("当前会话中没有可修改的 PPT Schema，请先生成一个 PPT。"))
		_ = send(event.Complete())
		return true
	}
	if !send(event.Thinking("✏️ 正在分析修改需求并重新生成 PPT Schema...\n")) {
		return false
	}
	modified, ok := a.schema(ctx, send, logger, input, inst, schemaModifyPrompt(input.Query, inst.PPTSchema))
	if !ok {
		return false
	}
	rendered, ok := a.render(ctx, send, logger, input, modified)
	if !ok {
		return false
	}
	return a.success(ctx, send, logger, input, record, rendered, true)
}

func (a *Agent) findTemplate(code string) Template {
	for _, tmpl := range a.templates {
		if tmpl.Code == code {
			return tmpl
		}
	}
	if len(a.templates) == 0 {
		return Template{}
	}
	return a.templates[0]
}

func fallbackSchema(inst Instance, tmpl Template) string {
	return fmt.Sprintf(`{"slides":[{"pageType":"COVER","pageDesc":"封面页","templatePageIndex":1,"data":{"title":{"type":"text","content":%q,"fontLimit":18},"subtitle":{"type":"text","content":"由 KimoAgent Go PPTBuilder 生成","fontLimit":40}}},{"pageType":"CONTENT","pageDesc":"主要内容","templatePageIndex":3,"data":{"title":{"type":"text","content":"核心内容","fontLimit":18},"bullets":{"type":"text","content":%q,"fontLimit":120}}},{"pageType":"END","pageDesc":"结束页","templatePageIndex":6,"data":{"summary":{"type":"text","content":"谢谢观看","fontLimit":60}}}]}`,
		previewText(inst.Requirement, 18),
		previewText(inst.Outline, 120),
	)
}

func fallbackSummary(inst Instance, pageCount int, modify bool) string {
	if modify {
		return fmt.Sprintf("✅ PPT 已成功修改完成。\n\n文件链接：%s", inst.FileURL)
	}
	return fmt.Sprintf("✅ PPT 已成功生成完成。\n\n本次为您制作了一份 PPT，共 %d 页。\n\n文件链接：%s", pageCount, inst.FileURL)
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func elapsedMillis(startedAt time.Time) int64 {
	return time.Since(startedAt).Milliseconds()
}

func previewText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len([]rune(text)) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit])
}

func buildPPTSearchQuery(requirement string, originalQuery string) string {
	cleaned := cleanRequirementForSearch(requirement)
	if topic := extractRequirementTopic(requirement); topic != "" {
		cleaned = topic
	}
	if cleaned == "" {
		cleaned = cleanRequirementForSearch(originalQuery)
	}
	if cleaned == "" {
		return "PPT 主题资料"
	}
	return truncateRunes(cleaned, maxPPTSearchChars)
}

func cleanRequirementForSearch(text string) string {
	replacer := strings.NewReplacer(
		"【开始生成PPT】", " ",
		"【暂停生成PPT】", " ",
		"```json", " ",
		"```", " ",
		"\r", " ",
		"\n", " ",
		"\t", " ",
	)
	text = replacer.Replace(text)
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return strings.Join(strings.Fields(text), " ")
}

func extractRequirementTopic(text string) string {
	candidates := []string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "-*0123456789.、 "))
		if line != "" {
			candidates = append(candidates, line)
		}
	}
	if len(candidates) == 0 {
		candidates = append(candidates, text)
	}

	for _, line := range candidates {
		lower := strings.ToLower(line)
		for _, key := range []string{"主题", "标题", "topic", "title"} {
			idx := strings.Index(lower, key)
			if idx < 0 {
				continue
			}
			value := strings.TrimSpace(line[idx+len(key):])
			value = strings.TrimLeft(value, "：: -—")
			value = cutBeforeAny(value, []string{"；", ";", "，", ",", "。", ".", "\n"})
			value = cleanRequirementForSearch(value)
			if value != "" {
				return value
			}
		}
	}
	return ""
}

func cutBeforeAny(text string, separators []string) string {
	cut := len(text)
	for _, sep := range separators {
		if idx := strings.Index(text, sep); idx >= 0 && idx < cut {
			cut = idx
		}
	}
	return strings.TrimSpace(text[:cut])
}

func truncateRunes(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[:limit]))
}

var _ agent.Agent = (*Agent)(nil)
