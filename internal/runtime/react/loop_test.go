package react

import (
	"context"
	"errors"
	"testing"

	"agentG/internal/runtime/event"
	"agentG/internal/runtime/model"
	"agentG/internal/runtime/tool"
)

type fakeModel struct {
	streams [][]model.Chunk
	calls   int
}

func (m *fakeModel) Generate(context.Context, model.Request) (model.Response, error) {
	return model.Response{}, nil
}

func (m *fakeModel) Stream(context.Context, model.Request) (<-chan model.Chunk, error) {
	if m.calls >= len(m.streams) {
		return nil, errors.New("unexpected stream call")
	}
	chunks := m.streams[m.calls]
	m.calls++
	out := make(chan model.Chunk, len(chunks))
	for _, chunk := range chunks {
		out <- chunk
	}
	close(out)
	return out, nil
}

type echoTool struct{}

func (echoTool) Definition() tool.Definition {
	return tool.Definition{
		Name:        "echo",
		Description: "echo input",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"value": map[string]any{"type": "string"},
			},
		},
	}
}

func (echoTool) Execute(_ context.Context, input tool.Input) (tool.Result, error) {
	return tool.Result{Content: "echo:" + tool.StringArg(input.Arguments, "value")}, nil
}

func TestLoopExecutesToolThenCompletes(t *testing.T) {
	registry, err := tool.NewRegistry(echoTool{})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	fm := &fakeModel{streams: [][]model.Chunk{
		{
			{ToolCalls: []model.ToolCall{{ID: "call_1", Index: 0, Name: "echo", Arguments: `{"value":"hi"}`}}},
			{Done: true},
		},
		{
			{Content: "done"},
			{Done: true},
		},
	}}
	loop := New(fm, registry, nil, WithMaxRounds(3))

	var events []event.Event
	ok := loop.Stream(context.Background(), Params{Temperature: 0.7}, []model.Message{{Role: model.RoleUser, Content: "hi"}}, func(evt event.Event) bool {
		events = append(events, evt)
		return true
	})
	if !ok {
		t.Fatal("loop returned false")
	}
	if fm.calls != 2 {
		t.Fatalf("model calls = %d, want 2", fm.calls)
	}
	if !hasEvent(events, event.TypeToolStart) || !hasEvent(events, event.TypeToolEnd) || !hasEvent(events, event.TypeComplete) {
		t.Fatalf("missing expected events: %#v", events)
	}
}

func TestParseThinkSegmentsAcrossChunks(t *testing.T) {
	inThink := false
	first := ParseThinkSegments("a<think>x", &inThink)
	second := ParseThinkSegments("y</think>b", &inThink)
	if len(first) != 2 || first[0].Thinking || !first[1].Thinking || first[1].Content != "x" {
		t.Fatalf("unexpected first segments: %#v", first)
	}
	if len(second) != 2 || !second[0].Thinking || second[0].Content != "y" || second[1].Thinking || second[1].Content != "b" {
		t.Fatalf("unexpected second segments: %#v", second)
	}
	if inThink {
		t.Fatal("expected think state to be closed")
	}
}

func hasEvent(events []event.Event, typ event.Type) bool {
	for _, evt := range events {
		if evt.Type == typ {
			return true
		}
	}
	return false
}
