# Milestone 9 Plan-Execute Agent

本阶段开始复刻 Java dodo-agent 的 `PlanExecuteAgent`。Java 版完整流程很大，包含需求澄清、研究主题生成、Plan、Execute、Critique、Compress、Summarize。本次 Go 版先把主流程、流式控制、任务取消、会话记忆和可观测日志打通。

## 当前实现范围

Go 版当前链路：

```text
GET /agent/deep/stream
-> Clarify requirement
-> Generate research topic
-> Plan
-> Execute tool tasks
-> Critique
-> repeat until passed or maxRounds
-> Synthesize final report
```

它复用已有 Runtime：

- Model Runtime：规划、评审、最终总结。
- Tool Runtime：执行 `web_search`。
- Task Runtime：同 conversationId 互斥和 `/agent/stop`。
- Context Runtime：需求澄清、研究主题和规划阶段上下文预算。
- Memory Runtime：加载历史，并保存 Plan-Execute 的问题、最终报告、思考过程、工具和引用。

## 和 Java 的概念映射

Java：

```text
PlanExecuteAgent
OverAllState
PlanExecutePrompts.PLAN / EXECUTE / CRITIQUE / SUMMARIZE
ToolCallback
Sinks.Many
AiSessionService
```

Go：

```text
internal/agents/planexecute.Agent
runState
prompts.go
tool.Registry
chan event.Event
memory.Store
```

## 当前事件输出

执行过程中会输出：

- `thinking`：需求分析、研究主题、计划数量、任务进度、评审结果。
- `tool_start`：每个计划任务开始执行。
- `tool_end`：每个工具任务返回结果。
- `reference`：搜索引用。
- `text`：最终研究报告流式正文。
- `complete`：输出结束。

## 当前边界

为了先跑通主干，本阶段暂不实现：

- 工具并发 Semaphore。
- 工具重试。
- 上下文压缩 Compress。
- 多工具选择，目前计划任务默认走 `web_search`。
- 分布式任务 TTL / Redis PubSub；当前是单进程 `context.CancelFunc` 取消。

这些能力都可以在当前主干上继续补。

## 本阶段关键实现点

- `internal/agents/planexecute.Agent` 是状态机式 Agent，不再依赖模型原生 `tool_calls` 驱动。
- `runState` 保存原始问题、历史消息、研究主题、任务结果、引用和评审反馈，对应 Java `OverAllState` 的教学版简化。
- `/agent/deep/stream` 复用 HTTP 多 Agent 路由，`/agent/stop?conversationId=...` 可以停止 deep research 任务。
- 模型输出中的 `<think>` 会被拆到 `thinking` 事件，最终报告正文只进入 `text` 事件。
- Plan/Critique 的 JSON 解析允许模型输出 markdown fenced JSON，并会先剥离 `<think>`。
- 新增 `types_test.go` 覆盖 JSON 提取、think 标签解析、done plan 判断和引用转换。

## 测试方式

启动服务后请求：

```powershell
curl.exe -N "http://127.0.0.1:8888/agent/deep/stream?conversationId=deep-1&query=请研究一下最近AI Agent工程化的发展趋势"
```

停止：

```powershell
curl.exe "http://127.0.0.1:8888/agent/stop?conversationId=deep-1"
```

如果同一个 `conversationId` 已经有 websearch 或 deep research 任务在跑，再发请求会得到 `TASK_ALREADY_RUNNING`。
