package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ligson/water/water-be/internal/config"
)

func TestSkillUploadEnableAndDeleteAPI(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	handler := NewRouter(db, config.Config{DataDir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	archive := apiSkillArchive(t)

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "review-skill.zip")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(archive); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := form.Close(); err != nil {
		t.Fatalf("close form: %v", err)
	}
	upload := httptest.NewRequest(http.MethodPost, "/api/skills", &body)
	upload.Header.Set("Content-Type", form.FormDataContentType())
	uploadRec := httptest.NewRecorder()
	handler.ServeHTTP(uploadRec, upload)
	if uploadRec.Code != http.StatusCreated {
		t.Fatalf("upload status=%d body=%s", uploadRec.Code, uploadRec.Body.String())
	}

	enableRec := performJSON(handler, http.MethodPost, "/api/skills/review-skill/enable", "")
	if enableRec.Code != http.StatusOK {
		t.Fatalf("enable status=%d body=%s", enableRec.Code, enableRec.Body.String())
	}
	listRec := performJSON(handler, http.MethodGet, "/api/skills", "")
	if listRec.Code != http.StatusOK || !bytes.Contains(listRec.Body.Bytes(), []byte(`"enabled":true`)) {
		t.Fatalf("unexpected list response: %s", listRec.Body.String())
	}

	deleteRec := performJSON(handler, http.MethodDelete, "/api/skills/review-skill", "")
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleteRec.Code, deleteRec.Body.String())
	}
}

func TestSkillInstallFromURLAPI(t *testing.T) {
	archive := apiSkillArchive(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	db := openTestDB(t)
	defer db.Close()
	handler := NewRouter(db, config.Config{DataDir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	payload, _ := json.Marshal(map[string]string{"url": server.URL + "/review-skill.zip"})
	rec := performJSON(handler, http.MethodPost, "/api/skills/install", string(payload))
	if rec.Code != http.StatusCreated || !bytes.Contains(rec.Body.Bytes(), []byte(`"source":"url"`)) {
		t.Fatalf("install status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func apiSkillArchive(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	entries := map[string]string{
		"review/skill.json": `{"id":"review-skill","name":"代码审查","version":"1.0.0","description":"审查工作流","keywords":["review"]}`,
		"review/SKILL.md":   "# 代码审查\n\n优先报告可验证的缺陷。",
	}
	for name, content := range entries {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatalf("create archive entry: %v", err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("write archive entry: %v", err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}
	return output.Bytes()
}
