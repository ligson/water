package api

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ligson/water/water-be/internal/agent"
	"github.com/ligson/water/water-be/internal/config"
	"github.com/ligson/water/water-be/internal/realtime"
	"github.com/ligson/water/water-be/internal/requestid"
)

type Router struct {
	db     *sql.DB
	cfg    config.Config
	logger *slog.Logger
	hub    *realtime.Hub
	agent  *agent.Runner
	mu     sync.Mutex
	cancel map[string]taskRun
}

type taskRun struct {
	id     string
	cancel context.CancelFunc
}

func NewRouter(db *sql.DB, cfg config.Config, logger *slog.Logger) http.Handler {
	r := &Router{
		db:     db,
		cfg:    cfg,
		logger: logger,
		hub:    realtime.NewHub(),
		cancel: make(map[string]taskRun),
	}
	r.agent = agent.NewRunner(db, r.appendTaskEvent)

	return requestid.Middleware(corsMiddleware(r))
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	switch req.URL.Path {
	case "/api/health":
		r.handleHealth(w, req)
	case "/api/providers":
		r.handleProviders(w, req)
	case "/api/workspaces":
		r.handleWorkspaces(w, req)
	default:
		if strings.HasPrefix(req.URL.Path, "/ws/tasks/") {
			r.handleTaskWebSocket(w, req, strings.TrimPrefix(req.URL.Path, "/ws/tasks/"))
			return
		}
		if strings.HasPrefix(req.URL.Path, "/api/tasks/") {
			r.handleTaskByID(w, req, strings.TrimPrefix(req.URL.Path, "/api/tasks/"))
			return
		}
		if strings.HasPrefix(req.URL.Path, "/api/approvals/") {
			r.handleApprovalByID(w, req, strings.TrimPrefix(req.URL.Path, "/api/approvals/"))
			return
		}
		if strings.HasPrefix(req.URL.Path, "/api/providers/") {
			r.handleProviderByID(w, req, strings.TrimPrefix(req.URL.Path, "/api/providers/"))
			return
		}
		if strings.HasPrefix(req.URL.Path, "/api/workspaces/") {
			r.handleWorkspaceByID(w, req, strings.TrimPrefix(req.URL.Path, "/api/workspaces/"))
			return
		}
		WriteError(req.Context(), w, http.StatusNotFound, "not found")
	}
}

func (r *Router) handleHealth(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ctx := req.Context()
	if err := r.db.PingContext(ctx); err != nil {
		r.logger.ErrorContext(ctx, "database health check failed", "error", err)
		WriteError(ctx, w, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	WriteOK(ctx, w, "ok", map[string]interface{}{
		"service": "water-be",
		"status":  "ok",
		"time":    time.Now().Format(time.RFC3339),
	})
}
