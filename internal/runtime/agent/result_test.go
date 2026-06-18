package agent

import (
	"context"
	"testing"

	"agentG/internal/runtime/event"
)

func TestCollectResultAggregatesStream(t *testing.T) {
	events := make(chan event.Event, 5)
	events <- event.Thinking("think ")
	events <- event.Text("hello")
	events <- event.Text(" world")
	events <- event.Reference(`[{"url":"https://example.com"}]`, 1)
	events <- event.Complete()
	close(events)

	result := CollectResult(context.Background(), events)
	if result.Status != ResultCompleted {
		t.Fatalf("status = %s, want completed", result.Status)
	}
	if result.Answer != "hello world" {
		t.Fatalf("answer = %q", result.Answer)
	}
	if result.Thinking != "think " {
		t.Fatalf("thinking = %q", result.Thinking)
	}
	if result.References == "" {
		t.Fatal("expected references to be captured")
	}
}

func TestCollectResultStopsOnError(t *testing.T) {
	events := make(chan event.Event, 2)
	events <- event.Text("partial")
	events <- event.Error("LLM_CALL_FAILED", "failed", "detail")
	close(events)

	result := CollectResult(context.Background(), events)
	if result.Status != ResultFailed {
		t.Fatalf("status = %s, want failed", result.Status)
	}
	if result.Answer != "partial" {
		t.Fatalf("answer = %q", result.Answer)
	}
	if result.Error == nil || result.Error.Code != "LLM_CALL_FAILED" {
		t.Fatalf("unexpected error: %#v", result.Error)
	}
}

func TestCollectResultCapturesStageOutput(t *testing.T) {
	events := make(chan event.Event, 2)
	events <- event.StageOutput("reference", "before_done", []string{"a", "b"})
	events <- event.Complete()
	close(events)

	result := CollectResult(context.Background(), events)
	if result.Status != ResultCompleted {
		t.Fatalf("status = %s, want completed", result.Status)
	}
	if got := result.StageOutputs["reference"]; got == nil {
		t.Fatal("expected reference stage output")
	}
}

func TestCollectResultStopsOnPaused(t *testing.T) {
	events := make(chan event.Event, 2)
	events <- event.Text("partial")
	events <- event.Paused("ask_user", "need input", map[string]any{"question": "continue?"})
	close(events)

	result := CollectResult(context.Background(), events)
	if result.Status != ResultPaused {
		t.Fatalf("status = %s, want paused", result.Status)
	}
	if result.Answer != "partial" {
		t.Fatalf("answer = %q", result.Answer)
	}
	if result.PauseState == nil || result.PauseState.Name != "ask_user" {
		t.Fatalf("unexpected pause state: %#v", result.PauseState)
	}
}
