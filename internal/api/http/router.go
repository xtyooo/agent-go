package http

import (
	"log/slog"
	"net/http"

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
	r.Options("/*", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
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

	return r
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

		next.ServeHTTP(w, r)
	})
}
