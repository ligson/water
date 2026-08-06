package contextpack

import (
	"context"
	"strings"
)

const DefaultBudgetRatio = 0.8

type Builder struct {
	store *Store
}

type BuildInput struct {
	WorkspaceID     string
	TaskID          string
	UserInput       string
	ContextTokens   int
	BudgetRatio     float64
	PinnedFilePaths []string
}

type Pack struct {
	TokenBudget       int           `json:"tokenBudget"`
	EstimatedTokens   int           `json:"estimatedTokens"`
	SystemInstruction string        `json:"systemInstruction"`
	TaskSummary       string        `json:"taskSummary"`
	FileSummaries     []FileSummary `json:"fileSummaries"`
	UserInput         string        `json:"userInput"`
	Truncated         bool          `json:"truncated"`
}

func NewBuilder(store *Store) *Builder {
	return &Builder{store: store}
}

func (b *Builder) Build(ctx context.Context, input BuildInput) (Pack, error) {
	ratio := input.BudgetRatio
	if ratio <= 0 || ratio > 1 {
		ratio = DefaultBudgetRatio
	}
	tokenBudget := int(float64(input.ContextTokens) * ratio)
	if tokenBudget <= 0 {
		tokenBudget = 8192
	}

	pack := Pack{
		TokenBudget:       tokenBudget,
		SystemInstruction: "按 Context Pack 回答。优先使用当前用户输入、已钉住文件和最近摘要；信息不足时先请求工具读取文件。",
		UserInput:         input.UserInput,
	}
	pack.EstimatedTokens = estimateTokens(pack.SystemInstruction) + estimateTokens(pack.UserInput)

	if input.TaskID != "" {
		summary, err := b.store.GetTaskSummary(ctx, input.TaskID)
		if err == nil {
			pack.TaskSummary = summary.Summary
			pack.EstimatedTokens += estimateTokens(summary.Summary)
		} else if err != ErrNotFound {
			return Pack{}, err
		}
	}

	fileSummaries, err := b.store.ListFileSummaries(ctx, input.WorkspaceID)
	if err != nil {
		return Pack{}, err
	}
	if len(input.PinnedFilePaths) > 0 {
		fileSummaries = prioritizePinned(fileSummaries, input.PinnedFilePaths)
	}
	for _, item := range fileSummaries {
		cost := estimateTokens(item.Path) + estimateTokens(item.Summary)
		if pack.EstimatedTokens+cost > tokenBudget {
			pack.Truncated = true
			break
		}
		pack.FileSummaries = append(pack.FileSummaries, item)
		pack.EstimatedTokens += cost
	}

	return pack, nil
}

func prioritizePinned(items []FileSummary, pinned []string) []FileSummary {
	seen := make(map[string]struct{}, len(items))
	out := make([]FileSummary, 0, len(items))
	for _, target := range pinned {
		for _, item := range items {
			if item.Path == target {
				out = append(out, item)
				seen[item.Path] = struct{}{}
			}
		}
	}
	for _, item := range items {
		if _, ok := seen[item.Path]; !ok {
			out = append(out, item)
		}
	}
	return out
}

func estimateTokens(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	runes := len([]rune(text))
	words := len(strings.Fields(text))
	estimate := runes / 3
	if words > estimate {
		estimate = words
	}
	if estimate <= 0 {
		return 1
	}
	return estimate
}
