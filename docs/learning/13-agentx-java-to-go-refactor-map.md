# Java AgentX 到 Go Agent 基座重构对照

本文对照：

- Java 项目：`D:\projects\learnDemo\ai_workspeace\spring-ai-agentx`
- Java 核心文档：`spring-ai-agentx\docs\core`
- Go 项目：`D:\projects\learnDemo\ai_workspeace\agent-go`

结论先行：Java 版是一个较完整的 Agent framework；当前 Go 版已经具备可运行的 ReAct 学习链路和若干 runtime 零件，但还没有把这些零件收敛成统一的“Agent 基座”。后续重构建议不要按 Java 文件逐个翻译，而是按 Agent 执行闭环逐层抽象。

## 一、核心定位差异

| 维度 | Java AgentX | 当前 Go 版 |
| --- | --- | --- |
| 项目形态 | 可配置 Agent 框架 | 学习型 Agent 项目 + 具体 Agent 实现 |
| 主入口 | `ReactAgent.builder()` | 多个具体 Agent：`websearch`、`planexecute`、`skills`、`pptx` |
| 执行核心 | `AgentLoopExecutor` 独立封装 | ReAct 主循环主要写在 `internal/agents/websearch/react_agent.go` |
| API 形态 | 同步、流式、完整结果、暂停恢复 | 以流式事件 `Run(ctx, input)` 为主 |
| 扩展机制 | Builder + Advisor + Provider + ToolCallback | Go interface + Option + Registry |
| 框架依赖 | Spring AI / Reactor / Advisor 链 | 标准 Go context/channel/interface |

## 二、能力交集

这些能力 Go 版已经具备雏形，可以作为重构时保留和上移的资产。

| 能力 | Java 位置 | Go 位置 | Go 现状 |
| --- | --- | --- | --- |
| ReAct tool-calling 循环 | `AgentLoopExecutor` | `internal/agents/websearch/react_agent.go` | 已实现，但耦合在 websearch Agent |
| 流式事件 | `AgentStreamEvent` | `internal/runtime/event` | 已实现基础事件 |
| 模型抽象 | Spring `ChatModel` / `ChatClient` | `internal/runtime/model` | 已抽象为 `Model` interface |
| 工具抽象 | `ToolCallback` | `internal/runtime/tool` | 已有 `Tool` / `Registry` |
| MCP 工具 | docs `02-工具与MCP` | `internal/runtime/mcp` | 已有 MCP runtime 和 adapter |
| Skills | `SkillsTool` | `internal/runtime/skill` + `ReadSkillTool` | 已有技能扫描和读取 |
| 任务并发控制 | `AgentTaskManager` | `internal/runtime/task` | 已有 conversation 级互斥和 stop |
| 会话记忆 | `ChatMemory` / `agentx_session` | `internal/runtime/memory` | 已有 session 记录和历史加载 |
| 上下文预算 | `ContextPolicy` / `ContextCompactor` | `internal/runtime/contextx` | 已有历史裁剪，未做压缩摘要 |
| 思考标签解析 | `ThinkingMode.THINK_TAG` | `parseThinkSegments` | 已支持 `<think>` 拆分 |
| Trace / replay | `TraceAudit` | `internal/runtime/trace` | 已有 SSE 事件级 trace |

## 三、主要差集

这些是 Java 基座中更框架化、当前 Go 版缺失或只部分具备的能力。

