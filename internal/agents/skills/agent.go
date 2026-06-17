package skills

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"agentG/internal/agents/websearch"
	"agentG/internal/runtime/agent"
	"agentG/internal/runtime/contextx"
	"agentG/internal/runtime/event"
	"agentG/internal/runtime/memory"
	"agentG/internal/runtime/model"
	"agentG/internal/runtime/skill"
	"agentG/internal/runtime/tool"
)

// Agent 是 Skills React Agent 的薄封装。
// Java dodo-agent 中 SkillsReactAgent 复用 ReAct 主循环，并额外挂载 Skills prompt/read_skill 工具；
// Go 版保持同样边界：这里只生成 Skills 系统提示词，具体 tool_calls 循环复用 websearch.ReactAgent。
type Agent struct {
	inner  agent.Agent
	skills *skill.Manager
}

type Option func(*options)

type options struct {
	maxRounds         int
	memory            memory.Store
	maxHistoryRecords int
	contextPolicy     contextx.Policy
}

func New(model model.Model, tools *tool.Registry, skillManager *skill.Manager, logger *slog.Logger, opts ...Option) *Agent {
	cfg := options{
		maxRounds:         5,
		memory:            memory.NoopStore{},
		maxHistoryRecords: 30,
		contextPolicy:     contextx.DefaultPolicy(),
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	react := websearch.New(model, tools, logger,
		websearch.WithAgentType("skills"),
		websearch.WithMaxRounds(cfg.maxRounds),
		websearch.WithMemory(cfg.memory, cfg.maxHistoryRecords),
		websearch.WithContextPolicy(cfg.contextPolicy),
		websearch.WithSystemPrompt(func(now time.Time) string {
			return systemPrompt(now, skillManager)
		}),
	)
	return &Agent{inner: react, skills: skillManager}
}

func WithMaxRounds(maxRounds int) Option {
	return func(o *options) {
		if maxRounds > 0 {
			o.maxRounds = maxRounds
		}
	}
}

func WithMemory(store memory.Store, maxHistoryRecords int) Option {
	return func(o *options) {
		if store != nil {
			o.memory = store
		}
		if maxHistoryRecords > 0 {
			o.maxHistoryRecords = maxHistoryRecords
		}
	}
}

func WithContextPolicy(policy contextx.Policy) Option {
	return func(o *options) {
		o.contextPolicy = policy.Normalize()
	}
}

func (a *Agent) Run(ctx context.Context, input agent.Input) (<-chan event.Event, error) {
	return a.inner.Run(ctx, input)
}

func systemPrompt(now time.Time, manager *skill.Manager) string {
	skillsPrompt := ""
	if manager != nil && manager.Enabled() {
		skillsPrompt = manager.Prompt(context.Background())
	}
	return fmt.Sprintf(`## 角色
你是一个全能型智能体助手，名字叫做：kimo，帮助用户解决各类问题。
你具备多种能力：联网搜索、当前时间查询、以及通过技能（Skill）系统获取专业领域的知识和工作流程。

## 当前系统时间
%s

## 技能使用指南
当用户的问题涉及某个专业领域时，你应该：
1. 先检查可用技能列表，看是否有匹配的技能。
2. 如果有，调用 read_skill 工具获取该技能的完整提示词。
3. 按照技能提示词中的指引来完成任务。
4. 如果没有合适技能，再使用联网搜索或直接回答。

## 联网搜索
当用户需要实时信息、时事新闻、技术资料等，可以使用 web_search 工具。

%s

## 工具调用规则
1. 工具调用必须只通过模型 tool_calls 字段输出。
2. 本轮无工具调用时，该轮输出会被视为最终答案。
3. 已有全部信息时，不要继续调用工具。
4. 禁止输出干扰解析的工具调用文本。

## 最终答案规则
- 输出自然语言答案，禁止包含工具调用格式。
- 尽量结构化呈现信息。
- 使用中文回答中文问题。
- 如果已经加载技能，必须遵守技能内容中的指令。`, now.Format(time.RFC3339), skillsPrompt)
}

var _ agent.Agent = (*Agent)(nil)
