package http

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentG/internal/runtime/agent"
	"agentG/internal/runtime/event"
	"agentG/internal/runtime/memory"
	"agentG/internal/runtime/task"
)

func TestRequestTraceIDUsesSafeQueryValue(t *testing.T) {
	req := httptest.NewRequest("GET", "/agent/chat/stream?traceId=web_abc-123.bad", nil)

	got := requestTraceID(req)
	if got != "web_abc-123bad" {
		t.Fatalf("requestTraceID() = %q, want sanitized query trace id", got)
	}
}

func TestRequestTraceIDGeneratesFallback(t *testing.T) {
	req := httptest.NewRequest("GET", "/agent/chat/stream", nil)

	got := requestTraceID(req)
	if got == "" {
		t.Fatal("requestTraceID() returned empty fallback")
	}
}

func TestAgentStreamCanAttachAfterClientDisconnect(t *testing.T) {
	current := &controlledAgent{
		started: make(chan struct{}),
		events:  make(chan event.Event, 8),
	}
	router := NewRouterWithAgentsAndTrace(
		slog.Default(),
		map[string]agent.Agent{"websearch": current},
		task.NewManager(slog.Default()),
		memory.NoopStore{},
		nil,
	)
	server := httptest.NewServer(router)
	defer server.Close()

	respCh := make(chan *stdhttp.Response, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := stdhttp.Get(server.URL + "/agent/chat/stream?query=hello&conversationId=conv-resume&traceId=trace_resume")
		if err != nil {
			errCh <- err
			return
		}
		respCh <- resp
	}()

	select {
	case <-current.started:
	case <-time.After(time.Second):
		t.Fatal("agent did not start")
	}

	current.events <- event.Thinking("first\n")
	resp := waitHTTPResponse(t, respCh, errCh)
	first := readSSEEvent(t, resp.Body)
	if first.Type != event.TypeThinking || first.Seq != 1 {
		t.Fatalf("first event = %#v, want thinking seq 1", first)
	}
	_ = resp.Body.Close()

	statusResp, err := stdhttp.Get(server.URL + "/agent/status?conversationId=conv-resume")
	if err != nil {
		t.Fatalf("status request: %v", err)
	}
	var statusPayload struct {
		Success bool          `json:"success"`
		Running bool          `json:"running"`
		Task    task.Snapshot `json:"task"`
	}
	if err := json.NewDecoder(statusResp.Body).Decode(&statusPayload); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	_ = statusResp.Body.Close()
	if !statusPayload.Success || !statusPayload.Running || statusPayload.Task.EventCount != 1 {
		t.Fatalf("unexpected status payload: %#v", statusPayload)
	}

	attachResp, err := stdhttp.Get(server.URL + "/agent/attach/stream?conversationId=conv-resume&from=1")
	if err != nil {
		t.Fatalf("attach request: %v", err)
	}
	defer attachResp.Body.Close()

	replayed := readSSEEvent(t, attachResp.Body)
	if replayed.Type != event.TypeThinking || replayed.Seq != 1 {
		t.Fatalf("replayed event = %#v, want thinking seq 1", replayed)
	}

	current.events <- event.Text("continued")
	current.events <- event.Complete()
	close(current.events)

	text := readSSEEvent(t, attachResp.Body)
	if text.Type != event.TypeText || text.Seq != 2 || text.Content != "continued" {
		t.Fatalf("text event = %#v, want text seq 2", text)
	}
	complete := readSSEEvent(t, attachResp.Body)
	if complete.Type != event.TypeComplete || complete.Seq != 3 {
		t.Fatalf("complete event = %#v, want complete seq 3", complete)
	}
}

type controlledAgent struct {
	started chan struct{}
	events  chan event.Event
}

func (a *controlledAgent) Run(ctx context.Context, input agent.Input) (<-chan event.Event, error) {
	close(a.started)
	return a.events, nil
}

func waitHTTPResponse(t *testing.T, respCh <-chan *stdhttp.Response, errCh <-chan error) *stdhttp.Response {
	t.Helper()
	select {
	case resp := <-respCh:
		return resp
	case err := <-errCh:
		t.Fatalf("stream request: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream response")
	}
	return nil
}

func readSSEEvent(t *testing.T, body io.Reader) event.Event {
	t.Helper()
	type result struct {
		evt event.Event
		err error
	}
	ch := make(chan result, 1)
	go func() {
		reader := bufio.NewReader(body)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				ch <- result{err: err}
				return
			}
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var evt event.Event
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &evt); err != nil {
				ch <- result{err: err}
				return
			}
			ch <- result{evt: evt}
			return
		}
	}()

	select {
	case result := <-ch:
		if result.err != nil {
			t.Fatalf("read SSE event: %v", result.err)
		}
		return result.evt
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SSE event")
		return event.Event{}
	}
}
