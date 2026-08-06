package api

import (
	"io"
	"log/slog"
	"net/http"
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
