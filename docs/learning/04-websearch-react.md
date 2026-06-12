# 04 WebSearch ReAct Agent

## 严格参考的 Java 源码

这次 Milestone 4 对照的是：

```text
D:/projects/learnDemo/ai_workspeace/LLMentor/agent/dodo-agent/src/main/java/cn/hollis/llm/mentor/agent/agent/websearch/WebSearchReactAgent.java
D:/projects/learnDemo/ai_workspeace/LLMentor/agent/dodo-agent/src/main/java/cn/hollis/llm/mentor/agent/agent/BaseAgent.java
D:/projects/learnDemo/ai_workspeace/LLMentor/agent/dodo-agent/src/main/java/cn/hollis/llm/mentor/agent/controller/AgentController.java
D:/projects/learnDemo/ai_workspeace/LLMentor/agent/dodo-agent/src/main/java/cn/hollis/llm/mentor/agent/entity/record/RoundState.java
D:/projects/learnDemo/ai_workspeace/LLMentor/agent/dodo-agent/src/main/java/cn/hollis/llm/mentor/agent/utils/ThinkTagParser.java
```

原项目的 WebSearch ReAct 不是让模型输出 JSON action，再由 Agent 解析。它使用 Spring AI 的原生 `tool_calls`：

```text
ChatClient.stream().chatResponse()
-> processChunk()
-> 如果 chunk 里有 tool_calls，RoundState.mode = TOOL_CALL
-> finishRound()
-> executeToolCalls()
-> append ToolResponseMessage
-> scheduleRound()
-> 没有 tool_calls 时认为本轮是最终答案
```

Go 版现在按这个语义复刻。

## 这个模块解决什么问题？

它把 Milestone 3 的 Tool Runtime 放进真正的 Agent 状态循环。

用户问题进入 `/agent/chat/stream` 后，Go 版 `websearch.ReactAgent` 会：

```text
构造 system prompt + user question
-> 调用 OpenAI-compatible stream，并传入 tools schema
-> 边接收 content 边输出 text/thinking SSE
-> 边接收 tool_calls 边合并参数
-> 轮次结束后执行工具
-> 追加 assistant tool_calls + tool response message
-> 进入下一轮
-> 无 tool_calls 时输出 reference 并 complete
```

这和 Java 版 `scheduleRound / processChunk / finishRound / executeToolCalls` 的职责划分一致。

## 核心接口是什么？

Go 侧新增了模型工具调用结构：

```go
type ToolDefinition struct {
    Name        string
    Description string
    Schema      map[string]any
}

type ToolCall struct {
    ID        string
    Index     int
    Name      string
    Arguments string
}
```

`model.Request` 支持：

```go
Messages []Message
Tools    []ToolDefinition
```

`model.Chunk` 支持：

```go
Content   string
ToolCalls []ToolCall
```

这对应 Java 里的：

```java
gen.getOutput().getText()
gen.getOutput().getToolCalls()
```

## 它和原项目的边界如何对应？

HTTP Controller：

- Java：`AgentController.webSearchStream`
- Go：`AgentHandler.ChatStream`

Agent：

- Java：`WebSearchReactAgent`
- Go：`internal/agents/websearch.ReactAgent`

轮次状态：

- Java：`RoundState.mode/textBuffer/toolCalls/inThink`
- Go：`roundState.mode/textBuffer/toolCalls/inThink`

工具执行：

- Java：`ToolCallback.call(argsJson)`
- Go：`tool.Registry.Execute(ctx, name, args)`

搜索结果引用：

- Java：`AgentState.searchResults`，最终 `createReferenceResponse`
- Go：`agentState.searchResults`，最终 `event.Reference`

think 标签解析：

- Java：`ThinkTagParser.parse(text, state.inThink)`
- Go：`parseThinkSegments(chunk, &state.inThink)`

## 这次实现里我踩了什么坑？

第一个坑是 ReAct 形式判断错了。

之前我实现成了 JSON action parser，这不是原项目的 WebSearchReactAgent。原项目依赖模型原生 tool_call 字段，Agent 不解析自然语言 action，而是在 stream chunk 里发现 tool_calls 后进入工具模式。

第二个坑是流式 tool_call 需要合并。

OpenAI-compatible 流式响应里，工具参数可能被拆成多个 delta。Java 版用 `mergeToolCall` 拼接 arguments。Go 版也做了同样的合并，并兼容 `id` 或 `index`。

第三个坑是没有 tool_call 才是最终答案。

原项目的判断是：如果本轮 `RoundState.mode != TOOL_CALL`，说明模型没有请求工具，这一轮文本就是最终答案。Go 版现在也按这个规则结束。

第四个坑是事件协议不能随便扩展。

原前端只认识 `text/thinking/tool_start/tool_end/reference/recommend/error/complete`。Go 版撤掉了额外的 `trace` 事件，调试过程通过 thinking、tool_start、tool_end 和日志表达。
