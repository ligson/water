package llm

import (
	"context"
	"fmt"
	"time"

	"github.com/ligson/water/water-be/internal/provider"
)

type TestResult struct {
	OK       bool   `json:"ok"`
	Message  string `json:"message"`
	HTTPCode int    `json:"httpCode"`
	Latency  string `json:"latency"`
}

func TestProvider(ctx context.Context, p provider.Provider) TestResult {
	client, err := NewOpenAIClient(p)
	if err != nil {
		return TestResult{OK: false, Message: err.Error()}
	}

	start := time.Now()
	_, err = client.Chat(ctx, ChatRequest{
		Messages: []Message{
			{Role: RoleUser, Content: "ping"},
		},
		MaxTokens: 1,
	})
	latency := time.Since(start)
	if err != nil {
		return TestResult{
			OK:      false,
			Message: fmt.Sprintf("connection failed: %v", err),
			Latency: latency.String(),
		}
	}

	return TestResult{
		OK:       true,
		Message:  "provider connection ok",
		HTTPCode: 200,
		Latency:  latency.String(),
	}
}
