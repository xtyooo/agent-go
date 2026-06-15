# Milestone 8 Context Runtime

本阶段先跳过 RAG，把上下文窗口管理独立出来。这样后续 RAG 接入时，只需要把检索片段作为一个新的 context section 交给 Context Runtime。

## 这个模块解决什么问题

Memory Runtime 负责“从哪里加载历史”，Context Runtime 负责“哪些内容能进入本次 prompt”。

如果没有 Context Runtime，长会话会不断把历史问答塞进 `messages`，最终带来三个问题：

1. 请求超过模型上下文窗口。
2. 旧历史挤占当前问题和工具结果的位置。
3. Agent 无法解释本次 prompt 是由哪些部分组成的。

## 核心接口

实现位于 `internal/runtime/contextx`。

```go
type Policy struct {
    MaxInputTokens       int
    ReservedOutputTokens int
    MaxHistoryTokens     int
    CharsPerToken        int
}

type Builder struct {}

func (b *Builder) Build(sections ...Section) BuildResult
```

当前 section 包括：

```text
system   系统提示词
history  会话历史
current  当前用户问题
rag      RAG 检索上下文，预留给后续 Milestone
```

## 当前策略

当前先实现保守策略：

- system/current 不裁剪。
- history 按预算裁剪。
- history 优先保留最近消息。
- 使用粗略 token 估算：`rune_count / chars_per_token + message_overhead`。
- 输出上下文预算日志，解释本次 prompt 组成。

日志示例：

```text
🧮 上下文预算已应用
message_count=...
input_token_estimate=...
history_message_input=...
history_message_kept=...
history_message_dropped=...
```

## 配置方式

`application.yaml`：

```yaml
context:
  max-input-tokens: 12000
  reserved-output-tokens: 2000
  max-history-tokens: 4000
  chars-per-token: 4
```

这些 token 都是估算值，不是模型 tokenizer 的精确值。后续如果需要精确控制，可以把 `EstimateMessage` 替换成模型对应 tokenizer。

## 和 WebSearch Agent 的关系

原来的链路是：

```text
system prompt
-> appendHistory
-> current question
```

现在改成：

```text
system messages
history messages
current messages
-> contextx.Builder.Build
-> messages
```

这样 WebSearch Agent 不再直接决定历史怎么裁剪，只负责提供上下文来源。

## 当前暂不实现

这次没有实现：

- RAG context 拼接。
- summary/compaction 模型摘要。
- 压缩摘要持久化。
- 精确 tokenizer。
- ReAct 轮次内部的 tool message 二次裁剪。

原因是你当前决定先跳过 RAG。现在先把上下文预算边界建好，后续 RAG、summary、Plan-Execute 都可以复用这层。
