package model

import "context"

type Role string

const (
	// RoleSystem 是系统提示词，通常只在消息列表开头出现。
	RoleSystem Role = "system"
	// RoleUser 是用户输入或 Agent 追加的控制指令。
	RoleUser Role = "user"
	// RoleAssistant 是模型输出；当模型请求工具时，这里还会带 ToolCalls。
	RoleAssistant Role = "assistant"
	// RoleTool 是工具执行结果，必须通过 ToolCallID 回连到 assistant 的 tool call。
	RoleTool Role = "tool"
)

// Message 是 OpenAI-compatible chat message 的领域模型。
// WebSearch ReAct 会不断追加 assistant(tool_calls) 和 tool(response) message 形成多轮上下文。
type Message struct {
	// Role 决定消息在模型协议中的身份：system/user/assistant/tool。
	Role Role `json:"role"`
	// Content 是自然语言文本；assistant 请求工具时可以为空。
	Content string `json:"content"`
	// Name 主要用于 tool message，表示是哪一个工具返回的结果。
	Name string `json:"name,omitempty"`
	// ToolCallID 用于 tool message，必须匹配模型发出的 ToolCall.ID。
	ToolCallID string `json:"toolCallId,omitempty"`
	// ToolCalls 用于 assistant message，保存模型原生工具调用。
	ToolCalls []ToolCall
}

// Request 是模型运行时的统一请求结构。
// OpenAICompatible 会把它转换成 /v1/chat/completions 的 JSON 请求。
type Request struct {
	// Messages 是完整上下文窗口，包含系统提示词、用户问题、工具调用和工具结果。
	Messages []Message
	// Tools 是本轮暴露给模型的函数工具 schema；为空时模型不能原生调用工具。
	Tools []ToolDefinition
	// Temperature 控制模型采样随机性。
	Temperature float64
	// MaxTokens 为 0 时不显式限制，由模型服务使用默认值。
	MaxTokens int
	// RequestID 仅用于日志链路追踪，不会发送给模型。
	RequestID string
}

// Response 是非流式 Generate 的完整结果。
type Response struct {
	// Content 是模型一次性返回的文本。
	Content string
	// ToolCalls 是模型一次性返回的原生工具调用。
	ToolCalls []ToolCall
}

// Chunk 是模型流式输出的最小单位。
// Content 和 ToolCalls 都可能为空；Done 表示服务端发送了 [DONE]。
type Chunk struct {
	// Content 是本次 delta 的文本片段。
	Content string
	// ToolCalls 是本次 delta 的工具调用片段，arguments 可能跨 chunk 拆分。
	ToolCalls []ToolCall
	// Done 表示流式响应正常结束。
	Done bool
	// Err 表示读取、解析或上游请求失败。
	Err error
}

// ToolDefinition 是暴露给模型的工具 schema。
type ToolDefinition struct {
	// Name 必须和 tool.Registry 中注册的工具名一致。
	Name string
	// Description 帮助模型判断何时调用该工具。
	Description string
	// Schema 是 JSON Schema parameters，对应 OpenAI function.parameters。
	Schema map[string]any
}

// ToolCall 是模型原生 tool_calls 的领域模型。
// 流式模式下，同一个 tool call 的 Arguments 经常被拆成多个 delta，需要按 ID/Index 合并。
type ToolCall struct {
	// ID 是模型分配的工具调用 ID，后续 tool response 必须带回。
	ID string
	// Index 是流式 delta 中的工具调用序号；部分服务早期 delta 可能没有 ID，只能靠 Index 合并。
	Index int
	// Name 是函数工具名。
	Name string
	// Arguments 是 JSON 字符串；流式模式下可能只是一个片段。
	Arguments string
}

// Model 抽象模型运行时，Agent 只依赖这里，不直接依赖具体 HTTP SDK。
type Model interface {
	Generate(ctx context.Context, req Request) (Response, error)
	Stream(ctx context.Context, req Request) (<-chan Chunk, error)
}
