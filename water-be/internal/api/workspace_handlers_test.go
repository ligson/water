package api

import (
	"archive/zip"
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ligson/water/water-be/internal/config"
)

func TestWorkspaceCRUD(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	created := createWorkspaceForTest(t, handler, "Water", "/tmp/water", "request_approval")
	if created.Name != "Water" {
		t.Fatalf("expected workspace name Water, got %q", created.Name)
	}
	if created.PermissionMode != "request_approval" {
		t.Fatalf("expected request_approval, got %q", created.PermissionMode)
	}

	updateBody := `{
		"name": "Water Updated",
		"rootPath": "/tmp/water-updated",
		"permissionMode": "full_access",
		"trusted": true
	}`
	updateRec := performJSON(handler, http.MethodPut, "/api/workspaces/"+created.ID, updateBody)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", updateRec.Code, updateRec.Body.String())
	}

	var updated workspaceEnvelope
	decodeTestEnvelope(t, updateRec, &updated)
	if updated.Data.Name != "Water Updated" {
		t.Fatalf("expected updated name, got %q", updated.Data.Name)
	}
	if updated.Data.PermissionMode != "full_access" {
		t.Fatalf("expected full_access, got %q", updated.Data.PermissionMode)
	}

	listRec := performJSON(handler, http.MethodGet, "/api/workspaces", "")
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", listRec.Code)
	}
	var listed workspaceListEnvelope
	decodeTestEnvelope(t, listRec, &listed)
	if len(listed.Data.Items) != 1 {
		t.Fatalf("expected one workspace, got %d", len(listed.Data.Items))
	}

	deleteRec := performJSON(handler, http.MethodDelete, "/api/workspaces/"+created.ID, "")
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", deleteRec.Code)
	}
}

func TestWorkspaceRequiresAbsoluteRootPath(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := `{
		"name": "Bad",
		"rootPath": "relative/path",
		"permissionMode": "request_approval"
	}`
	rec := performJSON(handler, http.MethodPost, "/api/workspaces", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}
}

func TestWorkspaceListEmptyArray(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rec := performJSON(handler, http.MethodGet, "/api/workspaces", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var listed workspaceListEnvelope
	decodeTestEnvelope(t, rec, &listed)
	if listed.Data.Items == nil {
		t.Fatalf("expected empty array, got nil")
	}
	if len(listed.Data.Items) != 0 {
		t.Fatalf("expected zero workspaces, got %d", len(listed.Data.Items))
	}
}

func TestWorkspaceExternalPaths(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Water", "/tmp/water", "request_approval")

	createBody := `{
		"path": "/tmp/external",
		"pathType": "directory",
		"accessMode": "write"
	}`
	createRec := performJSON(handler, http.MethodPost, "/api/workspaces/"+ws.ID+"/external-paths", createBody)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", createRec.Code, createRec.Body.String())
	}
	var created externalPathEnvelope
	decodeTestEnvelope(t, createRec, &created)
	if created.Data.Path != "/tmp/external" {
		t.Fatalf("expected /tmp/external, got %q", created.Data.Path)
	}
	if created.Data.AccessMode != "write" {
		t.Fatalf("expected write, got %q", created.Data.AccessMode)
	}
	if strings.Contains(createRec.Body.String(), "0001-01-01") {
		t.Fatalf("response should omit zero lastUsedAt: %s", createRec.Body.String())
	}

	listRec := performJSON(handler, http.MethodGet, "/api/workspaces/"+ws.ID+"/external-paths", "")
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", listRec.Code)
	}
	var listed externalPathListEnvelope
	decodeTestEnvelope(t, listRec, &listed)
	if len(listed.Data.Items) != 1 {
		t.Fatalf("expected one external path, got %d", len(listed.Data.Items))
	}

	deleteRec := performJSON(handler, http.MethodDelete, "/api/workspaces/"+ws.ID+"/external-paths/"+created.Data.ID, "")
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", deleteRec.Code)
	}
}

func TestWorkspaceExternalPathValidation(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Water", "/tmp/water", "request_approval")

	body := `{
		"path": "relative",
		"pathType": "directory",
		"accessMode": "read"
	}`
	rec := performJSON(handler, http.MethodPost, "/api/workspaces/"+ws.ID+"/external-paths", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}
}

