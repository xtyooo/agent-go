package task

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/learn-demo/agent-go/internal/runtime/event"
)

func TestManagerRegisterRejectsDuplicateConversation(t *testing.T) {
	manager := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)))

	first, err := manager.Register(context.Background(), "c1", "websearch")
	if err != nil {
		t.Fatalf("register first task: %v", err)
	}
	defer manager.Remove(first)

	if _, err := manager.Register(context.Background(), "c1", "websearch"); err == nil {
		t.Fatal("expected duplicate conversation registration to fail")
	}
}

func TestManagerRemoveAllowsRegisterAgain(t *testing.T) {
	manager := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)))

	first, err := manager.Register(context.Background(), "c1", "websearch")
	if err != nil {
		t.Fatalf("register first task: %v", err)
	}
	manager.Remove(first)

	second, err := manager.Register(context.Background(), "c1", "websearch")
	if err != nil {
		t.Fatalf("register after remove: %v", err)
	}
	manager.Remove(second)
}

func TestStopCancelsContextAndEmitsStopEvents(t *testing.T) {
	manager := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)))
	info, err := manager.Register(context.Background(), "c1", "websearch")
	if err != nil {
		t.Fatalf("register task: %v", err)
	}
	defer manager.Remove(info)

	source := make(chan event.Event)
	wrapped := manager.WrapEvents(info, source)

	if !manager.Stop("c1") {
		t.Fatal("expected stop to find task")
	}

	select {
	case <-info.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("expected task context to be canceled")
	}

	first := readEvent(t, wrapped)
	if first.Type != event.TypeThinking || first.Content == "" {
		t.Fatalf("expected thinking stop event, got %#v", first)
	}

	second := readEvent(t, wrapped)
	if second.Type != event.TypeComplete {
		t.Fatalf("expected complete event, got %#v", second)
	}

	select {
	case _, ok := <-wrapped:
		if ok {
			t.Fatal("expected wrapped channel to close")
		}
	case <-time.After(time.Second):
		t.Fatal("expected wrapped channel to close")
	}
}

func readEvent(t *testing.T, events <-chan event.Event) event.Event {
	t.Helper()
	select {
	case evt, ok := <-events:
		if !ok {
			t.Fatal("event channel closed unexpectedly")
		}
		return evt
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
		return event.Event{}
	}
}
