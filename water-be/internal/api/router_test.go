package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ligson/water/water-be/internal/auth"
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

func TestAuthGateUnlockAndLock(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	bootstrapPIN := "123456"
	if _, err := auth.NewStore(db).Ensure(context.Background(), bootstrapPIN); err != nil {
		t.Fatalf("ensure auth: %v", err)
	}

	cfg := config.Config{AuthEnabled: true}
	handler := NewRouter(db, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	unauthRec := performJSON(handler, http.MethodGet, "/api/providers", "")
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 before unlock, got %d", unauthRec.Code)
	}

	unlockRec := performJSON(handler, http.MethodPost, "/api/auth/unlock", `{"pin":"123456"}`)
	if unlockRec.Code != http.StatusOK {
		t.Fatalf("expected unlock 200, got %d: %s", unlockRec.Code, unlockRec.Body.String())
	}
	var unlockBody struct {
		Data struct {
			AccessToken string `json:"accessToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(unlockRec.Body.Bytes(), &unlockBody); err != nil {
		t.Fatalf("decode unlock response: %v", err)
	}
	if unlockBody.Data.AccessToken == "" {
		t.Fatalf("expected access token")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/providers", nil)
	req.Header.Set("Authorization", "Bearer "+unlockBody.Data.AccessToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected protected request 200 after unlock, got %d", rec.Code)
	}

	lockReq := httptest.NewRequest(http.MethodPost, "/api/auth/lock", nil)
	lockReq.Header.Set("Authorization", "Bearer "+unlockBody.Data.AccessToken)
	lockRec := httptest.NewRecorder()
	handler.ServeHTTP(lockRec, lockReq)
	if lockRec.Code != http.StatusOK {
		t.Fatalf("expected lock 200, got %d: %s", lockRec.Code, lockRec.Body.String())
	}

	afterLockRec := performJSON(handler, http.MethodGet, "/api/providers", "")
	if afterLockRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 after lock, got %d", afterLockRec.Code)
	}

	changeWithoutTokenRec := performJSON(handler, http.MethodPost, "/api/auth/change-pin", `{"currentPin":"123456","newPin":"654321"}`)
	if changeWithoutTokenRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected change pin without token 401, got %d", changeWithoutTokenRec.Code)
	}

	unlockAgainRec := performJSON(handler, http.MethodPost, "/api/auth/unlock", `{"pin":"123456"}`)
	if unlockAgainRec.Code != http.StatusOK {
		t.Fatalf("expected second unlock 200, got %d: %s", unlockAgainRec.Code, unlockAgainRec.Body.String())
	}
	var unlockAgainBody struct {
		Data struct {
			AccessToken string `json:"accessToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(unlockAgainRec.Body.Bytes(), &unlockAgainBody); err != nil {
		t.Fatalf("decode second unlock response: %v", err)
	}
	changeReq := httptest.NewRequest(http.MethodPost, "/api/auth/change-pin", strings.NewReader(`{"currentPin":"123456","newPin":"654321"}`))
	changeReq.Header.Set("Content-Type", "application/json")
	changeReq.Header.Set("Authorization", "Bearer "+unlockAgainBody.Data.AccessToken)
	changeRec := httptest.NewRecorder()
	handler.ServeHTTP(changeRec, changeReq)
	if changeRec.Code != http.StatusOK {
		t.Fatalf("expected change pin with token 200, got %d: %s", changeRec.Code, changeRec.Body.String())
	}
}

func TestAuthPINLockoutEscalatesAndBlocksCorrectPIN(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	if _, err := auth.NewStore(db).Ensure(context.Background(), "123456"); err != nil {
		t.Fatalf("ensure auth: %v", err)
	}
	handler := NewRouter(db, config.Config{AuthEnabled: true}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	for attempt := 1; attempt <= 2; attempt++ {
		rec := performJSON(handler, http.MethodPost, "/api/auth/unlock", `{"pin":"000000"}`)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong PIN attempt %d: expected 401, got %d: %s", attempt, rec.Code, rec.Body.String())
		}
	}

	thirdAttempt := performJSON(handler, http.MethodPost, "/api/auth/unlock", `{"pin":"000000"}`)
	if thirdAttempt.Code != http.StatusTooManyRequests {
		t.Fatalf("third wrong PIN attempt: expected 429, got %d: %s", thirdAttempt.Code, thirdAttempt.Body.String())
	}

	lockedAttempt := performJSON(handler, http.MethodPost, "/api/auth/unlock", `{"pin":"123456"}`)
	if lockedAttempt.Code != http.StatusTooManyRequests {
		t.Fatalf("correct PIN during lockout: expected 429, got %d: %s", lockedAttempt.Code, lockedAttempt.Body.String())
	}
	if lockedAttempt.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header during lockout")
	}
	var lockedBody struct {
		Data struct {
			RetryAfterSeconds int `json:"retryAfterSeconds"`
		} `json:"data"`
	}
	if err := json.Unmarshal(lockedAttempt.Body.Bytes(), &lockedBody); err != nil {
		t.Fatalf("decode lockout response: %v", err)
	}
	if lockedBody.Data.RetryAfterSeconds <= 0 {
		t.Fatalf("expected positive retryAfterSeconds, got %d", lockedBody.Data.RetryAfterSeconds)
	}

	statusRec := performJSON(handler, http.MethodGet, "/api/auth/status", "")
	if statusRec.Code != http.StatusOK {
		t.Fatalf("auth status during lockout: expected 200, got %d", statusRec.Code)
	}
	var statusBody struct {
		Data struct {
			PINRetryAfterSeconds int `json:"pinRetryAfterSeconds"`
		} `json:"data"`
	}
	if err := json.Unmarshal(statusRec.Body.Bytes(), &statusBody); err != nil {
		t.Fatalf("decode auth status: %v", err)
	}
	if statusBody.Data.PINRetryAfterSeconds <= 0 {
		t.Fatalf("expected status lockout countdown, got %d", statusBody.Data.PINRetryAfterSeconds)
	}

	if _, err := db.Exec(`UPDATE auth_state SET pin_locked_until = ''`); err != nil {
		t.Fatalf("expire first lockout: %v", err)
	}
	fourthAttempt := performJSON(handler, http.MethodPost, "/api/auth/unlock", `{"pin":"000000"}`)
	if fourthAttempt.Code != http.StatusTooManyRequests {
		t.Fatalf("fourth wrong PIN attempt: expected 429, got %d: %s", fourthAttempt.Code, fourthAttempt.Body.String())
	}
	var escalatedBody struct {
		Data struct {
			RetryAfterSeconds int `json:"retryAfterSeconds"`
		} `json:"data"`
	}
	if err := json.Unmarshal(fourthAttempt.Body.Bytes(), &escalatedBody); err != nil {
		t.Fatalf("decode escalated lockout response: %v", err)
	}
	if escalatedBody.Data.RetryAfterSeconds < 4*60 {
		t.Fatalf("expected fourth failure to lock for about five minutes, got %d seconds", escalatedBody.Data.RetryAfterSeconds)
	}
}

func TestAuthPINSuccessResetsFailureCount(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	if _, err := auth.NewStore(db).Ensure(context.Background(), "123456"); err != nil {
		t.Fatalf("ensure auth: %v", err)
	}
	handler := NewRouter(db, config.Config{AuthEnabled: true}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	for attempt := 1; attempt <= 2; attempt++ {
		rec := performJSON(handler, http.MethodPost, "/api/auth/unlock", `{"pin":"000000"}`)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong PIN attempt %d: expected 401, got %d", attempt, rec.Code)
		}
	}
	correct := performJSON(handler, http.MethodPost, "/api/auth/unlock", `{"pin":"123456"}`)
	if correct.Code != http.StatusOK {
		t.Fatalf("correct PIN: expected 200, got %d: %s", correct.Code, correct.Body.String())
	}
	afterReset := performJSON(handler, http.MethodPost, "/api/auth/unlock", `{"pin":"000000"}`)
	if afterReset.Code != http.StatusUnauthorized {
		t.Fatalf("first wrong PIN after success: expected 401, got %d: %s", afterReset.Code, afterReset.Body.String())
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
