package trace

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"agentG/internal/runtime/event"
)

func TestFileStoreRecordsAndLoadsTrace(t *testing.T) {
	store, err := NewFileStore(Config{
		Enabled:              true,
		Directory:            t.TempDir(),
		MaxEventContentChars: 8,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}

	recorder, err := store.Start(context.Background(), RunMeta{
		TraceID:        "trace-test",
		RequestID:      "req-1",
		ConversationID: "conv-1",
		AgentType:      "websearch",
		Query:          "测试 trace",
		StartedAt:      time.Now(),
	})
	if err != nil {
		t.Fatalf("start trace: %v", err)
	}

	recorder.Record(event.Thinking("准备调用模型"))
	recorder.Record(event.Text("一二三四五六七八九十"))
	summary, err := recorder.Finish("completed", nil)
	if err != nil {
		t.Fatalf("finish trace: %v", err)
	}
	if summary.EventCount != 2 {
		t.Fatalf("event count = %d, want 2", summary.EventCount)
	}
	if summary.TypeCounts[event.TypeText] != 1 {
		t.Fatalf("text type count = %d, want 1", summary.TypeCounts[event.TypeText])
	}

	run, err := store.Load(context.Background(), "trace-test")
	if err != nil {
		t.Fatalf("load trace: %v", err)
	}
	if run.TraceID != "trace-test" || run.Status != "completed" {
		t.Fatalf("unexpected run metadata: %#v", run)
	}
	if len(run.Events) != 2 {
		t.Fatalf("events length = %d, want 2", len(run.Events))
	}
	if !strings.Contains(run.Events[1].Event.Content, "trace已截断") {
		t.Fatalf("expected content to be truncated, got %q", run.Events[1].Event.Content)
	}
}

func TestDisabledStoreReturnsNoopRecorder(t *testing.T) {
	store, err := NewFileStore(Config{Enabled: false}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new disabled store: %v", err)
	}
	recorder, err := store.Start(context.Background(), RunMeta{TraceID: "disabled"})
	if err != nil {
		t.Fatalf("start disabled trace: %v", err)
	}
	if recorder.Enabled() {
		t.Fatal("disabled store should return noop recorder")
	}
	recorder.Record(event.Text("不会落盘"))
	if _, err := recorder.Finish("completed", nil); err != nil {
		t.Fatalf("finish noop recorder: %v", err)
	}
}
