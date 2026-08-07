package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ligson/water/water-be/internal/provider"
)

const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

var ErrProviderDisabled = errors.New("provider is disabled")

type Client interface {
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
	ChatStream(ctx context.Context, req ChatRequest) (<-chan ChatEvent, error)
}

type ModelOption struct {
	ID string `json:"id"`
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	ID       string           `json:"id"`
	Index    int              `json:"index,omitempty"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ChatRequest struct {
	Model       string
	Messages    []Message
	Tools       []Tool
	Temperature *float64
	MaxTokens   int
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type ChatResponse struct {
	Content      string
	ToolCalls    []ToolCall
	FinishReason string
}

type ChatEvent struct {
	Type         string
	Delta        string
	ToolCalls    []ToolCall
	FinishReason string
	Err          error
}

type OpenAIClient struct {
	provider provider.Provider
	http     *http.Client
}

func NewOpenAIClient(p provider.Provider) (*OpenAIClient, error) {
	if !p.Enabled {
		return nil, ErrProviderDisabled
	}
	baseURL := strings.TrimRight(p.BaseURL, "/")
	if _, err := url.ParseRequestURI(baseURL); err != nil {
		return nil, fmt.Errorf("invalid provider base url: %w", err)
	}
	p.BaseURL = baseURL

	timeout := time.Duration(p.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	return &OpenAIClient{
		provider: p,
		http:     &http.Client{Timeout: timeout},
	}, nil
}

func (c *OpenAIClient) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	payload, err := c.buildPayload(req, false)
	if err != nil {
		return ChatResponse{}, err
	}

	httpReq, err := c.newRequest(ctx, http.MethodPost, "/chat/completions", payload)
	if err != nil {
		return ChatResponse{}, err
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("chat request failed: %w", err)
	}
	defer resp.Body.Close()

	if err := checkOpenAIResponse(resp); err != nil {
		return ChatResponse{}, err
	}

	var decoded openAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return ChatResponse{}, fmt.Errorf("decode chat response: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return ChatResponse{}, errors.New("chat response has no choices")
	}

	first := decoded.Choices[0]
	return ChatResponse{
		Content:      first.Message.Content,
		ToolCalls:    first.Message.ToolCalls,
		FinishReason: first.FinishReason,
	}, nil
}

func (c *OpenAIClient) ChatStream(ctx context.Context, req ChatRequest) (<-chan ChatEvent, error) {
	payload, err := c.buildPayload(req, true)
	if err != nil {
		return nil, err
	}

	httpReq, err := c.newRequest(ctx, http.MethodPost, "/chat/completions", payload)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("chat stream request failed: %w", err)
	}
	if err := checkOpenAIResponse(resp); err != nil {
		_ = resp.Body.Close()
		return nil, err
	}

	events := make(chan ChatEvent, 16)
	go func() {
		defer close(events)
		defer resp.Body.Close()
		done := make(chan struct{})
		defer close(done)
		go func() {
			select {
			case <-ctx.Done():
				_ = resp.Body.Close()
			case <-done:
			}
		}()
		readOpenAIStream(ctx, resp.Body, events)
	}()

	return events, nil
}

func (c *OpenAIClient) ListModels(ctx context.Context) ([]ModelOption, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("list models request failed: %w", err)
	}
	defer resp.Body.Close()

	if err := checkOpenAIResponse(resp); err != nil {
		return nil, err
	}

	var decoded openAIModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode model list response: %w", err)
	}

	items := decoded.Data
	if len(items) == 0 {
		items = decoded.Models
	}
	models := make([]ModelOption, 0, len(items))
	seen := make(map[string]struct{})
	for _, item := range items {
		id := strings.TrimSpace(firstNonEmpty(item.ID, item.Name, item.Model))
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, ModelOption{ID: id})
	}
	return models, nil
}

func (c *OpenAIClient) buildPayload(req ChatRequest, stream bool) ([]byte, error) {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = c.provider.Model
	}
	if model == "" {
		return nil, errors.New("model is required")
	}
	if len(req.Messages) == 0 {
		return nil, errors.New("messages are required")
	}

	body := map[string]interface{}{
		"model":    model,
		"messages": req.Messages,
		"stream":   stream,
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode chat request: %w", err)
	}
	return payload, nil
}

func (c *OpenAIClient) newRequest(ctx context.Context, method string, endpoint string, payload []byte) (*http.Request, error) {
	var body io.Reader
	if len(payload) > 0 {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.provider.BaseURL+endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("build provider request: %w", err)
	}
	if len(payload) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.provider.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.provider.APIKey)
	}

	var extraHeaders map[string]string
	if strings.TrimSpace(c.provider.HeadersJSON) != "" {
		if err := json.Unmarshal([]byte(c.provider.HeadersJSON), &extraHeaders); err != nil {
			return nil, fmt.Errorf("decode provider headers: %w", err)
		}
		for key, value := range extraHeaders {
			key = strings.TrimSpace(key)
			if key != "" {
				req.Header.Set(key, value)
			}
		}
	}

	return req, nil
}

type openAIModelsResponse struct {
	Data   []openAIModelItem `json:"data"`
	Models []openAIModelItem `json:"models"`
}

type openAIModelItem struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Model   string `json:"model"`
	OwnedBy string `json:"owned_by"`
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func checkOpenAIResponse(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = resp.Status
	}
	return fmt.Errorf("provider returned HTTP %d: %s", resp.StatusCode, message)
}

func readOpenAIStream(ctx context.Context, body io.Reader, events chan<- ChatEvent) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var dataLines []string
	flush := func() bool {
		if len(dataLines) == 0 {
			return true
		}
		raw := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		if strings.TrimSpace(raw) == "[DONE]" {
			events <- ChatEvent{Type: "done"}
			return false
		}

		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
			events <- ChatEvent{Type: "error", Err: fmt.Errorf("decode stream chunk: %w", err)}
			return false
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				events <- ChatEvent{Type: "delta", Delta: choice.Delta.Content}
			}
			if len(choice.Delta.ToolCalls) > 0 {
				events <- ChatEvent{Type: "tool_calls", ToolCalls: choice.Delta.ToolCalls}
			}
			if choice.FinishReason != "" {
				events <- ChatEvent{Type: "completed", FinishReason: choice.FinishReason}
			}
		}
		return true
	}

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			events <- ChatEvent{Type: "error", Err: ctx.Err()}
			return
		default:
		}

		line := scanner.Text()
		if line == "" {
			if !flush() {
				return
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := ctx.Err(); err != nil {
		events <- ChatEvent{Type: "error", Err: err}
		return
	}
	if err := scanner.Err(); err != nil {
		events <- ChatEvent{Type: "error", Err: fmt.Errorf("read stream: %w", err)}
		return
	}
	flush()
}

type openAIChatResponse struct {
	Choices []struct {
		Message struct {
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

type openAIStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}