func TestWorkspaceFilesListAndRead(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Water\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "guide.md"), []byte("guide"), 0o644); err != nil {
		t.Fatalf("write guide: %v", err)
	}

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Water", root, "request_approval")

	listRec := performJSON(handler, http.MethodGet, "/api/workspaces/"+ws.ID+"/files", "")
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", listRec.Code, listRec.Body.String())
	}
	var listed workspaceFileListEnvelope
	decodeTestEnvelope(t, listRec, &listed)
	if len(listed.Data.Items) != 2 {
		t.Fatalf("expected two items, got %d", len(listed.Data.Items))
	}
	if !listed.Data.Items[0].IsDir || listed.Data.Items[0].Name != "docs" {
		t.Fatalf("expected directory first, got %#v", listed.Data.Items[0])
	}

	readRec := performJSON(handler, http.MethodGet, "/api/workspaces/"+ws.ID+"/files/content?path=README.md", "")
	if readRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", readRec.Code, readRec.Body.String())
	}
	var content workspaceFileContentEnvelope
	decodeTestEnvelope(t, readRec, &content)
	if content.Data.Content != "# Water\n" {
		t.Fatalf("expected file content, got %q", content.Data.Content)
	}
}

func TestWorkspaceFileAndArchiveDownload(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "app.java"), []byte("class App {}\n"), 0o644); err != nil {
		t.Fatalf("write app: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Demo\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Water Demo", root, "request_approval")

	fileRec := performJSON(handler, http.MethodGet, "/api/workspaces/"+ws.ID+"/files/download?path=src/app.java", "")
	if fileRec.Code != http.StatusOK {
		t.Fatalf("expected file download 200, got %d: %s", fileRec.Code, fileRec.Body.String())
	}
	if fileRec.Body.String() != "class App {}\n" {
		t.Fatalf("unexpected file download body %q", fileRec.Body.String())
	}
	if !strings.Contains(fileRec.Header().Get("Content-Disposition"), "app.java") {
		t.Fatalf("expected attachment filename, got %q", fileRec.Header().Get("Content-Disposition"))
	}

	archiveRec := performJSON(handler, http.MethodGet, "/api/workspaces/"+ws.ID+"/archive", "")
	if archiveRec.Code != http.StatusOK {
		t.Fatalf("expected archive download 200, got %d: %s", archiveRec.Code, archiveRec.Body.String())
	}
	reader, err := zip.NewReader(bytes.NewReader(archiveRec.Body.Bytes()), int64(archiveRec.Body.Len()))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	entries := make(map[string]bool)
	for _, file := range reader.File {
		entries[file.Name] = true
	}
	for _, expected := range []string{"README.md", "src/app.java"} {
		if !entries[expected] {
			t.Fatalf("expected archive entry %q, got %#v", expected, entries)
		}
	}
}

func TestWorkspaceFilesRejectPathEscape(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Water", t.TempDir(), "request_approval")

	rec := performJSON(handler, http.MethodGet, "/api/workspaces/"+ws.ID+"/files?path=../", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func createWorkspaceForTest(t *testing.T, handler http.Handler, name string, rootPath string, permissionMode string) workspaceResponse {
	t.Helper()

	return createWorkspaceForTestWithProvider(t, handler, name, rootPath, permissionMode, "")
}

func createWorkspaceForTestWithProvider(t *testing.T, handler http.Handler, name string, rootPath string, permissionMode string, providerID string) workspaceResponse {
	t.Helper()

	body := `{
		"name": "` + name + `",
		"rootPath": "` + rootPath + `",
		"defaultProviderId": "` + providerID + `",
		"permissionMode": "` + permissionMode + `",
		"trusted": true
	}`
	rec := performJSON(handler, http.MethodPost, "/api/workspaces", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create workspace: status %d body %s", rec.Code, rec.Body.String())
	}
	var envelope workspaceEnvelope
	decodeTestEnvelope(t, rec, &envelope)
	return envelope.Data
}

type workspaceResponse struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	RootPath          string `json:"rootPath"`
	DefaultProviderID string `json:"defaultProviderId"`
	PermissionMode    string `json:"permissionMode"`
	Trusted           bool   `json:"trusted"`
}

type workspaceEnvelope struct {
	Success bool              `json:"success"`
	Data    workspaceResponse `json:"data"`
}

type workspaceListEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		Items []workspaceResponse `json:"items"`
	} `json:"data"`
}

type externalPathResponse struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	Path        string `json:"path"`
	PathType    string `json:"pathType"`
	AccessMode  string `json:"accessMode"`
}

type externalPathEnvelope struct {
	Success bool                 `json:"success"`
	Data    externalPathResponse `json:"data"`
}

type externalPathListEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		Items []externalPathResponse `json:"items"`
	} `json:"data"`
}

type workspaceFileResponse struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	IsDir      bool   `json:"isDir"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modifiedAt"`
}

type workspaceFileListEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		Path  string                  `json:"path"`
		Items []workspaceFileResponse `json:"items"`
	} `json:"data"`
}

type workspaceFileContentResponse struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Size      int64  `json:"size"`
	Truncated bool   `json:"truncated"`
}

type workspaceFileContentEnvelope struct {
	Success bool                         `json:"success"`
	Data    workspaceFileContentResponse `json:"data"`
}
