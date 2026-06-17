package http

import (
	"net/http/httptest"
	"testing"
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
