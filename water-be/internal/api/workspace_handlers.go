package api

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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

type workspaceFileItem struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	IsDir      bool   `json:"isDir"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modifiedAt"`
}

type workspaceFileContent struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Size      int64  `json:"size"`
	Truncated bool   `json:"truncated"`
}

const workspaceFilePreviewLimit = 512 * 1024

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

	if action == "files" {
		if req.Method != http.MethodGet {
			WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if actionID == "" {
			r.listWorkspaceFiles(w, req, id)
			return
		}
		if actionID == "content" {
			r.readWorkspaceFile(w, req, id)
			return
		}
		if actionID == "download" {
			r.downloadWorkspaceFile(w, req, id)
			return
		}
		WriteError(req.Context(), w, http.StatusNotFound, "not found")
		return
	}

	if action == "archive" {
		if actionID != "" {
			WriteError(req.Context(), w, http.StatusNotFound, "not found")
			return
		}
		if req.Method != http.MethodGet {
			WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		r.downloadWorkspaceArchive(w, req, id)
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

func (r *Router) listWorkspaceFiles(w http.ResponseWriter, req *http.Request, workspaceID string) {
	ws, err := workspace.NewStore(r.db).Get(req.Context(), workspaceID)
	if errors.Is(err, workspace.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "workspace not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "get workspace for files", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "get workspace failed")
		return
	}

	relPath, absPath, err := resolveWorkspaceFilePath(ws.RootPath, req.URL.Query().Get("path"))
	if err != nil {
		WriteError(req.Context(), w, http.StatusBadRequest, err.Error())
		return
	}

	info, err := os.Stat(absPath)
	if err != nil {
		WriteError(req.Context(), w, http.StatusNotFound, "path not found")
		return
	}
	if !info.IsDir() {
		WriteError(req.Context(), w, http.StatusBadRequest, "path is not a directory")
		return
	}

	entries, err := os.ReadDir(absPath)
	if err != nil {
		r.logger.ErrorContext(req.Context(), "read workspace directory", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "read directory failed")
		return
	}

	items := make([]workspaceFileItem, 0, len(entries))
	for _, entry := range entries {
		entryInfo, err := entry.Info()
		if err != nil {
			continue
		}
		itemRelPath := filepath.Join(relPath, entry.Name())
		if relPath == "." || relPath == "" {
			itemRelPath = entry.Name()
		}
		items = append(items, workspaceFileItem{
			Name:       entry.Name(),
			Path:       filepath.ToSlash(itemRelPath),
			IsDir:      entry.IsDir(),
			Size:       entryInfo.Size(),
			ModifiedAt: entryInfo.ModTime().Format(time.RFC3339),
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].IsDir != items[j].IsDir {
			return items[i].IsDir
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})

	WriteOK(req.Context(), w, "ok", map[string]interface{}{
		"path":  filepath.ToSlash(relPath),
		"items": items,
	})
}

func (r *Router) readWorkspaceFile(w http.ResponseWriter, req *http.Request, workspaceID string) {
	ws, err := workspace.NewStore(r.db).Get(req.Context(), workspaceID)
	if errors.Is(err, workspace.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "workspace not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "get workspace for file content", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "get workspace failed")
		return
	}

	relPath, absPath, err := resolveWorkspaceFilePath(ws.RootPath, req.URL.Query().Get("path"))
	if err != nil {
		WriteError(req.Context(), w, http.StatusBadRequest, err.Error())
		return
	}

	info, err := os.Stat(absPath)
	if err != nil {
		WriteError(req.Context(), w, http.StatusNotFound, "file not found")
		return
	}
	if info.IsDir() {
		WriteError(req.Context(), w, http.StatusBadRequest, "path is a directory")
		return
	}

	file, err := os.Open(absPath)
	if err != nil {
		r.logger.ErrorContext(req.Context(), "open workspace file", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "open file failed")
		return
	}
	defer file.Close()

	raw, err := io.ReadAll(io.LimitReader(file, workspaceFilePreviewLimit+1))
	if err != nil {
		r.logger.ErrorContext(req.Context(), "read workspace file", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "read file failed")
		return
	}
	truncated := len(raw) > workspaceFilePreviewLimit
	if truncated {
		raw = raw[:workspaceFilePreviewLimit]
	}

	WriteOK(req.Context(), w, "ok", workspaceFileContent{
		Path:      filepath.ToSlash(relPath),
		Content:   string(raw),
		Size:      info.Size(),
		Truncated: truncated,
	})
}

func (r *Router) downloadWorkspaceFile(w http.ResponseWriter, req *http.Request, workspaceID string) {
	ws, err := workspace.NewStore(r.db).Get(req.Context(), workspaceID)
	if errors.Is(err, workspace.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "workspace not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "get workspace for file download", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "get workspace failed")
		return
	}

	relPath, absPath, err := resolveWorkspaceFilePath(ws.RootPath, req.URL.Query().Get("path"))
	if err != nil {
		WriteError(req.Context(), w, http.StatusBadRequest, err.Error())
		return
	}

	info, err := os.Stat(absPath)
	if err != nil {
		WriteError(req.Context(), w, http.StatusNotFound, "file not found")
		return
	}
	if info.IsDir() {
		WriteError(req.Context(), w, http.StatusBadRequest, "path is a directory")
		return
	}

	file, err := os.Open(absPath)
	if err != nil {
		r.logger.ErrorContext(req.Context(), "open workspace file download", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "open file failed")
		return
	}
	defer file.Close()

	w.Header().Set("Content-Disposition", contentDispositionAttachment(filepath.Base(relPath)))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	http.ServeContent(w, req, filepath.Base(relPath), info.ModTime(), file)
}

func (r *Router) downloadWorkspaceArchive(w http.ResponseWriter, req *http.Request, workspaceID string) {
	ws, err := workspace.NewStore(r.db).Get(req.Context(), workspaceID)
	if errors.Is(err, workspace.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "workspace not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "get workspace for archive", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "get workspace failed")
		return
	}

	rootPath, err := filepath.EvalSymlinks(filepath.Clean(ws.RootPath))
	if err != nil {
		WriteError(req.Context(), w, http.StatusNotFound, "workspace root not found")
		return
	}
	rootInfo, err := os.Stat(rootPath)
	if err != nil {
		WriteError(req.Context(), w, http.StatusNotFound, "workspace root not found")
		return
	}
	if !rootInfo.IsDir() {
		WriteError(req.Context(), w, http.StatusBadRequest, "workspace root is not a directory")
		return
	}

	archiveName := safeDownloadName(ws.Name)
	if archiveName == "" {
		archiveName = "workspace"
	}
	w.Header().Set("Content-Disposition", contentDispositionAttachment(archiveName+".zip"))
	w.Header().Set("Content-Type", "application/zip")

	zipWriter := zip.NewWriter(w)
	defer zipWriter.Close()
	if err := filepath.WalkDir(rootPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == rootPath {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		relPath, err := filepath.Rel(rootPath, path)
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relPath)
		header.Method = zip.Deflate
		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}); err != nil {
		r.logger.ErrorContext(req.Context(), "create workspace archive", "error", err)
		return
	}
}

func resolveWorkspaceFilePath(rootPath string, requestedPath string) (string, string, error) {
	rootPath = filepath.Clean(rootPath)
	requestedPath = strings.TrimSpace(requestedPath)
	if requestedPath == "" {
		requestedPath = "."
	}
	if filepath.IsAbs(requestedPath) {
		return "", "", fmt.Errorf("path must be relative to workspace")
	}
	relPath := filepath.Clean(requestedPath)
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path escapes workspace")
	}

	absPath := filepath.Join(rootPath, relPath)
	realRoot, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return "", "", fmt.Errorf("workspace root not found")
	}
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", "", fmt.Errorf("path not found")
	}
	innerRel, err := filepath.Rel(realRoot, realPath)
	if err != nil || innerRel == ".." || strings.HasPrefix(innerRel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path escapes workspace")
	}
	return relPath, realPath, nil
}

func contentDispositionAttachment(filename string) string {
	if filename == "" {
		filename = "download"
	}
	fallback := safeDownloadName(filename)
	if fallback == "" {
		fallback = "download"
	}
	return fmt.Sprintf(`attachment; filename=%q; filename*=UTF-8''%s`, fallback, url.PathEscape(filename))
}

func safeDownloadName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "\x00", "")
	return replacer.Replace(name)
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
	if len(parts) == 2 && parts[1] == "files" {
		return parts[0], parts[1], "", true
	}
	if len(parts) == 2 && parts[1] == "archive" {
		return parts[0], parts[1], "", true
	}
	if len(parts) == 3 && parts[1] == "files" {
		return parts[0], parts[1], parts[2], true
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
