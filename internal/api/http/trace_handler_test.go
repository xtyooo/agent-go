package http

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/learn-demo/agent-go/internal/runtime/event"
	"github.com/learn-demo/agent-go/internal/runtime/trace"
)

func TestTraceReplayStreamEmitsSavedEvents(t *testing.T) {
	store, err := trace.NewFileStore(trace.Config{
		Enabled:   true,
		Directory: t.TempDir(),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new trace store: %v", err)
	}
	recorder, err := store.Start(context.Background(), trace.RunMeta{
		TraceID:        "replay-test",
		RequestID:      "req-1",
		ConversationID: "conv-1",
		AgentType:      "websearch",
		StartedAt:      time.Now(),
	})
	if err != nil {
		t.Fatalf("start trace: %v", err)
	}
	recorder.Record(event.Thinking("开始"))
	recorder.Record(event.Text("你好"))
	recorder.Record(event.Complete())
	if _, err := recorder.Finish("completed", nil); err != nil {
		t.Fatalf("finish trace: %v", err)
	}

	handler := NewTraceHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), store)
	req := httptest.NewRequest(http.MethodGet, "/trace/replay/stream?traceId=replay-test", nil)
	res := httptest.NewRecorder()

	handler.ReplayStream(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	body := res.Body.String()
	for _, want := range []string{`"type":"thinking"`, `"content":"开始"`, `"type":"text"`, `"content":"你好"`, `"type":"complete"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("replay body missing %s: %s", want, body)
		}
	}
}

func TestTraceDetailReturnsSavedRun(t *testing.T) {
	store, err := trace.NewFileStore(trace.Config{
		Enabled:   true,
		Directory: t.TempDir(),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new trace store: %v", err)
	}
	recorder, err := store.Start(context.Background(), trace.RunMeta{
		TraceID:        "detail-test",
		ConversationID: "conv-1",
		AgentType:      "skills",
		StartedAt:      time.Now(),
	})
	if err != nil {
		t.Fatalf("start trace: %v", err)
	}
	recorder.Record(event.Text("详情"))
	if _, err := recorder.Finish("completed", nil); err != nil {
		t.Fatalf("finish trace: %v", err)
	}

	handler := NewTraceHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), store)
	req := httptest.NewRequest(http.MethodGet, "/trace/detail?traceId=detail-test", nil)
	res := httptest.NewRecorder()

	handler.GetTrace(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	if body := res.Body.String(); !strings.Contains(body, `"traceId":"detail-test"`) || !strings.Contains(body, `"agentType":"skills"`) {
		t.Fatalf("unexpected detail body: %s", body)
	}
}
