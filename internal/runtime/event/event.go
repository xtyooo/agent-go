package event

import "time"

type Type string

const (
	TypeAgentStart   Type = "agent_start"
	TypeThinking     Type = "thinking"
	TypeText         Type = "text"
	TypeToolStart    Type = "tool_start"
	TypeToolEnd      Type = "tool_end"
	TypeReference    Type = "reference"
	TypeRecommend    Type = "recommend"
	TypeStageOutput  Type = "stage_output"
	TypePaused       Type = "paused"
	TypeTodoProgress Type = "todo_progress"
	TypeRetrying     Type = "retrying"
	TypeError        Type = "error"
	TypeComplete     Type = "complete"
)

// Event is the stable Agent -> HTTP/SSE protocol.
type Event struct {
	Seq  int  `json:"seq,omitempty"`
	Type Type `json:"type"`

	Content string `json:"content,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Detail  string `json:"detail,omitempty"`

	ToolName  string `json:"toolName,omitempty"`
	ToolCall  string `json:"toolCallId,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Result    string `json:"result,omitempty"`

	Data   any    `json:"data,omitempty"`
	Name   string `json:"name,omitempty"`
	Stage  string `json:"stage,omitempty"`
	Status string `json:"status,omitempty"`

	Attempt     int `json:"attempt,omitempty"`
	MaxAttempts int `json:"maxAttempts,omitempty"`
	Count       int `json:"count,omitempty"`

	Time time.Time `json:"time"`
}

func AgentStart(agentType, conversationID, requestID string) Event {
	return Event{
		Type:    TypeAgentStart,
		Name:    agentType,
		Content: agentType,
		Data: map[string]any{
			"agentType":      agentType,
			"conversationId": conversationID,
			"requestId":      requestID,
		},
		Time: time.Now(),
	}
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

func StageOutput(name, stage string, data any) Event {
	return Event{Type: TypeStageOutput, Name: name, Stage: stage, Data: data, Content: stringContent(data), Time: time.Now()}
}

func Paused(name, content string, data any) Event {
	return Event{Type: TypePaused, Name: name, Content: content, Data: data, Time: time.Now()}
}

func TodoProgress(name, status string, data any) Event {
	return Event{Type: TypeTodoProgress, Name: name, Status: status, Data: data, Content: stringContent(data), Time: time.Now()}
}

func Retrying(name string, attempt, maxAttempts int, detail string) Event {
	return Event{Type: TypeRetrying, Name: name, Attempt: attempt, MaxAttempts: maxAttempts, Detail: detail, Content: detail, Time: time.Now()}
}

func Error(code, content, detail string) Event {
	return Event{Type: TypeError, Code: code, Content: content, Message: content, Detail: detail, Time: time.Now()}
}

func Complete() Event {
	return Event{Type: TypeComplete, Time: time.Now()}
}

func stringContent(data any) string {
	value, _ := data.(string)
	return value
}