| Java 能力 | Go 当前状态 | 建议重构落点 |
| --- | --- | --- |
| 统一 `ReactAgent` Builder | 缺失，具体 Agent 各自装配 | `internal/runtime/agentx` 或 `internal/runtime/react` |
| `AgentLoopExecutor` | 缺失，循环散落在具体 Agent | 抽出通用 loop executor |
| `AgentResult`：Completed / Paused / Failed | 缺失，只有事件流 | 增加 result API，可由事件聚合得到 |
| `PauseState` / Human-in-the-Loop | 缺失 | 增加 pause/resume 状态模型 |
| `AskUserTool` / `PauseAdvisor` | 缺失 | Go 中用 tool interceptor / policy 替代 Advisor |
| `StageOutputProvider` | Go 只有 reference/recommend 专用事件 | 抽成通用 stage provider |
| `ThinkingMode.REASONING_CONTENT` | 缺失 | 扩展 `model.Chunk`，支持 reasoning 字段 |
| LLM 重试与重试事件 | 缺失或局部处理 | 抽 `LlmInvoker`，统一 retry |
| `ContextCompactor` micro/auto compact | Go 只有 trim history | 扩展 context runtime |
| `ToolSearch` 延迟工具加载 | 缺失 | 增加 deferred registry + search tool |
| 结构化输出 `OutputType` | 缺失 | 增加 schema/json repair/result parser |
| 三层记忆：session/profile/semantic | Go 只有 session | 增加 profile store、semantic memory 可选层 |
| `TodoWriteTool` 进度事件 | 缺失 | 增加 todo tool + `todo_progress` 事件 |
| `SubAgentTool` | 缺失 | 增加 Agent-as-Tool 包装 |
| 文件/Bash/Grep/Python 工具 | Go 目前偏 web/search/mock | 按安全边界逐个实现 |
| LLM request/response trace audit | Go 记录 SSE 事件，不记录每轮 LLM 请求 | 增加 round trace recorder |

## 四、推荐重构顺序

不要一开始就补全所有 Java 特性。最稳的学习顺序是先抽“主循环”，再补“横切能力”。

### Phase 1：抽出 Go 版通用 ReAct Loop

目标：把 `websearch.ReactAgent` 里通用的 ReAct 执行流程移到 runtime。

建议新增：

- `internal/runtime/react/loop.go`
- `internal/runtime/react/options.go`
- `internal/runtime/react/state.go`
- `internal/runtime/react/tool_executor.go`

核心对象：

```go
type Loop struct {
    Model model.Model
    Tools *tool.Registry
    MaxRounds int
    ContextPolicy contextx.Policy
}

type Params struct {
    Query string
    ConversationID string
    RequestID string
    Temperature float64
}

func (l *Loop) Stream(ctx context.Context, params Params, messages []model.Message) (<-chan event.Event, error)
```

迁移内容：

- `scheduleRounds`
- `streamRound`
- `executeToolCalls`
- `forceFinalStream`
- `roundState`
- `<think>` parsing

完成后，`websearch`、`skills`、后续通用 Agent 都复用同一个 loop。

### Phase 2：统一事件模型

目标：让 Go 事件接近 Java `AgentStreamEvent` 的扩展能力。

建议补充事件：

- `agent_start`
- `stage_output`
- `paused`
- `todo_progress`
- `retrying`

当前 `reference`、`recommend` 可以先保留，后续作为 `stage_output` 的特化兼容事件。

### Phase 3：抽 Tool Executor

目标：把工具执行、错误包装、参数解析、tool response message 组装从 Agent 里移走。

建议能力：

- 工具不存在时返回 JSON error 给模型，而不是直接中断
- arguments 非法 JSON 时做安全兜底
- 多 tool call 顺序保持
- 支持并发执行但按原顺序追加结果
- 为 HITL 和 StageOutput 留扩展点

### Phase 4：实现 Result API

Java 有：

- `call`
- `stream`
- `callForResult`
- `streamForResult`
- `resume`
- `resumeStream`

Go 可以先实现：

```go
type Result struct {
    Status string // completed, paused, failed
    Answer string
    Thinking string
    StageOutputs map[string]any
    PauseState *PauseState
    Error *Error
}
```

先由事件聚合实现 `CallForResult`，不要急着重写底层 loop。

### Phase 5：Human-in-the-Loop

Java 的 Advisor 不适合直接搬到 Go。Go 里建议用明确的 hook：

```go
type ToolPolicy interface {
    BeforeExecute(ctx context.Context, call model.ToolCall) (Decision, error)
}
```

