package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ligson/water/water-be/internal/agent"
	"github.com/ligson/water/water-be/internal/auth"
	"github.com/ligson/water/water-be/internal/config"
	"github.com/ligson/water/water-be/internal/event"
	"github.com/ligson/water/water-be/internal/realtime"
	"github.com/ligson/water/water-be/internal/requestid"
	"github.com/ligson/water/water-be/internal/skill"
	"github.com/ligson/water/water-be/internal/task"
)

type Router struct {
	db     *sql.DB
	cfg    config.Config
	logger *slog.Logger
	hub    *realtime.Hub
	agent  *agent.Runner
	auth   *auth.Store
	skills *skill.Store
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
		auth:   auth.NewStore(db),
		skills: skill.NewStore(db, cfg.DataDir),
		cancel: make(map[string]taskRun),
	}
	r.agent = agent.NewRunner(db, r.appendTaskEvent)
	r.recoverInterruptedRunningTurns(context.Background())

	return requestid.Middleware(corsMiddleware(r))
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if r.cfg.AuthEnabled && !r.isPublicRoute(req.URL.Path) {
		if ok := r.requireAuth(w, req); !ok {
			return
		}
	}

	switch req.URL.Path {
	case "/api/health":
		r.handleHealth(w, req)
	case "/api/providers":
		r.handleProviders(w, req)
	case "/api/provider-models":
		r.handleProviderModels(w, req)
	case "/api/workspaces":
		r.handleWorkspaces(w, req)
	case "/api/skills":
		r.handleSkills(w, req)
	case "/api/skills/install":
		if req.Method != http.MethodPost {
			WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		r.installSkillFromURL(w, req)
	default:
		if strings.HasPrefix(req.URL.Path, "/api/auth/") {
			r.handleAuth(w, req, strings.TrimPrefix(req.URL.Path, "/api/auth/"))
			return
		}
		if strings.HasPrefix(req.URL.Path, "/ws/tasks/") {
			r.handleTaskWebSocket(w, req, strings.TrimPrefix(req.URL.Path, "/ws/tasks/"))
			return
		}
		if strings.HasPrefix(req.URL.Path, "/ws/terminal-sessions/") {
			r.handleTerminalWebSocket(w, req, strings.TrimPrefix(req.URL.Path, "/ws/terminal-sessions/"))
			return
		}
		if req.URL.Path == "/api/terminal-sessions" {
			r.handleTerminalSessions(w, req)
			return
		}
		if strings.HasPrefix(req.URL.Path, "/api/terminal-sessions/") {
			r.handleTerminalSessionByID(w, req, strings.TrimPrefix(req.URL.Path, "/api/terminal-sessions/"))
			return
		}
		if strings.HasPrefix(req.URL.Path, "/api/terminal-profiles/") {
			r.handleTerminalProfileByID(w, req, strings.TrimPrefix(req.URL.Path, "/api/terminal-profiles/"))
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
		if strings.HasPrefix(req.URL.Path, "/api/skills/") {
			r.handleSkillByID(w, req, strings.TrimPrefix(req.URL.Path, "/api/skills/"))
			return
		}
		if strings.HasPrefix(req.URL.Path, "/api/workspaces/") {
			r.handleWorkspaceByID(w, req, strings.TrimPrefix(req.URL.Path, "/api/workspaces/"))
			return
		}
		WriteError(req.Context(), w, http.StatusNotFound, "not found")
	}
}

func (r *Router) skillStore() *skill.Store {
	return r.skills
}

func (r *Router) isPublicRoute(path string) bool {
	if path == "/api/health" || path == "/api/auth/status" || path == "/api/auth/unlock" {
		return true
	}
	return false
}

func (r *Router) requireAuth(w http.ResponseWriter, req *http.Request) bool {
	token := authTokenFromRequest(req)
	if token == "" {
		WriteError(req.Context(), w, http.StatusUnauthorized, "auth required")
		return false
	}
	valid, err := r.authStore().ValidateToken(req.Context(), token)
	if err != nil {
		if errors.Is(err, auth.ErrNotConfigured) {
			return true
		}
		r.logger.ErrorContext(req.Context(), "validate auth token", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "validate auth failed")
		return false
	}
	if !valid {
		WriteError(req.Context(), w, http.StatusUnauthorized, "auth required")
		return false
	}
	return true
}

func (r *Router) authStore() *auth.Store {
	return r.auth
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

func (r *Router) recoverInterruptedRunningTurns(ctx context.Context) {
	items, err := task.NewStore(r.db).ListTurnsByStatus(ctx, task.TurnStatusRunning)
	if err != nil {
		r.logger.Error("recover running turns", "error", err)
		return
	}
	for _, item := range items {
		if _, err := task.NewStore(r.db).UpdateTurnStatus(ctx, item.ID, task.TurnStatusInterrupted); err != nil {
			r.logger.Error("mark stale running turn interrupted", "turnId", item.ID, "error", err)
			continue
		}
		payload := fmt.Sprintf(`{"reason":"backend_restarted","message":"若水后端已重启，上一轮运行已自动暂停。可继续上一轮结果重新推进。","canContinue":true,"continuationPrompt":"继续上一轮任务，先确认已经完成的结果，再接着推进剩余工作，不要重复已完成内容。"}`)
		if _, err := r.appendTaskEvent(ctx, event.AppendInput{
			RequestID:   "backend-restart-recovery",
			WorkspaceID: item.WorkspaceID,
			TaskID:      item.TaskID,
			TurnID:      item.ID,
			Type:        "turn.interrupted",
			PayloadJSON: payload,
		}); err != nil {
			r.logger.Error("append stale turn recovery event", "turnId", item.ID, "error", err)
		}
	}
}
