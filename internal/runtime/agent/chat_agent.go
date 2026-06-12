package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/learn-demo/agent-go/internal/runtime/event"
	"github.com/learn-demo/agent-go/internal/runtime/model"
	"github.com/learn-demo/agent-go/internal/runtime/tool"
)

// ChatAgent 是 Milestone 早期的教学版 Agent。
//
// 它展示了“Agent -> event channel -> HTTP/SSE”的最小闭环：
//  1. 根据用户输入决定是否走本地工具。
//  2. 如果不走工具，就直接把问题转发给模型流。
//  3. 把模型 chunk 或工具结果统一转换成 event.Event。
//
// 注意：它不是当前严格复刻 dodo-agent 的主实现。
// 真正对齐 Java WebSearchReactAgent 的主流程在 internal/agents/websearch/react_agent.go。
type ChatAgent struct {
	// model 是最基础的模型运行时。
	model model.Model
	// tools 是可选工具注册表；这个 ChatAgent 只做简单关键字规划，不是完整 ReAct。
	tools *tool.Registry
}

// NewChatAgent 创建带默认工具集的教学版 ChatAgent。
// 默认工具集来自 runtime/tool，方便早期 Milestone 直接演示工具调用事件。
func NewChatAgent(model model.Model) *ChatAgent {
	tools, err := tool.NewDefaultRegistry(tool.DefaultRegistryConfig{})
	if err != nil {
		panic(err)
	}

	return NewChatAgentWithTools(model, tools)
}

// NewChatAgentWithTools 允许测试或示例注入自定义工具注册表。
// 这样 Agent 主流程可以复用，工具集合可以按场景替换。
func NewChatAgentWithTools(model model.Model, tools *tool.Registry) *ChatAgent {
	return &ChatAgent{
		model: model,
		tools: tools,
	}
}

// Run 把一次用户请求转换成可被 HTTP/SSE 层消费的事件流。
//
// 这个函数的结构和 WebSearch ReactAgent.Run 很像，都是：
//  1. 创建只读事件 channel。
//  2. 启动 goroutine 执行耗时的模型/工具工作。
//  3. 通过 send 函数把内部结果统一推入 channel。
//  4. goroutine 退出时 close(events)，通知上游流式响应结束。
//
// 区别在于：这里的工具规划是硬编码关键词规则，而 WebSearch ReactAgent
// 是严格参考 dodo-agent，通过模型原生 tool_calls 驱动 ReAct 多轮流程。
func (a *ChatAgent) Run(ctx context.Context, input Input) (<-chan event.Event, error) {
	// events 是 Agent 对外暴露的唯一数据通道。
	// 调用方只能读，不能写；这能避免 HTTP 层反向污染 Agent 内部状态。
	events := make(chan event.Event)

	// 模型流和工具执行都可能阻塞，所以放到 goroutine 中运行。
	// Run 本身快速返回 channel，让 HTTP handler 可以马上开始写 SSE 响应头。
	go func() {
		defer close(events)
		logger := slog.Default()
		if input.RequestID != "" {
			logger = logger.With("request_id", input.RequestID)
		}
		if input.ConversationID != "" {
			logger = logger.With("conversation_id", input.ConversationID)
		}

		// send 是普通 ChatAgent 的唯一事件出口；客户端取消时停止后续模型或工具工作。
		send := func(evt event.Event) bool {
			select {
			case <-ctx.Done():
				return false
			case events <- evt:
				return true
			}
		}

		if !send(event.Thinking("Preparing Agent Runtime...\n")) {
			return
		}
		if a.tools != nil {
			if !send(event.Recommend("Available tools loaded", a.tools.Definitions())) {
				return
			}
		}

		// 这是 Milestone 早期的简单工具规划：用关键词决定是否调用一个工具。
		// WebSearch ReactAgent 已经改为模型原生 tool_calls，这里保留用于学习对比。
		if call, ok := a.planToolCall(input.Query); ok {
			if !a.runToolCall(ctx, send, logger, call) {
				return
			}
			_ = send(event.Complete())
			return
		}

		if !send(event.Thinking("Calling Model Runtime...\n")) {
			return
		}

		// 没有命中本地工具规划时，直接把问题交给模型流式回答。
		// 这里不传 tools schema，因此模型不会返回 tool_calls，只会返回普通文本 chunk。
		stream, err := a.model.Stream(ctx, model.Request{
			Temperature: 0.7,
			Messages: []model.Message{
				{
					Role:    model.RoleSystem,
					Content: "You are an assistant for learning Agent Runtime. Answer directly and clearly, with emphasis on runtime boundaries.",
				},
				{
					Role:    model.RoleUser,
					Content: input.Query,
				},
			},
			RequestID: input.RequestID,
		})
		if err != nil {
			_ = send(event.Error("MODEL_START_FAILED", "model stream failed to start", err.Error()))
			return
		}

		// 模型层已经把 HTTP SSE 的 data 行、JSON delta、DONE 标记等细节封装成 StreamChunk。
		// ChatAgent 只需要把正文 chunk.Content 转成 text 事件，并在 Done 时补 complete。
		for chunk := range stream {
			if chunk.Err != nil {
				_ = send(event.Error("MODEL_STREAM_FAILED", "model stream failed", chunk.Err.Error()))
				return
			}
			if chunk.Done {
				_ = send(event.Complete())
				return
			}
			if chunk.Content == "" {
				continue
			}
			if !send(event.Text(chunk.Content)) {
				return
			}
		}

		_ = send(event.Complete())
	}()

	return events, nil
}

