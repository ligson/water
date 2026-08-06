package contextpack

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
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
		SystemInstruction: "按 Context Pack 回答。优先使用当前用户输入、任务滚动摘要、已钉住文件和相关文件摘要；信息不足时先请求工具读取文件或向用户确认，禁止凭空补全。修改后优先验证。",
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
	fileSummaries = prioritizeRelevant(fileSummaries, input.PinnedFilePaths, input.UserInput+"\n"+pack.TaskSummary)
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

func prioritizeRelevant(items []FileSummary, pinned []string, query string) []FileSummary {
	if len(items) == 0 {
		return items
	}
	pinnedRank := make(map[string]int, len(pinned))
	for index, path := range pinned {
		pinnedRank[normalizePath(path)] = index
	}
	terms := extractTerms(query)
	if len(terms) == 0 && len(pinnedRank) == 0 {
		return items
	}

	type rankedFile struct {
		item  FileSummary
		score int
		index int
	}
	ranked := make([]rankedFile, 0, len(items))
	for index, item := range items {
		score := relevanceScore(item, terms)
		if rank, ok := pinnedRank[normalizePath(item.Path)]; ok {
			score += 100000 - rank
		}
		ranked = append(ranked, rankedFile{item: item, score: score, index: index})
	}
	sort.SliceStable(ranked, func(i int, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].index < ranked[j].index
	})

	out := make([]FileSummary, 0, len(ranked))
	for _, item := range ranked {
		out = append(out, item.item)
	}
	return out
}

func relevanceScore(item FileSummary, terms map[string]struct{}) int {
	if len(terms) == 0 {
		return 0
	}
	path := strings.ToLower(item.Path)
	base := strings.ToLower(filepath.Base(item.Path))
	summary := strings.ToLower(item.Summary)
	symbols := strings.ToLower(item.SymbolsJSON)
	imports := strings.ToLower(item.ImportsJSON)
	language := strings.ToLower(item.Language)

	score := 0
	for term := range terms {
		if term == "" {
			continue
		}
		if strings.Contains(path, term) {
			score += 16
		}
		if strings.Contains(base, term) {
			score += 24
		}
		if strings.Contains(summary, term) {
			score += 6
		}
		if strings.Contains(symbols, term) {
			score += 10
		}
		if strings.Contains(imports, term) {
			score += 4
		}
		if language == term {
			score += 4
		}
	}
	return score
}

func extractTerms(text string) map[string]struct{} {
	terms := make(map[string]struct{})
	var current strings.Builder
	flush := func() {
		value := strings.ToLower(current.String())
		current.Reset()
		if len([]rune(value)) < 2 {
			return
		}
		if isStopTerm(value) {
			return
		}
		terms[value] = struct{}{}
	}
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			flush()
			terms[string(r)] = struct{}{}
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.' {
			current.WriteRune(unicode.ToLower(r))
			continue
		}
		flush()
	}
	flush()
	return terms
}

func isStopTerm(value string) bool {
	switch value {
	case "the", "and", "for", "with", "this", "that", "from", "true", "false", "null",
		"一个", "这个", "那个", "需要", "一下", "当前", "帮我", "文件", "代码":
		return true
	default:
		return false
	}
}

func normalizePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
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
