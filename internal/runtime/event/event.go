package event

import "time"

type Type string

const (
	// TypeThinking 表示思考/状态提示，前端通常渲染为灰色过程信息。
	TypeThinking Type = "thinking"
	// TypeText 表示最终答案正文的流式片段。
	TypeText Type = "text"
	// TypeToolStart 表示工具开始执行。
	TypeToolStart Type = "tool_start"
	// TypeToolEnd 表示工具执行结束。
	TypeToolEnd Type = "tool_end"
	// TypeReference 表示搜索引用列表。
	TypeReference Type = "reference"
	// TypeRecommend 表示推荐问题，当前 Go 复刻版本暂未作为主链路重点。
	TypeRecommend Type = "recommend"
	// TypeError 表示可发送给前端的错误事件。
	TypeError Type = "error"
	// TypeComplete 表示本次 SSE 输出结束。
	TypeComplete Type = "complete"
)

// Event 是 Agent -> HTTP SSE 的统一事件协议。
// 类型集合保持和 dodo-agent 前端兼容，不额外引入 trace 之类的事件类型。
type Event struct {
	// Type 决定前端如何分发和渲染事件。
	Type Type `json:"type"`
	// Content 是通用文本载荷：text/thinking/reference/tool_end/error 都会使用。
	Content string `json:"content,omitempty"`
	// Code 是错误码，例如 BAD_REQUEST、LLM_CALL_FAILED。
	Code string `json:"code,omitempty"`
	// Message 是 error 事件的用户可读文案；为了兼容也会和 Content 保持一致。
	Message string `json:"message,omitempty"`
	// Detail 是错误详情，通常用于控制台或调试面板。
	Detail string `json:"detail,omitempty"`
	// ToolName 是 tool_start/tool_end 的工具名。
	ToolName string `json:"toolName,omitempty"`
	// ToolCall 是模型原生 tool_call_id，用来关联开始和结束事件。
	ToolCall string `json:"toolCallId,omitempty"`
	// Arguments 是工具调用参数 JSON 字符串。
	Arguments string `json:"arguments,omitempty"`
	// Result 与 Content 同步保存 tool_end 结果，兼容不同前端字段读取习惯。
	Result string `json:"result,omitempty"`
	// Data 用于结构化扩展数据，例如 recommend 的候选问题。
	Data any `json:"data,omitempty"`
	// Count 用于 reference 等聚合事件表示条目数量。
	Count int `json:"count,omitempty"`
	// Time 是服务端事件生成时间。
	Time time.Time `json:"time"`
}

func Thinking(content string) Event {
	return Event{Type: TypeThinking, Content: content, Time: time.Now()}
}

func Text(content string) Event {
	return Event{Type: TypeText, Content: content, Time: time.Now()}
}

func ToolStart(name, callID, args string) Event {
	return Event{Type: TypeToolStart, ToolName: name, ToolCall: callID, Arguments: args, Time: time.Now()}
}

func ToolEnd(name, callID, content string) Event {
	return Event{Type: TypeToolEnd, ToolName: name, ToolCall: callID, Content: content, Result: content, Time: time.Now()}
}

func Reference(content string, count int) Event {
	return Event{Type: TypeReference, Content: content, Count: count, Time: time.Now()}
}

func Recommend(content string, data any) Event {
	return Event{Type: TypeRecommend, Content: content, Data: data, Time: time.Now()}
}

func Error(code, content, detail string) Event {
	return Event{Type: TypeError, Code: code, Content: content, Message: content, Detail: detail, Time: time.Now()}
}

func Complete() Event {
	return Event{Type: TypeComplete, Time: time.Now()}
}