type plannedToolCall struct {
	// name 是 Registry 中的工具名。
	name string
	// args 是已经规划好的工具参数。
	args map[string]any
}

// planToolCall 是早期教学版的规则规划器。
// 它不解析模型输出，也不代表 dodo-agent WebSearchReactAgent 的真实语义。
func (a *ChatAgent) planToolCall(query string) (plannedToolCall, bool) {
	if a.tools == nil {
		return plannedToolCall{}, false
	}

	normalized := strings.ToLower(strings.TrimSpace(query))
	if normalized == "" {
		return plannedToolCall{}, false
	}

	if strings.Contains(normalized, "\u5929\u6c14") || strings.Contains(normalized, "weather") {
		return plannedToolCall{
			name: "weather_mock",
			args: map[string]any{"city": inferCity(query)},
		}, true
	}

	if strings.Contains(normalized, "\u65f6\u95f4") ||
		strings.Contains(normalized, "\u51e0\u70b9") ||
		strings.Contains(normalized, "time") {
		return plannedToolCall{
			name: "current_time",
			args: map[string]any{"timezone": "Asia/Shanghai"},
		}, true
	}

	if strings.Contains(normalized, "\u641c\u7d22") ||
		strings.Contains(normalized, "\u67e5\u4e00\u4e0b") ||
		strings.Contains(normalized, "\u67e5\u8be2") ||
		strings.Contains(normalized, "search") {
		return plannedToolCall{
			name: "web_search",
			args: map[string]any{"query": query},
		}, true
	}

	return plannedToolCall{}, false
}

// runToolCall 执行一次由 planToolCall 规划出的本地工具调用。
//
// 事件顺序保持和 WebSearch ReactAgent 一致：
//  1. tool_start：前端知道哪个工具开始执行，以及参数是什么。
//  2. tool_end：前端拿到工具输出。
//  3. text：教学版直接把工具结果作为最终文本输出。
//
// 完整 ReAct 不会在这里完成；它会把工具结果追加成 tool role message，
// 再交给模型进入下一轮推理。这里为了早期学习链路，做了简化。
func (a *ChatAgent) runToolCall(ctx context.Context, send func(event.Event) bool, logger *slog.Logger, call plannedToolCall) bool {
	callID := fmt.Sprintf("%s-%d", call.name, time.Now().UnixNano())
	args := tool.MustJSON(call.args)

	logger.Info("\U0001F527 工具调用开始", "tool", call.name, "call_id", callID, "arguments", args)
	if !send(event.ToolStart(call.name, callID, args)) {
		return false
	}

	result, err := a.tools.Execute(ctx, call.name, call.args)
	if err != nil {
		logger.Error("\U0000274C 工具调用失败", "tool", call.name, "call_id", callID, "error", err)
		return send(event.Error("TOOL_EXECUTION_FAILED", "tool execution failed", err.Error()))
	}
	logger.Info("\U00002705 工具调用完成", "tool", call.name, "call_id", callID)

	if !send(event.ToolEnd(call.name, callID, result.Content)) {
		return false
	}

	return send(event.Text(result.Content))
}

// inferCity 是 weather_mock 的简单参数推断器。
//
// 它只服务于教学版关键词规划：如果问题里出现中文城市名，就转成工具需要的英文城市名。
// 没有命中时默认 Shanghai，避免工具参数为空导致示例链路中断。
func inferCity(query string) string {
	candidates := []struct {
		match string
		city  string
	}{
		{match: "\u5317\u4eac", city: "Beijing"},
		{match: "\u4e0a\u6d77", city: "Shanghai"},
		{match: "\u5e7f\u5dde", city: "Guangzhou"},
		{match: "\u6df1\u5733", city: "Shenzhen"},
		{match: "\u676d\u5dde", city: "Hangzhou"},
		{match: "\u5357\u4eac", city: "Nanjing"},
		{match: "\u6210\u90fd", city: "Chengdu"},
		{match: "\u6b66\u6c49", city: "Wuhan"},
		{match: "\u897f\u5b89", city: "Xi'an"},
		{match: "\u91cd\u5e86", city: "Chongqing"},
	}

	for _, candidate := range candidates {
		if strings.Contains(query, candidate.match) {
			return candidate.city
		}
	}
	return "Shanghai"
}
