package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/ligson/water/water-be/internal/workspace"
)

type workspaceRequest struct {
	Name              string `json:"name"`
	RootPath          string `json:"rootPath"`
	DefaultProviderID string `json:"defaultProviderId"`
	PermissionMode    string `json:"permissionMode"`
	Trusted           bool   `json:"trusted"`
}

type externalPathRequest struct {
	Path         string `json:"path"`
	PathType     string `json:"pathType"`
	AccessMode   string `json:"accessMode"`
	SourceTaskID string `json:"sourceTaskId"`
}

func (r *Router) handleWorkspaces(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		r.listWorkspaces(w, req)
	case http.MethodPost:
		r.createWorkspace(w, req)
	default:
		WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (r *Router) handleWorkspaceByID(w http.ResponseWriter, req *http.Request, rest string) {
	id, action, actionID, ok := splitWorkspacePath(rest)
	if !ok {
		WriteError(req.Context(), w, http.StatusNotFound, "not found")
		return
	}

	if action == "external-paths" {
		if actionID == "" {
			r.handleWorkspaceExternalPaths(w, req, id)
			return
		}
		if req.Method != http.MethodDelete {
			WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		r.deleteWorkspaceExternalPath(w, req, id, actionID)
		return
	}

	if action == "tasks" {
		if actionID != "" {
			WriteError(req.Context(), w, http.StatusNotFound, "not found")
			return
		}
		r.handleWorkspaceTasks(w, req, id)
		return
	}

	if action == "approvals" {
		if actionID != "" {
			WriteError(req.Context(), w, http.StatusNotFound, "not found")
			return
		}
		r.listWorkspaceApprovals(w, req, id)
		return
	}

	switch req.Method {
	case http.MethodGet:
		r.getWorkspace(w, req, id)
	case http.MethodPut:
		r.updateWorkspace(w, req, id)
	case http.MethodDelete:
		r.deleteWorkspace(w, req, id)
	default:
		WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (r *Router) handleWorkspaceExternalPaths(w http.ResponseWriter, req *http.Request, workspaceID string) {
	switch req.Method {
	case http.MethodGet:
		r.listWorkspaceExternalPaths(w, req, workspaceID)
	case http.MethodPost:
		r.createWorkspaceExternalPath(w, req, workspaceID)
	default:
		WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (r *Router) listWorkspaces(w http.ResponseWriter, req *http.Request) {
	items, err := workspace.NewStore(r.db).List(req.Context())
	if err != nil {
		r.logger.ErrorContext(req.Context(), "list workspaces", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "list workspaces failed")
		return
	}
	WriteOK(req.Context(), w, "ok", map[string]interface{}{"items": items})
}

func (r *Router) createWorkspace(w http.ResponseWriter, req *http.Request) {
	input, ok := decodeWorkspaceRequest(w, req)
	if !ok {
		return
	}

	created, err := workspace.NewStore(r.db).Create(req.Context(), workspace.CreateInput{
		Name:              input.Name,
		RootPath:          input.RootPath,
		DefaultProviderID: input.DefaultProviderID,
		PermissionMode:    input.PermissionMode,
		Trusted:           input.Trusted,
	})
	if err != nil {
		r.logger.ErrorContext(req.Context(), "create workspace", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "create workspace failed")
		return
	}
	WriteJSON(req.Context(), w, http.StatusCreated, true, "workspace created", created)
}

func (r *Router) getWorkspace(w http.ResponseWriter, req *http.Request, id string) {
	item, err := workspace.NewStore(r.db).Get(req.Context(), id)
	if errors.Is(err, workspace.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "workspace not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "get workspace", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "get workspace failed")
		return
	}
	WriteOK(req.Context(), w, "ok", item)
}

func (r *Router) updateWorkspace(w http.ResponseWriter, req *http.Request, id string) {
	input, ok := decodeWorkspaceRequest(w, req)
	if !ok {
		return
	}

	updated, err := workspace.NewStore(r.db).Update(req.Context(), id, workspace.UpdateInput{
		Name:              input.Name,
		RootPath:          input.RootPath,
		DefaultProviderID: input.DefaultProviderID,
		PermissionMode:    input.PermissionMode,
		Trusted:           input.Trusted,
	})
	if errors.Is(err, workspace.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "workspace not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "update workspace", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "update workspace failed")
		return
	}
	WriteOK(req.Context(), w, "workspace updated", updated)
}

func (r *Router) deleteWorkspace(w http.ResponseWriter, req *http.Request, id string) {
	err := workspace.NewStore(r.db).Delete(req.Context(), id)
	if errors.Is(err, workspace.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "workspace not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "delete workspace", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "delete workspace failed")
		return
	}
	WriteOK(req.Context(), w, "workspace deleted", map[string]interface{}{})
}

func (r *Router) listWorkspaceExternalPaths(w http.ResponseWriter, req *http.Request, workspaceID string) {
	items, err := workspace.NewStore(r.db).ListExternalPaths(req.Context(), workspaceID)
	if errors.Is(err, workspace.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "workspace not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "list external paths", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "list external paths failed")
		return
	}
	WriteOK(req.Context(), w, "ok", map[string]interface{}{"items": items})
}

func (r *Router) createWorkspaceExternalPath(w http.ResponseWriter, req *http.Request, workspaceID string) {
	input, ok := decodeExternalPathRequest(w, req)
	if !ok {
		return
	}

	created, err := workspace.NewStore(r.db).CreateExternalPath(req.Context(), workspaceID, workspace.CreateExternalPathInput{
		Path:         input.Path,
		PathType:     input.PathType,
		AccessMode:   input.AccessMode,
		SourceTaskID: input.SourceTaskID,
	})
	if errors.Is(err, workspace.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "workspace not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "create external path", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "create external path failed")
		return
	}
	WriteJSON(req.Context(), w, http.StatusCreated, true, "external path authorized", created)
}

func (r *Router) deleteWorkspaceExternalPath(w http.ResponseWriter, req *http.Request, workspaceID string, pathID string) {
	err := workspace.NewStore(r.db).DeleteExternalPath(req.Context(), workspaceID, pathID)
	if errors.Is(err, workspace.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "external path not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "delete external path", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "delete external path failed")
		return
	}
	WriteOK(req.Context(), w, "external path removed", map[string]interface{}{})
}

func decodeWorkspaceRequest(w http.ResponseWriter, req *http.Request) (workspaceRequest, bool) {
	defer req.Body.Close()

	var input workspaceRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		WriteError(req.Context(), w, http.StatusBadRequest, "invalid json body")
		return workspaceRequest{}, false
	}

	input.Name = strings.TrimSpace(input.Name)
	input.RootPath = filepath.Clean(strings.TrimSpace(input.RootPath))
	input.DefaultProviderID = strings.TrimSpace(input.DefaultProviderID)
	input.PermissionMode = strings.TrimSpace(input.PermissionMode)

	if input.Name == "" {
		WriteError(req.Context(), w, http.StatusBadRequest, "name is required")
		return workspaceRequest{}, false
	}
	if input.RootPath == "" || input.RootPath == "." {
		WriteError(req.Context(), w, http.StatusBadRequest, "rootPath is required")
		return workspaceRequest{}, false
	}
	if !filepath.IsAbs(input.RootPath) {
		WriteError(req.Context(), w, http.StatusBadRequest, "rootPath must be absolute")
		return workspaceRequest{}, false
	}
	if input.PermissionMode == "" {
		input.PermissionMode = workspace.PermissionModeRequestApproval
	}
	if !validPermissionMode(input.PermissionMode) {
		WriteError(req.Context(), w, http.StatusBadRequest, "unsupported permissionMode")
		return workspaceRequest{}, false
	}

	return input, true
}

func decodeExternalPathRequest(w http.ResponseWriter, req *http.Request) (externalPathRequest, bool) {
	defer req.Body.Close()

	var input externalPathRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		WriteError(req.Context(), w, http.StatusBadRequest, "invalid json body")
		return externalPathRequest{}, false
	}

	input.Path = filepath.Clean(strings.TrimSpace(input.Path))
	input.PathType = strings.TrimSpace(input.PathType)
	input.AccessMode = strings.TrimSpace(input.AccessMode)
	input.SourceTaskID = strings.TrimSpace(input.SourceTaskID)

	if input.Path == "" || input.Path == "." {
		WriteError(req.Context(), w, http.StatusBadRequest, "path is required")
		return externalPathRequest{}, false
	}
	if !filepath.IsAbs(input.Path) {
		WriteError(req.Context(), w, http.StatusBadRequest, "path must be absolute")
		return externalPathRequest{}, false
	}
	if !validPathType(input.PathType) {
		WriteError(req.Context(), w, http.StatusBadRequest, "unsupported pathType")
		return externalPathRequest{}, false
	}
	if !validAccessMode(input.AccessMode) {
		WriteError(req.Context(), w, http.StatusBadRequest, "unsupported accessMode")
		return externalPathRequest{}, false
	}

	return input, true
}

func splitWorkspacePath(rest string) (id string, action string, actionID string, ok bool) {
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return "", "", "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) == 1 {
		return parts[0], "", "", true
	}
	if len(parts) == 2 && parts[1] == "external-paths" {
		return parts[0], parts[1], "", true
	}
	if len(parts) == 2 && parts[1] == "tasks" {
		return parts[0], parts[1], "", true
	}
	if len(parts) == 2 && parts[1] == "approvals" {
		return parts[0], parts[1], "", true
	}
	if len(parts) == 3 && parts[1] == "external-paths" {
		return parts[0], parts[1], parts[2], true
	}
	return "", "", "", false
}

func validPermissionMode(value string) bool {
	return value == workspace.PermissionModeRequestApproval || value == workspace.PermissionModeFullAccess
}

func validPathType(value string) bool {
	return value == workspace.PathTypeFile || value == workspace.PathTypeDirectory
}

func validAccessMode(value string) bool {
	return value == workspace.AccessModeRead || value == workspace.AccessModeWrite
}
