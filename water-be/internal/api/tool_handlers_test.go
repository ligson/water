package api

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/ligson/water/water-be/internal/config"
)

func TestTaskToolReadFileInWorkspace(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "hello.txt")
	if err := os.WriteFile(filePath, []byte("water"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Water", root, "request_approval")
	task := createTaskForTest(t, handler, ws.ID, "Tool read")

	rec := performJSON(handler, http.MethodPost, "/api/tasks/"+task.ID+"/tools", `{
		"name": "read_file",
		"arguments": {"path": "`+filePath+`"}
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var envelope toolEnvelope
	decodeTestEnvelope(t, rec, &envelope)
	content := envelope.Data.Output["content"]
	if content != "water" {
		t.Fatalf("expected file content water, got %v", content)
	}
}

func TestTaskToolWriteFileApprovalFlow(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "out.txt")

	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Water", root, "request_approval")
	task := createTaskForTest(t, handler, ws.ID, "Tool write")

	rec := performJSON(handler, http.MethodPost, "/api/tasks/"+task.ID+"/tools", `{
		"name": "write_file",
		"arguments": {"path": "`+filePath+`", "content": "ok"}
	}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d: %s", rec.Code, rec.Body.String())
	}

	var pending toolEnvelope
	decodeTestEnvelope(t, rec, &pending)
	if pending.Data.Approval == nil || pending.Data.Approval.Status != "pending" {
		t.Fatalf("expected pending approval, got %#v", pending.Data.Approval)
	}
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Fatalf("file should not be written before approval")
	}

	resolveRec := performJSON(handler, http.MethodPost, "/api/approvals/"+pending.Data.Approval.ID+"/resolve", `{
		"status": "approved",
		"message": "ok"
	}`)
	if resolveRec.Code != http.StatusOK {
		t.Fatalf("expected approve status 200, got %d: %s", resolveRec.Code, resolveRec.Body.String())
	}

	runRec := performJSON(handler, http.MethodPost, "/api/tasks/"+task.ID+"/tools", `{
		"name": "write_file",
		"approvalId": "`+pending.Data.Approval.ID+`",
		"arguments": {"path": "`+filePath+`", "content": "ok"}
	}`)
	if runRec.Code != http.StatusOK {
		t.Fatalf("expected approved write status 200, got %d: %s", runRec.Code, runRec.Body.String())
	}
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(content) != "ok" {
		t.Fatalf("expected written content ok, got %q", string(content))
	}
}

func TestTaskToolWriteFileFullAccess(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "auto.txt")

	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Water", root, "full_access")
	task := createTaskForTest(t, handler, ws.ID, "Tool write full access")

	rec := performJSON(handler, http.MethodPost, "/api/tasks/"+task.ID+"/tools", `{
		"name": "write_file",
		"arguments": {"path": "`+filePath+`", "content": "auto"}
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(content) != "auto" {
		t.Fatalf("expected written content auto, got %q", string(content))
	}
}

func TestTaskToolRunCommandApprovalFlow(t *testing.T) {
	root := t.TempDir()

	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Water", root, "request_approval")
	task := createTaskForTest(t, handler, ws.ID, "Tool command")

	rec := performJSON(handler, http.MethodPost, "/api/tasks/"+task.ID+"/tools", `{
		"name": "run_command",
		"arguments": {"command": "printf water", "workingDir": "`+root+`"}
	}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d: %s", rec.Code, rec.Body.String())
	}

	var pending toolEnvelope
	decodeTestEnvelope(t, rec, &pending)
	if pending.Data.Approval == nil {
		t.Fatalf("expected pending approval")
	}

	resolveRec := performJSON(handler, http.MethodPost, "/api/approvals/"+pending.Data.Approval.ID+"/resolve", `{
		"status": "approved",
		"message": "ok"
	}`)
	if resolveRec.Code != http.StatusOK {
		t.Fatalf("expected approve status 200, got %d: %s", resolveRec.Code, resolveRec.Body.String())
	}

	runRec := performJSON(handler, http.MethodPost, "/api/tasks/"+task.ID+"/tools", `{
		"name": "run_command",
		"approvalId": "`+pending.Data.Approval.ID+`",
		"arguments": {"command": "printf water", "workingDir": "`+root+`"}
	}`)
	if runRec.Code != http.StatusOK {
		t.Fatalf("expected command status 200, got %d: %s", runRec.Code, runRec.Body.String())
	}

	var output toolEnvelope
	decodeTestEnvelope(t, runRec, &output)
	result := output.Data.Output
	if result["output"] != "water" {
		t.Fatalf("expected command output water, got %#v", result["output"])
	}
}

func TestTaskToolRejectsExternalPathWithoutAuthorization(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside fixture: %v", err)
	}

	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Water", root, "request_approval")
	task := createTaskForTest(t, handler, ws.ID, "Tool deny")

	rec := performJSON(handler, http.MethodPost, "/api/tasks/"+task.ID+"/tools", `{
		"name": "read_file",
		"arguments": {"path": "`+outside+`"}
	}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

type toolEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		Name     string                     `json:"name"`
		Approved bool                       `json:"approved"`
		Output   map[string]interface{}     `json:"output"`
		Approval *approvalResponseForAssert `json:"approval"`
	} `json:"data"`
}

type approvalResponseForAssert struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}
