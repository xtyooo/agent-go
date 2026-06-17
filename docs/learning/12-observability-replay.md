# Milestone 12：Observability + Replay

本阶段补的是 Agent Runtime 的可观测性和离线回放能力。

在 Java dodo-agent 里，很多运行信息来自 Reactor 流上的 `doOnNext`、`doOnError`、`doFinally` 以及日志。Go 版没有 Reactor，但有一个更清晰的边界：

```text
Agent goroutine
    -> event.Event channel
    -> task.Manager.WrapEvents
    -> http.StreamEventsWithOptions
    -> 浏览器 SSE
                    └─ trace.Recorder 记录事件
```

也就是说，Trace Runtime 不侵入 WebSearch、Plan-Execute、Skills、PPTX 的主流程，而是在 HTTP/SSE 边界记录已经准备发送给前端的事件。

## 这个模块解决什么问题？

Agent 一旦进入流式执行，排查问题不能只看最后的报错。我们需要知道：

- 本次请求的 `traceId`、`requestId`、`conversationId`、`agentType`。
- SSE 总共输出了多少事件。
- 哪些事件是 `thinking`、`text`、`tool_start`、`tool_end`、`reference`、`error`、`complete`。
- 第一个事件多久出现。
- 最终是正常完成、客户端取消，还是启动失败。
- 失败时前端收到的错误事件是什么。

Trace JSON 保存后，可以不再调用模型和工具，直接用 replay 接口复现前端流式输出。

## 核心接口

Trace Runtime 位于：

```text
internal/runtime/trace
```

核心结构：

```go
type Store interface {
    Start(ctx context.Context, meta RunMeta) (*Recorder, error)
    Load(ctx context.Context, traceID string) (Run, error)
}
```

当前实现是 `FileStore`，每次 Agent Run 保存一个 JSON 文件：

```text
traces/{traceId}.json
```

`Run` 中保存：

- `RunMeta`：请求级元数据。
- `Events`：按顺序保存的 SSE 事件。
- `OffsetMs`：事件相对 Run 开始的耗时。
- `TypeCounts`：事件类型统计。
- `Status`：`running / completed / cancelled / failed`。

敏感配置不会写入 trace。模型 API Key、Tavily Key、MCP Header 都只存在于配置和运行时初始化中。

## HTTP 接口

正常请求仍然走原来的 Agent SSE 接口：

```text
GET /agent/chat/stream?conversationId=1&query=...
GET /agent/deep/stream?conversationId=1&query=...
GET /agent/skills/stream?conversationId=1&query=...
GET /agent/pptx/stream?conversationId=1&query=...
```

每次请求会生成一个 `trace_id`，控制台日志会输出：

```text
🧭 Agent Trace 已开始 trace_id=...
📊 Agent Trace 已保存 trace_id=... file=traces/xxx.json
📊 Agent Trace 摘要 trace_id=... event_count=...
```

查询 trace：

```text
GET /trace/detail?traceId=xxx
GET /trace/{traceId}
```

回放 trace：

```text
GET /trace/replay/stream?traceId=xxx
GET /trace/{traceId}/replay/stream
```

默认快速回放，不等待原始事件间隔。需要模拟原始节奏时：

```text
GET /trace/replay/stream?traceId=xxx&timing=original&maxDelayMs=500
```

`maxDelayMs` 用于限制单次等待，避免原始请求中长时间等待模型导致 replay 也很慢。

## 配置

`application.yaml`：

```yaml
observability:
  trace:
    enabled: true
    directory: "traces"
    max-event-content-chars: 4000
```

`max-event-content-chars` 用于截断单个事件里的长文本字段。比如搜索结果、工具返回和长回答都会被限制长度，避免 trace 文件过大。

## 和 Java 流式控制的对应关系

Java Reactor 版常见结构是：

```text
Sinks.Many<String>  -> SSE 输出通道
Disposable          -> 模型流取消句柄
doOnNext            -> 事件观测点
doFinally           -> 任务收尾
```

Go 版对应为：

```text
chan event.Event          -> SSE 输出通道
context.CancelFunc        -> 模型流和工具调用的取消句柄
StreamOptions.Observer    -> 事件观测点
defer task.Manager.Remove -> 任务收尾
trace.Recorder.Finish     -> trace 收尾落盘
```

这也是为什么 trace 记录放在 `StreamEventsWithOptions` 上，而不是塞进某个具体 Agent。只要 Agent 最终输出 `event.Event`，它就天然获得 trace 能力。

## 这次实现里踩的坑

第一，失败请求也需要 trace。

如果会话已有任务正在执行，或者 Agent 启动失败，请求不会进入正常事件循环。这里要手动把 `error` 事件写入 recorder，否则失败 trace 会是空的。

第二，Replay 不能重新调用模型。

Replay 的价值在于“只复现当时前端看到的事件”。因此 `/trace/replay/stream` 只读取 JSON 中的 `Events`，不会进入 Agent、Model、Tool Runtime。

第三，Trace 记录点要靠近 SSE。

如果在 Agent 内部记录，四类 Agent 都要重复接入；如果在 Model Runtime 记录，又看不到工具事件和 complete 事件。SSE 边界是当前项目最稳定的统一观测点。

## 完成标准

当前已完成：

- 每次 Agent Run 自动生成 `traceId`。
- 自动记录 SSE 事件序列、事件类型统计、首事件耗时和总耗时。
- trace 保存为 JSON 文件。
- 支持 `/trace/detail` 查询。
- 支持 `/trace/replay/stream` 离线 SSE 回放。
- 支持中文 emoji 控制台日志。
- 增加 `internal/runtime/trace` 和 HTTP replay 单元测试。

后续可继续增强：

- 记录每轮模型请求摘要、prompt token 估算和模型耗时。
- 记录工具调用的参数摘要、返回摘要和错误栈。
- 增加 trace list 接口。
- 将 trace 从本地文件迁移到 MySQL 或对象存储。
