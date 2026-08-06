package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ligson/water/water-be/internal/provider"
)

func TestOpenAIClientChat(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")

		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["model"] != "local-model" {
			t.Fatalf("expected model local-model, got %v", body["model"])
		}
		if body["stream"] != false {
			t.Fatalf("expected stream false, got %v", body["stream"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [{
				"message": {"content": "hello"},
				"finish_reason": "stop"
			}]
		}`))
	}))
	defer server.Close()

	client := newTestOpenAIClient(t, server.URL+"/v1")
	resp, err := client.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if resp.Content != "hello" {
		t.Fatalf("expected hello, got %q", resp.Content)
	}
	if resp.FinishReason != "stop" {
		t.Fatalf("expected stop finish reason, got %q", resp.FinishReason)
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("expected authorization header, got %q", gotAuth)
	}
}

func TestOpenAIClientChatStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["stream"] != true {
			t.Fatalf("expected stream true, got %v", body["stream"])
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"he\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"llo\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := newTestOpenAIClient(t, server.URL+"/v1")
	events, err := client.ChatStream(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("chat stream: %v", err)
	}

	var pieces []string
	var completed bool
	for event := range events {
		if event.Err != nil {
			t.Fatalf("stream event error: %v", event.Err)
		}
		if event.Type == "delta" {
			pieces = append(pieces, event.Delta)
		}
		if event.Type == "completed" && event.FinishReason == "stop" {
			completed = true
		}
	}

	if strings.Join(pieces, "") != "hello" {
		t.Fatalf("expected hello, got %q", strings.Join(pieces, ""))
	}
	if !completed {
		t.Fatalf("expected completed event")
	}
}

func TestOpenAIClientErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad model", http.StatusBadGateway)
	}))
	defer server.Close()

	client := newTestOpenAIClient(t, server.URL+"/v1")
	_, err := client.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err == nil {
		t.Fatalf("expected provider error")
	}
	if !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("expected HTTP 502 error, got %v", err)
	}
}

func newTestOpenAIClient(t *testing.T, baseURL string) *OpenAIClient {
	t.Helper()

	client, err := NewOpenAIClient(provider.Provider{
		Name:        "test",
		Type:        provider.TypeOpenAICompatible,
		BaseURL:     baseURL,
		Model:       "local-model",
		APIKey:      "secret",
		Enabled:     true,
		TimeoutMS:   30000,
		HeadersJSON: "{}",
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return client
}
