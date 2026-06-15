# WebSearchReactAgent Source Map

## Java Source

严格参考目录：

```text
D:/projects/learnDemo/ai_workspeace/LLMentor/agent/dodo-agent
```

当前 Go 复刻重点只覆盖 `/agent/chat/stream` 对应链路：

```text
AgentController.webSearchStream
-> initWebSearchAgent
-> WebSearchReactAgent.stream(conversationId, query)
-> streamInternal
-> scheduleRound
-> processChunk
-> finishRound
-> executeToolCalls
-> scheduleRound
```

## Controller Mapping

Java:

```text
src/main/java/.../controller/AgentController.java
GET /agent/chat/stream
```

职责：

- 校验 `query` 和 `conversationId`
- 初始化 `WebSearchReactAgent`
- 加载持久化 ChatMemory
- 返回 `Flux<String>`，produces 为 `text/event-stream`

Go:

```text
internal/api/http/agent_handler.go
cmd/KimoAgent/main.go
```

职责：

- 校验 `query` 和 `conversationId`
- 注入 `websearch.ReactAgent`
- 读取 `<-chan event.Event`
- 写出 SSE

当前差异：

- Go 版已经补入最小 `Memory Runtime + MySQL ai_session`，用于加载最近历史和保存问答结果；会话列表/删除接口后续再补。
- Go 版已经补入单机版 `AgentTaskManager`，支持 conversationId 互斥、客户端断开取消和 `/agent/stop` 主动停止；Redis 分布式停止后续再补。

## Agent Loop Mapping

Java:

```text
WebSearchReactAgent.scheduleRound
WebSearchReactAgent.processChunk
WebSearchReactAgent.finishRound
WebSearchReactAgent.executeToolCalls
```

Go:

```text
internal/agents/websearch/react_agent.go
ReactAgent.scheduleRounds
ReactAgent.streamRound
ReactAgent.executeToolCalls
ReactAgent.forceFinalStream
```

核心语义：

- 每轮都调用模型 stream。
- 如果流式 chunk 中出现 `tool_calls`，本轮进入工具调用模式。
- 工具调用参数可能跨 chunk，需要合并。
- 轮次结束后统一执行工具，并把 tool response 加回 messages。
- 如果本轮没有工具调用，模型输出就是最终答案。

## Round State Mapping

Java:

```java
RoundMode mode
StringBuilder textBuffer
List<AssistantMessage.ToolCall> toolCalls
boolean inThink
```

Go:

```go
type roundState struct {
    mode       roundMode
    textBuffer strings.Builder
    toolCalls  []model.ToolCall
    inThink    bool
}
```

## Tool Call Mapping

Java:

```java
AssistantMessage.ToolCall
ToolResponseMessage.ToolResponse
ToolCallback.call(argsJson)
```

Go:

```go
model.ToolCall
model.Message{Role: model.RoleTool, ToolCallID: ...}
tool.Registry.Execute(ctx, name, args)
```

## Event Mapping

Java `AgentResponse` / `AgentStreamEvent`:

```text
text
thinking
tool_start
tool_end
reference
recommend
error
complete
```

Go:

```text
internal/runtime/event/event.go
```

当前 Go 版不再新增 `trace` 类型，避免偏离原前端协议。

## Search Result Mapping

Java:

```text
AgentState.searchResults
SearchResult(url, title, content)
createReferenceResponse(JSON.toJSONString(agentState.searchResults))
```

Go:

```text
agentState.searchResults []tool.SearchResult
event.Reference(tool.MustJSON(results), len(results))
```

## Known Gaps

这些差异保留到后续里程碑：

- Memory：Java 会保存 question/answer/thinking/tools/reference/recommend；Go 暂未持久化。
- Task：Go 已实现单机 `AgentTaskManager`，用 `context.CancelFunc` 替代 Java Reactor `Disposable`；Redis 分布式停止暂未实现。
- Tavily：Java 通过 MCP 初始化 `ToolCallback`；Go 当前先用 Tavily HTTP API 和 mock fallback。
- Recommendations：Java 默认生成推荐问题；Go 版等 Memory/推荐上下文完善后补齐。
