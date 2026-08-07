package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ligson/water/water-be/internal/config"
)

func TestProviderCRUDEnvelopeAndMasking(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	createBody := `{
		"name": "Local",
		"type": "openai-compatible",
		"baseUrl": "http://localhost:11434/v1",
		"model": "qwen2.5-coder:7b",
		"apiKey": "secret",
		"isDefault": true
	}`
	createRec := performJSON(handler, http.MethodPost, "/api/providers", createBody)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", createRec.Code, createRec.Body.String())
	}
	if strings.Contains(createRec.Body.String(), "secret") {
		t.Fatalf("response leaked api key: %s", createRec.Body.String())
	}

	var created providerEnvelope
	decodeTestEnvelope(t, createRec, &created)
	if !created.Success {
		t.Fatalf("expected success create response")
	}
	if !created.Data.APIKeyConfigured {
		t.Fatalf("expected apiKeyConfigured true")
	}
	if !created.Data.IsDefault {
		t.Fatalf("expected created provider to be default")
	}
	if created.Data.ContextWindowTokens != 8192 {
		t.Fatalf("expected default context window 8192, got %d", created.Data.ContextWindowTokens)
	}

	listRec := performJSON(handler, http.MethodGet, "/api/providers", "")
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", listRec.Code)
	}
	if strings.Contains(listRec.Body.String(), "secret") {
		t.Fatalf("list response leaked api key: %s", listRec.Body.String())
	}

	var listed providerListEnvelope
	decodeTestEnvelope(t, listRec, &listed)
	if len(listed.Data.Items) != 1 {
		t.Fatalf("expected one provider, got %d", len(listed.Data.Items))
	}
	if listed.Data.Items[0].ID != created.Data.ID {
		t.Fatalf("expected provider id %q, got %q", created.Data.ID, listed.Data.Items[0].ID)
	}
}

func TestProviderValidation(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rec := performJSON(handler, http.MethodPost, "/api/providers", `{"name":"bad"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}

	var body Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if body.Success {
		t.Fatalf("expected validation failure")
	}
}

func TestProviderListEmptyArray(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rec := performJSON(handler, http.MethodGet, "/api/providers", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var listed providerListEnvelope
	decodeTestEnvelope(t, rec, &listed)
	if listed.Data.Items == nil {
		t.Fatalf("expected empty array, got nil")
	}
	if len(listed.Data.Items) != 0 {
		t.Fatalf("expected zero providers, got %d", len(listed.Data.Items))
	}
}

func TestProviderSetDefault(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	first := createProviderForTest(t, handler, "First", true)
	second := createProviderForTest(t, handler, "Second", false)

	defaultRec := performJSON(handler, http.MethodPost, "/api/providers/"+second.ID+"/default", "")
	if defaultRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", defaultRec.Code, defaultRec.Body.String())
	}

	listRec := performJSON(handler, http.MethodGet, "/api/providers", "")
	var listed providerListEnvelope
	decodeTestEnvelope(t, listRec, &listed)

	defaults := 0
	for _, item := range listed.Data.Items {
		if item.IsDefault {
			defaults++
			if item.ID != second.ID {
				t.Fatalf("expected second provider as default, got %q", item.ID)
			}
		}
		if item.ID == first.ID && item.IsDefault {
			t.Fatalf("first provider should no longer be default")
		}
	}
	if defaults != 1 {
		t.Fatalf("expected exactly one default provider, got %d", defaults)
	}
}

func TestProviderModelsUsesStoredProvider(t *testing.T) {
	var gotAuth string
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected provider path %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"object": "list",
			"data": [
				{"id": "qwen2.5-coder:7b"},
				{"id": "llama3.1:8b"}
			]
		}`))
	}))
	defer modelServer.Close()

	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	createBody := `{
		"name": "Local",
		"type": "openai-compatible",
		"baseUrl": "` + modelServer.URL + `/v1",
		"model": "qwen2.5-coder:7b",
		"apiKey": "secret",
		"isDefault": true
	}`
	createRec := performJSON(handler, http.MethodPost, "/api/providers", createBody)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create provider: status %d body %s", createRec.Code, createRec.Body.String())
	}
	var created providerEnvelope
	decodeTestEnvelope(t, createRec, &created)

	modelsRec := performJSON(handler, http.MethodPost, "/api/provider-models", `{"providerId":"`+created.Data.ID+`"}`)
	if modelsRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", modelsRec.Code, modelsRec.Body.String())
	}
	if strings.Contains(modelsRec.Body.String(), "secret") {
		t.Fatalf("model list response leaked api key: %s", modelsRec.Body.String())
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("expected stored authorization header, got %q", gotAuth)
	}

	var models providerModelListEnvelope
	decodeTestEnvelope(t, modelsRec, &models)
	if len(models.Data.Items) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models.Data.Items))
	}
	if models.Data.Items[0].ID != "qwen2.5-coder:7b" {
		t.Fatalf("unexpected first model: %+v", models.Data.Items[0])
	}
}

func createProviderForTest(t *testing.T, handler http.Handler, name string, isDefault bool) providerResponse {
	t.Helper()

	return createProviderForTestWithBaseURL(t, handler, name, "http://localhost:11434/v1", isDefault)
}

func createProviderForTestWithBaseURL(t *testing.T, handler http.Handler, name string, baseURL string, isDefault bool) providerResponse {
	t.Helper()

	body := `{
		"name": "` + name + `",
		"type": "openai-compatible",
		"baseUrl": "` + baseURL + `",
		"model": "qwen2.5-coder:7b",
		"isDefault": ` + boolJSON(isDefault) + `
	}`
	rec := performJSON(handler, http.MethodPost, "/api/providers", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create provider: status %d body %s", rec.Code, rec.Body.String())
	}
	var envelope providerEnvelope
	decodeTestEnvelope(t, rec, &envelope)
	return envelope.Data
}

func performJSON(handler http.Handler, method string, path string, body string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeTestEnvelope(t *testing.T, rec *httptest.ResponseRecorder, target interface{}) {
	t.Helper()

	if err := json.Unmarshal(rec.Body.Bytes(), target); err != nil {
		t.Fatalf("decode envelope: %v body=%s", err, rec.Body.String())
	}
}

func boolJSON(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

type providerResponse struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Type                string `json:"type"`
	BaseURL             string `json:"baseUrl"`
	Model               string `json:"model"`
	APIKeyConfigured    bool   `json:"apiKeyConfigured"`
	IsDefault           bool   `json:"isDefault"`
	Enabled             bool   `json:"enabled"`
	ContextWindowTokens int    `json:"contextWindowTokens"`
}

type providerEnvelope struct {
	Success  bool             `json:"success"`
	HTTPCode int              `json:"httpCode"`
	Data     providerResponse `json:"data"`
}

type providerListEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		Items []providerResponse `json:"items"`
	} `json:"data"`
}

type providerModelListEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	} `json:"data"`
}
