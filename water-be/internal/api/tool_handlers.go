package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/ligson/water/water-be/internal/approval"
	"github.com/ligson/water/water-be/internal/event"
	"github.com/ligson/water/water-be/internal/requestid"
	"github.com/ligson/water/water-be/internal/sandbox"
	"github.com/ligson/water/water-be/internal/task"
	"github.com/ligson/water/water-be/internal/tools"
	"github.com/ligson/water/water-be/internal/workspace"
)

type resolveApprovalRequest struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

func (r *Router) executeTaskTool(w http.ResponseWriter, req *http.Request, taskID string) {
	defer req.Body.Close()

	currentTask, err := task.NewStore(r.db).Get(req.Context(), taskID)
	if errors.Is(err, task.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "task not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "get task for tool", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "get task failed")
		return
	}

	ws, err := workspace.NewStore(r.db).Get(req.Context(), currentTask.WorkspaceID)
	if err != nil {
		r.logger.ErrorContext(req.Context(), "get workspace for tool", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "get workspace failed")
		return
	}

	var input tools.Request
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		WriteError(req.Context(), w, http.StatusBadRequest, "invalid json body")
		return
	}
	if input.RequestID == "" {
		input.RequestID = requestid.FromContext(req.Context())
	}

	executor := tools.NewExecutor(sandbox.NewPermissionEngine(workspace.NewStore(r.db)), approval.NewStore(r.db))
	result, err := executor.Execute(req.Context(), tools.Context{
		Workspace: ws,
		Task:      currentTask,
	}, input)
	if errors.Is(err, tools.ErrApprovalRequired) {
		r.appendToolEvent(req, currentTask, "approval.requested", map[string]interface{}{"approval": result.Approval})
		WriteJSON(req.Context(), w, http.StatusAccepted, true, "approval required", result)
		return
	}
	if err != nil {
		r.appendToolEvent(req, currentTask, "tool.failed", map[string]interface{}{"name": input.Name, "message": err.Error()})
		WriteError(req.Context(), w, http.StatusBadRequest, err.Error())
		return
	}

	r.appendToolEvent(req, currentTask, "tool.completed", map[string]interface{}{"name": result.Name, "output": result.Output})
	WriteOK(req.Context(), w, "tool executed", result)
}

func (r *Router) listWorkspaceApprovals(w http.ResponseWriter, req *http.Request, workspaceID string) {
	if req.Method != http.MethodGet {
		WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if _, err := workspace.NewStore(r.db).Get(req.Context(), workspaceID); errors.Is(err, workspace.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "workspace not found")
		return
	} else if err != nil {
		r.logger.ErrorContext(req.Context(), "get workspace for approvals", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "get workspace failed")
		return
	}

	items, err := approval.NewStore(r.db).ListByWorkspace(req.Context(), workspaceID, req.URL.Query().Get("status"))
	if err != nil {
		r.logger.ErrorContext(req.Context(), "list approvals", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "list approvals failed")
		return
	}
	WriteOK(req.Context(), w, "ok", map[string]interface{}{"items": items})
}

func (r *Router) handleApprovalByID(w http.ResponseWriter, req *http.Request, rest string) {
	approvalID, action, ok := splitApprovalPath(rest)
	if !ok || action != "resolve" {
		WriteError(req.Context(), w, http.StatusNotFound, "not found")
		return
	}
	if req.Method != http.MethodPost {
		WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	defer req.Body.Close()
	var input resolveApprovalRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		WriteError(req.Context(), w, http.StatusBadRequest, "invalid json body")
		return
	}
	resolved, err := approval.NewStore(r.db).Resolve(req.Context(), approvalID, input.Status, input.Message)
	if errors.Is(err, approval.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "approval not found or already resolved")
		return
	}
	if err != nil {
		WriteError(req.Context(), w, http.StatusBadRequest, err.Error())
		return
	}

	if resolved.TaskID != "" {
		_, _ = r.appendTaskEvent(req.Context(), event.AppendInput{
			RequestID:   requestid.FromContext(req.Context()),
			WorkspaceID: resolved.WorkspaceID,
			TaskID:      resolved.TaskID,
			TurnID:      resolved.TurnID,
			Type:        "approval.resolved",
			PayloadJSON: mustJSON(map[string]interface{}{"approval": resolved}),
		})
	}

	WriteOK(req.Context(), w, "approval resolved", resolved)
}

func (r *Router) appendToolEvent(req *http.Request, currentTask task.Task, eventType string, payload map[string]interface{}) {
	_, _ = r.appendTaskEvent(req.Context(), event.AppendInput{
		RequestID:   requestid.FromContext(req.Context()),
		WorkspaceID: currentTask.WorkspaceID,
		TaskID:      currentTask.ID,
		Type:        eventType,
		PayloadJSON: mustJSON(payload),
	})
}

func splitApprovalPath(rest string) (id string, action string, ok bool) {
	parts := splitPath(rest)
	if len(parts) == 2 && parts[1] == "resolve" {
		return parts[0], parts[1], true
	}
	return "", "", false
}

func splitPath(rest string) []string {
	var parts []string
	for _, part := range strings.Split(rest, "/") {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func mustJSON(value interface{}) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return `{}`
	}
	return string(raw)
}
