package api

import (
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/ligson/water/water-be/internal/config"
	"github.com/ligson/water/water-be/internal/store"
)

func TestHealthEnvelope(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set("X-Request-Id", "req_test")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("X-Request-Id"); got != "req_test" {
		t.Fatalf("expected request id header req_test, got %q", got)
	}

	var body Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Success {
		t.Fatalf("expected success response")
	}
	if body.RequestID != "req_test" {
		t.Fatalf("expected request id req_test, got %q", body.RequestID)
	}
	if body.HTTPCode != http.StatusOK {
		t.Fatalf("expected httpCode 200, got %d", body.HTTPCode)
	}
}

func TestNotFoundEnvelope(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest(http.MethodGet, "/api/missing", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rec.Code)
	}

	var body Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Success {
		t.Fatalf("expected error response")
	}
	if body.HTTPCode != http.StatusNotFound {
		t.Fatalf("expected httpCode 404, got %d", body.HTTPCode)
	}
	if body.Data == nil {
		t.Fatalf("expected data to be an empty object, got nil")
	}
}

func TestCORSForLocalDevFrontend(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest(http.MethodOptions, "/api/health", nil)
	req.Header.Set("Origin", "http://127.0.0.1:5173")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://127.0.0.1:5173" {
		t.Fatalf("expected local frontend origin, got %q", got)
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "water-test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := store.Migrate(db); err != nil {
		_ = db.Close()
		t.Fatalf("migrate db: %v", err)
	}
	return db
}