`Decision` 可以是：

- execute
- pause
- deny

暂停时保存：

- messages
- current round
- pending tool calls
- params
- query
- token usage，可后补

### Phase 6：StageOutputProvider

把现在 websearch 里硬编码的 reference 输出抽象成：

```go
type StageTiming string

const (
    AfterStart StageTiming = "after_start"
    AfterToolEnd StageTiming = "after_tool_end"
    BeforeComplete StageTiming = "before_complete"
)

type StageOutputProvider interface {
    Name() string
    Timing() StageTiming
    Produce(ctx context.Context, state StageContext) (any, error)
}
```

这样 reference、recommend、tool summary、citation 都可以作为插件式输出。

### Phase 7：上下文压缩

当前 Go 的 `contextx` 是预算裁剪，不是 Java 的上下文压缩。

建议分两步：

1. `micro_compact`：压缩旧 tool result、旧 tool call arguments。
2. `auto_compact`：超过阈值时用 LLM 摘要早期上下文。

这部分应在每轮 LLM 调用前执行，而不是只在初始 messages 构建时执行。

### Phase 8：ToolSearch / Deferred Tools

当工具数量变多后再做。过早引入会增加调试复杂度。

Go 版可以实现：

- `DeferredRegistry`
- `tool_search` 元工具
- keyword search 先行
- LLM search 后补

### Phase 9：SubAgent

SubAgent 应该在通用 loop 稳定后实现，否则容易把父子 Agent 的事件、trace、memory 关系做乱。

建议规则：

- 子 Agent 通过 tool 暴露：`call_{name}`
- 子 Agent 有独立 messages/context
- 默认不写 session memory
- 子事件带 `source`
- 禁止第一阶段子 Agent 再嵌套子 Agent

## 五、Go 包结构建议

当前已有 runtime 包可以保留，新增抽象尽量往 runtime 放：

```text
internal/runtime/
  agent/
    agent.go              # 外部统一接口，保留
    result.go             # Result / PauseState
  react/
    loop.go               # 通用 ReAct loop
    invoker.go            # LLM 调用和 retry
    round.go              # RoundState / merge tool call
    tool_executor.go      # 工具执行
    stage.go              # StageOutput provider
    policy.go             # HITL / tool policy
  event/
    event.go              # 扩展事件类型
  contextx/
    context.go            # 当前预算构建
    compact.go            # 新增压缩
  tool/
    tool.go               # 保留
    deferred.go           # ToolSearch 后续加入
```

具体业务 Agent 应该变薄：

```text
internal/agents/websearch/
  agent.go      # 只负责 prompt、reference provider、默认工具选择

internal/agents/skills/
  agent.go      # 只负责 skills prompt/tool 装配
```

## 六、优先级判断

建议优先做：

1. 通用 ReAct Loop
2. Tool Executor
3. Result API
4. StageOutputProvider
5. HITL pause/resume

建议暂缓：

1. ToolSearch
2. SubAgent
3. Semantic memory
4. 结构化输出
5. Bash/Python/FileSystem 全套工具

原因是前五项会直接塑造 Go 版 Agent 基座的边界；后五项是能力扩展，依赖基座稳定。

## 七、迁移原则

Java 版值得学习的是职责拆分，不是 Spring AI 的具体形态。

迁移时重点保留这些设计：

- Agent 只负责装配，不直接塞满执行细节。
- Loop 负责轮次控制。
- Invoker 负责模型调用和重试。
- ToolExecutor 负责工具执行和 tool message 闭环。
- Event 是前端和 trace 的稳定协议。
- Memory、Context、Trace 都是横切能力，不应该绑死在某个具体 Agent。

Go 版不建议照搬这些设计：

- Spring Advisor 链。Go 中用显式 interface/hook 更清楚。
- Reactor Flux。Go 中 channel + context 足够。
- Builder 大而全。Go 中可以用 functional options，但要控制字段数量。
- Java 注解工具。Go 中保持显式 Tool interface。

