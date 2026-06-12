# 03 Tool Runtime

## 这个模块解决什么问题？

Tool Runtime 把“外部能力调用”从 Agent 逻辑里拆出来。

在这次实现之前，`ChatAgent` 只有一条路径：接收用户输入，然后直接调用 `Model Runtime` 流式返回。这样的问题是，后续无论接天气、搜索、文件检索还是 MCP 工具，都容易把执行细节塞进 Agent。

这次实现后，工具有了独立边界：

- 工具用统一接口暴露名称、描述、参数 schema。
- 工具执行结果统一封装为 `tool.Result`。
- 工具注册、查找、执行由 `tool.Registry` 负责。
- Agent 只负责决定是否调用工具，以及把工具过程转换成 SSE 事件。

## 核心接口是什么？

核心接口在 `internal/runtime/tool`：

```go
type Tool interface {
    Definition() Definition
    Execute(ctx context.Context, input Input) (Result, error)
}
```

`Definition` 描述工具给 Agent 或模型看的元信息：

```go
type Definition struct {
    Name        string
    Description string
    Schema      map[string]any
}
```

`Registry` 是工具运行时的入口：

```go
registry.Register(tool)
registry.Definitions()
registry.Execute(ctx, name, args)
```

这次内置了三个 mock 工具：

- `current_time`
- `weather_mock`
- `web_search_mock`

## 它和其他模块的边界是什么？

和 HTTP 层的边界：

HTTP 层仍然只负责 SSE 写出，不知道工具怎么执行。

和 Agent 层的边界：

Agent 负责把用户输入映射成工具调用，并把工具调用生命周期转换成事件：

```text
recommend -> tool_start -> tool_end -> text -> complete
```

和 Model Runtime 的边界：

这次还没有实现真正的 ReAct。工具调用暂时由 `ChatAgent.planToolCall` 的简单规则触发。Milestone 4 会把这部分替换成：

```text
LLM decides action -> Tool Runtime executes -> Observation appended -> Next round
```

和 Event Runtime 的边界：

工具过程通过已有事件协议输出：

- `recommend`：列出可用工具定义。
- `tool_start`：工具开始执行，携带 toolName、callId、arguments。
- `tool_end`：工具执行完成，携带结果摘要。
- `error`：工具失败时返回，不让整个 Agent 崩溃。

## 这次实现里我踩了什么坑？

第一个坑是不要提前做 ReAct。

Milestone 3 的重点不是让模型决定工具，而是先把工具边界建出来。现在的规则触发比较粗糙，但它能验证工具注册、执行、日志和 SSE 事件链路。等 Milestone 4 再替换决策层。

第二个坑是工具参数 schema 暂时只是描述，不是强校验。

这次没有引入 JSON Schema 校验库，只做了最小参数读取和必填检查。后续接 MCP 或真实工具前，需要把 schema 校验补强。

第三个坑是工具必须响应 `context.Context`。

即使是 mock 工具，也先检查了 `ctx.Done()`。后面工具可能会访问 HTTP API、数据库或文件系统，如果不响应 context，`/agent/stop` 和客户端断开连接就不能真正停止任务。

第四个坑是源码编码。

当前 Windows PowerShell 环境显示 UTF-8 中文源码时容易乱码。为了避免 Go 字符串被误判和维护困难，这次新增 Go 源码保持 ASCII，中文意图匹配用 Unicode escape 表达。
