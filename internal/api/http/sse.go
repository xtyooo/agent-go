package http

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"agentG/internal/runtime/event"
	"agentG/internal/runtime/task"
)

type StreamSummary struct {
	// EventCount 是本次 SSE 写出的事件总数。
	EventCount int
	// TypeCounts 按事件类型统计数量，用于判断一次请求主要消耗在 text、thinking 还是 tool 事件。
	TypeCounts map[event.Type]int
	// FirstEventMs 是从打开 SSE 到写出第一个事件的耗时。
	FirstEventMs int64
}

// StreamObserver 是 SSE 写出边界的观察者。
// Trace Runtime 通过这个回调记录已经准备写给前端的事件，但不会影响事件本身的发送。
type StreamObserver func(event.Event)

// StreamOptions 保存 SSE 写出时的可选扩展能力。
type StreamOptions struct {
	// Observer 会在事件写入 ResponseWriter 前被调用。
	Observer StreamObserver
}

// StreamEvents 把 Agent 事件 channel 写成浏览器可消费的 SSE。
// 这里是 HTTP 层和 Agent 层的背压边界：客户端断开会通过 request context 反向取消 Agent。
func StreamEvents(w http.ResponseWriter, r *http.Request, events <-chan event.Event, logger *slog.Logger, conversationID string, requestID string) StreamSummary {
	return StreamEventsWithOptions(w, r, events, logger, conversationID, requestID, StreamOptions{})
}

// StreamEventsWithOptions 把 Agent 事件写成 SSE，并允许调用方观察已发送事件。
func StreamEventsWithOptions(w http.ResponseWriter, r *http.Request, events <-chan event.Event, logger *slog.Logger, conversationID string, requestID string, options StreamOptions) StreamSummary {
	if logger == nil {
		logger = slog.Default()
	}
	if requestID != "" {
		logger = logger.With("request_id", requestID)
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		logger.Error("\U0000274C 当前响应不支持 SSE 流式写出", "conversation_id", conversationID)
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return newStreamSummary()
	}

	startedAt := time.Now()
	summary := newStreamSummary()
	logger.Info("\U0001F4E1 SSE 流已打开", "conversation_id", conversationID)

	for {
		select {
		case <-r.Context().Done():
			logger.Warn("\U0001F6D1 SSE 流被客户端取消",
				"conversation_id", conversationID,
				"event_count", summary.EventCount,
				"event_text_count", summary.TypeCounts[event.TypeText],
				"event_thinking_count", summary.TypeCounts[event.TypeThinking],
				"event_tool_start_count", summary.TypeCounts[event.TypeToolStart],
				"event_tool_end_count", summary.TypeCounts[event.TypeToolEnd],
				"event_reference_count", summary.TypeCounts[event.TypeReference],
				"event_error_count", summary.TypeCounts[event.TypeError],
				"event_complete_count", summary.TypeCounts[event.TypeComplete],
				"first_event_ms", summary.FirstEventMs,
				"elapsed_ms", elapsedMillis(startedAt),
				"error", r.Context().Err(),
			)
			return summary
		case evt, ok := <-events:
			if !ok {
				logger.Info("\U00002705 SSE 流已正常关闭",
					"conversation_id", conversationID,
					"event_count", summary.EventCount,
					"event_text_count", summary.TypeCounts[event.TypeText],
					"event_thinking_count", summary.TypeCounts[event.TypeThinking],
					"event_tool_start_count", summary.TypeCounts[event.TypeToolStart],
					"event_tool_end_count", summary.TypeCounts[event.TypeToolEnd],
					"event_reference_count", summary.TypeCounts[event.TypeReference],
					"event_error_count", summary.TypeCounts[event.TypeError],
					"event_complete_count", summary.TypeCounts[event.TypeComplete],
					"first_event_ms", summary.FirstEventMs,
					"elapsed_ms", elapsedMillis(startedAt),
				)
				return summary
			}
			recordEvent(&summary, evt, startedAt)
			if options.Observer != nil {
				options.Observer(evt)
			}
			writeSSE(w, evt)
			flusher.Flush()
		}
	}
}

func newStreamSummary() StreamSummary {
	return StreamSummary{TypeCounts: make(map[event.Type]int)}
}

// recordEvent 在真正写出 SSE 前更新观测统计。
// 统计发生在 writeSSE 前，这样即使前端处理慢，也能区分“生成慢”和“写出慢”。
func recordEvent(summary *StreamSummary, evt event.Event, startedAt time.Time) {
	summary.EventCount++
	summary.TypeCounts[evt.Type]++
	if summary.EventCount == 1 {
		summary.FirstEventMs = elapsedMillis(startedAt)
	}
}

func WriteSSEEvent(w http.ResponseWriter, evt event.Event) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	writeSSE(w, evt)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func StreamTaskRecords(w http.ResponseWriter, r *http.Request, records <-chan task.EventRecord, logger *slog.Logger, conversationID string, requestID string, options StreamOptions) StreamSummary {
	if logger == nil {
		logger = slog.Default()
	}
	if requestID != "" {
		logger = logger.With("request_id", requestID)
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		logger.Error("current response writer does not support SSE streaming", "conversation_id", conversationID)
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return newStreamSummary()
	}

	startedAt := time.Now()
	summary := newStreamSummary()
	logger.Info("task SSE stream opened", "conversation_id", conversationID)

	for {
		select {
		case <-r.Context().Done():
			logger.Warn("task SSE stream was closed by client",
				"conversation_id", conversationID,
				"event_count", summary.EventCount,
				"first_event_ms", summary.FirstEventMs,
				"elapsed_ms", elapsedMillis(startedAt),
				"error", r.Context().Err(),
			)
			return summary
		case record, ok := <-records:
			if !ok {
				logger.Info("task SSE stream closed",
					"conversation_id", conversationID,
					"event_count", summary.EventCount,
					"first_event_ms", summary.FirstEventMs,
					"elapsed_ms", elapsedMillis(startedAt),
				)
				return summary
			}
			evt := record.Event
			recordEvent(&summary, evt, startedAt)
			if options.Observer != nil {
				options.Observer(evt)
			}
			writeSSE(w, evt)
			flusher.Flush()
		}
	}
}

func writeSSE(w http.ResponseWriter, evt event.Event) {
	payload, err := json.Marshal(evt)
	if err != nil {
		payload = []byte(`{"type":"error","code":"EVENT_ENCODE_FAILED","content":"failed to encode event"}`)
	}

	_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
}

func elapsedMillis(startedAt time.Time) int64 {
	return time.Since(startedAt).Milliseconds()
}
