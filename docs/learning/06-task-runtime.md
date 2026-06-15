# Milestone 6 Task Runtime

本阶段复刻 Java dodo-agent 的 `AgentTaskManager`，解决流式任务的生命周期管理问题。

## Java 到 Go 的概念映射

Java Reactor 版本：

```text
Sinks.Many<String>  -> SSE 输出通道
Disposable          -> 模型流订阅控制句柄
ConcurrentHashMap   -> conversationId 到 TaskInfo 的任务表
dispose()           -> 取消上游模型生成
```

Go 版本：

```text
chan event.Event    -> SSE 输出通道
context.CancelFunc  -> 模型流和工具执行的取消句柄
map + sync.Mutex    -> conversationId 到 TaskInfo 的任务表
cancel()            -> 取消上游 HTTP stream / Scanner / tool call
```

核心点是：Go 里不需要 Reactor `Disposable`。只要模型请求、工具执行、Agent 主流程都使用同一个 `context.Context`，调用 `cancel()` 就会从源头中断模型 HTTP 请求和后续执行。

## 核心接口

实现位于 `internal/runtime/task`：

```go
type Manager struct {
    tasks map[string]*Info
}

func (m *Manager) Register(parent context.Context, conversationID string, agentType string) (*Info, error)
func (m *Manager) Stop(conversationID string) bool
func (m *Manager) Remove(info *Info)
func (m *Manager) WrapEvents(info *Info, source <-chan event.Event) <-chan event.Event
```

`Register` 做同会话互斥；`Stop` 触发取消；`Remove` 清理任务表；`WrapEvents` 在用户停止时给原 SSE 流补一条停止提示和 `complete`。

## 当前请求链路

```text
GET /agent/chat/stream
-> AgentHandler.ChatStream
-> taskManager.Register(r.Context(), conversationId, "websearch")
-> agent.Run(taskInfo.Context(), input)
-> taskManager.WrapEvents(taskInfo, events)
-> StreamEvents(...)
-> defer taskManager.Remove(taskInfo)
```

如果用户关闭浏览器或网络断开，`r.Context()` 会取消，派生出的 `taskInfo.Context()` 也会取消，模型流会被中断。

如果用户主动调用：

```text
GET /agent/stop?conversationId=xxx
```

则 `taskManager.Stop(conversationId)` 会关闭 stop channel 并调用 `cancel()`，Agent 和模型流会尽快退出。

## 并发控制

同一个 `conversationId` 只能注册一个运行中任务。

如果用户在回答尚未完成时再次发送消息，`Register` 会返回错误，HTTP 层输出：

```json
{"type":"error","code":"TASK_ALREADY_RUNNING","content":"该会话正在执行中，请稍后再试"}
```

这避免了同一会话多个模型流同时写 SSE，导致答案交错。

## 停止生成

停止生成不是简单关闭前端 SSE。

正确流程是：

```text
用户点击停止
-> /agent/stop
-> Manager.Stop
-> context cancel
-> 模型 HTTP stream 中断
-> Scanner 停止读取 resp.Body
-> 工具执行收到 ctx.Done
-> SSE 输出“已停止生成” + complete
-> Remove 清理任务
```

这和 Java `disposable.dispose()` 的目标一致：从源头终止模型推理，而不是只丢弃下游输出。

## 当前边界

本阶段实现的是单进程内任务管理：

- 支持同会话互斥。
- 支持主动停止。
- 支持客户端断开自动取消。
- 支持任务完成后清理。

还没有实现 Redis `SET NX TTL` 和 Pub/Sub 分布式停止，这属于下一步扩展。
