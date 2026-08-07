package api

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/ligson/water/water-be/internal/config"
)

func TestTerminalProfileCRUDAndSession(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Water", "/tmp/water", "request_approval")

	createBody := `{
		"name": "Dev Server",
		"host": "127.0.0.1",
		"port": 22,
		"username": "water",
		"authType": "password",
		"password": "secret",
		"defaultCwd": "/srv/water",
		"enabled": true
	}`
	createRec := performJSON(handler, http.MethodPost, "/api/workspaces/"+ws.ID+"/terminal-profiles", createBody)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected create profile 201, got %d: %s", createRec.Code, createRec.Body.String())
	}
	if strings.Contains(createRec.Body.String(), "secret") {
		t.Fatalf("terminal profile response should not expose password: %s", createRec.Body.String())
	}
	var created terminalProfileEnvelope
	decodeTestEnvelope(t, createRec, &created)
	if !created.Data.PasswordConfigured {
		t.Fatalf("expected passwordConfigured=true, got %#v", created.Data)
	}

	listRec := performJSON(handler, http.MethodGet, "/api/workspaces/"+ws.ID+"/terminal-profiles", "")
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected list profiles 200, got %d: %s", listRec.Code, listRec.Body.String())
	}
	var listed terminalProfileListEnvelope
	decodeTestEnvelope(t, listRec, &listed)
	if len(listed.Data.Items) != 1 || listed.Data.Items[0].ID != created.Data.ID {
		t.Fatalf("expected created profile in list, got %#v", listed.Data.Items)
	}

	sessionBody := `{"workspaceId":"` + ws.ID + `","cols":120,"rows":34}`
	sessionRec := performJSON(handler, http.MethodPost, "/api/terminal-sessions", sessionBody)
	if sessionRec.Code != http.StatusCreated {
		t.Fatalf("expected create session 201, got %d: %s", sessionRec.Code, sessionRec.Body.String())
	}
	var session terminalSessionEnvelope
	decodeTestEnvelope(t, sessionRec, &session)
	if session.Data.Cwd != "/tmp/water" {
		t.Fatalf("expected default cwd, got %q", session.Data.Cwd)
	}
	if session.Data.Cols != 120 || session.Data.Rows != 34 {
		t.Fatalf("expected session size 120x34, got %dx%d", session.Data.Cols, session.Data.Rows)
	}

	deleteRec := performJSON(handler, http.MethodDelete, "/api/terminal-profiles/"+created.Data.ID, "")
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("expected delete profile 200, got %d: %s", deleteRec.Code, deleteRec.Body.String())
	}
}

func TestTerminalProfileValidation(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Water", "/tmp/water", "request_approval")

	rec := performJSON(handler, http.MethodPost, "/api/workspaces/"+ws.ID+"/terminal-profiles", `{"name":"bad","host":"127.0.0.1","username":"water","authType":"ssh_agent"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected unsupported auth type 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

type terminalProfileResponse struct {
	ID                   string `json:"id"`
	WorkspaceID          string `json:"workspaceId"`
	Name                 string `json:"name"`
	Host                 string `json:"host"`
	Port                 int    `json:"port"`
	Username             string `json:"username"`
	AuthType             string `json:"authType"`
	PasswordConfigured   bool   `json:"passwordConfigured"`
	PrivateKeyConfigured bool   `json:"privateKeyConfigured"`
	DefaultCwd           string `json:"defaultCwd"`
	Enabled              bool   `json:"enabled"`
}

type terminalProfileEnvelope struct {
	Success bool                    `json:"success"`
	Data    terminalProfileResponse `json:"data"`
}

type terminalProfileListEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		Items []terminalProfileResponse `json:"items"`
	} `json:"data"`
}

type terminalSessionResponse struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	ProfileID   string `json:"profileId"`
	Status      string `json:"status"`
	Cwd         string `json:"cwd"`
	Cols        int    `json:"cols"`
	Rows        int    `json:"rows"`
}

type terminalSessionEnvelope struct {
	Success bool                    `json:"success"`
	Data    terminalSessionResponse `json:"data"`
}
