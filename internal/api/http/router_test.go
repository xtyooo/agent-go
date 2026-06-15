package http

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/learn-demo/agent-go/internal/runtime/memory"
	"github.com/learn-demo/agent-go/internal/runtime/task"
)

func TestRouterServesWebAppAndOptions(t *testing.T) {
	distDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(distDir, "index.html"), []byte("<!doctype html><title>KimoAgent</title>"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	t.Setenv("KIMO_WEB_DIST_DIR", distDir)

	router := NewRouterWithAgents(slog.Default(), nil, task.NewManager(slog.Default()), memory.NoopStore{})

	for _, path := range []string{"/", "/chat/example"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", path, res.Code)
		}
		if body := res.Body.String(); !strings.Contains(body, "<!doctype html>") {
			t.Fatalf("GET %s body = %q", path, body)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/session/list", nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /session/list status = %d, want 200", res.Code)
	}
	if strings.Contains(res.Body.String(), "<!doctype html>") {
		t.Fatalf("GET /session/list was handled by web app: %q", res.Body.String())
	}

	req = httptest.NewRequest(http.MethodOptions, "/agent/chat/stream", nil)
	res = httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS status = %d, want 204", res.Code)
	}
}
