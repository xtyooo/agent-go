package http

import (
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/learn-demo/agent-go/internal/runtime/agent"
	"github.com/learn-demo/agent-go/internal/runtime/memory"
	"github.com/learn-demo/agent-go/internal/runtime/task"
)

func NewRouter(logger *slog.Logger, chatAgent agent.Agent, tasks *task.Manager) http.Handler {
	return NewRouterWithAgents(logger, map[string]agent.Agent{"websearch": chatAgent}, tasks, memory.NoopStore{})
}

func NewRouterWithAgents(logger *slog.Logger, agents map[string]agent.Agent, tasks *task.Manager, store memory.Store) http.Handler {
	r := chi.NewRouter()
	r.Use(corsMiddleware)

	handler := NewAgentHandlerWithAgents(logger, agents, tasks)
	sessionHandler := NewSessionHandler(logger, store)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	r.Route("/agent", func(r chi.Router) {
		r.Get("/chat/stream", handler.ChatStream)
		r.Get("/deep/stream", handler.DeepStream)
		r.Get("/skills/stream", handler.SkillsStream)
		r.Get("/stop", handler.StopAgent)
		r.Post("/stop", handler.StopAgent)
	})

	r.Route("/session", func(r chi.Router) {
		r.Get("/list", sessionHandler.ListSessions)
		r.Get("/detail", sessionHandler.GetSession)
		r.Get("/{sessionId}", sessionHandler.GetSession)
		r.Delete("/delete", sessionHandler.DeleteSession)
		r.Post("/delete", sessionHandler.DeleteSession)
		r.Delete("/{sessionId}", sessionHandler.DeleteSession)
		r.Post("/rename", sessionHandler.RenameSession)
		r.Put("/{sessionId}/rename", sessionHandler.RenameSession)
		r.Patch("/{sessionId}/rename", sessionHandler.RenameSession)
		r.Post("/{sessionId}/rename", sessionHandler.RenameSession)
	})

	mountWebApp(r, logger, webDistDir())

	return r
}

func mountWebApp(r chi.Router, logger *slog.Logger, distDir string) {
	info, err := os.Stat(distDir)
	if err != nil || !info.IsDir() {
		if logger != nil {
			logger.Warn("web app dist not found, skip static mount", "dir", distDir, "error", err)
		}
		return
	}

	fileServer := http.FileServer(http.Dir(distDir))
	r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		cleanPath := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if cleanPath == "" || cleanPath == "." {
			cleanPath = "index.html"
		}
		fullPath := filepath.Join(distDir, filepath.FromSlash(cleanPath))
		if info, err := os.Stat(fullPath); err == nil && !info.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(distDir, "index.html"))
	})
}

func webDistDir() string {
	if override := strings.TrimSpace(os.Getenv("KIMO_WEB_DIST_DIR")); override != "" {
		return override
	}

	defaultDir := filepath.Join("web", "agent-demo", "dist")
	candidates := []string{defaultDir}
	if cwd, err := os.Getwd(); err == nil {
		candidates = appendWebDistCandidates(candidates, cwd)
	}
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidates = append(candidates, filepath.Join(exeDir, "dist"))
		candidates = appendWebDistCandidates(candidates, exeDir)
	}

	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return defaultDir
}

func appendWebDistCandidates(candidates []string, start string) []string {
	dir, err := filepath.Abs(start)
	if err != nil {
		dir = start
	}
	for {
		candidates = append(candidates, filepath.Join(dir, "web", "agent-demo", "dist"))
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return candidates
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Expose-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
