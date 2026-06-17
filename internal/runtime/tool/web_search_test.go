package tool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebSearchToolTruncatesLongTavilyQuery(t *testing.T) {
	var received tavilySearchRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answer":"ok","request_id":"req-1","results":[]}`))
	}))
	defer server.Close()

	search := NewWebSearchTool(WebSearchConfig{
		APIKey:   "test-key",
		Endpoint: server.URL,
	})
	longQuery := strings.Repeat("新能源汽车产业趋势", 80)

	result, err := search.Execute(context.Background(), Input{Arguments: map[string]any{"query": longQuery}})
	if err != nil {
		t.Fatalf("execute search: %v", err)
	}
	if got := len([]rune(received.Query)); got > tavilyMaxQueryChars {
		t.Fatalf("query length = %d, want <= %d", got, tavilyMaxQueryChars)
	}
	if truncated, ok := result.Data["query_truncated"].(bool); !ok || !truncated {
		t.Fatalf("expected query_truncated=true, got %#v", result.Data["query_truncated"])
	}
}

func TestNormalizeSearchQueryCollapsesWhitespace(t *testing.T) {
	query := normalizeSearchQuery("  AI\n\nAgent\t  PPT   ", tavilyMaxQueryChars)
	if query != "AI Agent PPT" {
		t.Fatalf("unexpected normalized query: %q", query)
	}
}
