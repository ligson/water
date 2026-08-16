package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestMessageMarshalsMultimodalContentParts(t *testing.T) {
	raw, err := json.Marshal(Message{
		Role:    RoleUser,
		Content: "inspect image",
		ContentParts: []ContentPart{
			{Type: "text", Text: "inspect image"},
			{Type: "image_url", ImageURL: &ContentImageURL{URL: "data:image/png;base64,AAAA"}},
		},
	})
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode message: %v", err)
	}
	parts, ok := decoded["content"].([]interface{})
	if !ok || len(parts) != 2 {
		t.Fatalf("expected two content parts, got %#v", decoded["content"])
	}
	imagePart := parts[1].(map[string]interface{})
	imageURL := imagePart["image_url"].(map[string]interface{})
	if imageURL["url"] != "data:image/png;base64,AAAA" {
		t.Fatalf("unexpected image url: %#v", imageURL)
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

func TestOpenAIClientChatStreamCanOutliveRequestTimeoutWhileActive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"still\"}}]}\n\n"))
		flusher.Flush()
		time.Sleep(35 * time.Millisecond)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\" active\"}}]}\n\n"))
		flusher.Flush()
		time.Sleep(35 * time.Millisecond)
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := newTestOpenAIClientWithTimeouts(t, server.URL+"/v1", 50*time.Millisecond, 500*time.Millisecond)
	events, err := client.ChatStream(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("chat stream: %v", err)
	}

	var content strings.Builder
	for event := range events {
		if event.Err != nil {
			t.Fatalf("stream event error: %v", event.Err)
		}
		content.WriteString(event.Delta)
	}
	if content.String() != "still active" {
		t.Fatalf("expected active stream content, got %q", content.String())
	}
}

func TestOpenAIClientChatStreamIdleTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		time.Sleep(150 * time.Millisecond)
	}))
	defer server.Close()

	client := newTestOpenAIClientWithTimeouts(t, server.URL+"/v1", 100*time.Millisecond, 30*time.Millisecond)
	events, err := client.ChatStream(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("chat stream: %v", err)
	}

	var streamErr error
	for event := range events {
		if event.Err != nil {
			streamErr = event.Err
		}
	}
	if streamErr == nil || !strings.Contains(streamErr.Error(), "流式响应空闲超时") {
		t.Fatalf("expected explicit stream idle timeout, got %v", streamErr)
	}
}

func TestOpenAIClientListModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("expected authorization header, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"object": "list",
			"data": [
				{"id": "qwen2.5-coder:7b"},
				{"name": "llama3.1:8b"},
				{"model": "deepseek-r1:14b"}
			]
		}`))
	}))
	defer server.Close()

	client := newTestOpenAIClient(t, server.URL+"/v1")
	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatalf("list models: %v", err)
	}
	if len(models) != 3 {
		t.Fatalf("expected 3 models, got %d", len(models))
	}
	if models[0].ID != "qwen2.5-coder:7b" || models[1].ID != "llama3.1:8b" || models[2].ID != "deepseek-r1:14b" {
		t.Fatalf("unexpected models: %+v", models)
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
	return newTestOpenAIClientWithTimeouts(t, baseURL, 30*time.Second, 60*time.Second)
}

func newTestOpenAIClientWithTimeouts(t *testing.T, baseURL string, requestTimeout time.Duration, streamIdleTimeout time.Duration) *OpenAIClient {
	t.Helper()

	client, err := NewOpenAIClient(provider.Provider{
		Name:                "test",
		Type:                provider.TypeOpenAICompatible,
		BaseURL:             baseURL,
		Model:               "local-model",
		APIKey:              "secret",
		Enabled:             true,
		TimeoutMS:           int(requestTimeout / time.Millisecond),
		StreamIdleTimeoutMS: int(streamIdleTimeout / time.Millisecond),
		HeadersJSON:         "{}",
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return client
}
