package agent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"

	"github.com/ligson/water/water-be/internal/approval"
	"github.com/ligson/water/water-be/internal/contextpack"
	"github.com/ligson/water/water-be/internal/event"
	"github.com/ligson/water/water-be/internal/llm"
	"github.com/ligson/water/water-be/internal/provider"
	"github.com/ligson/water/water-be/internal/sandbox"
	"github.com/ligson/water/water-be/internal/task"
	"github.com/ligson/water/water-be/internal/tools"
	"github.com/ligson/water/water-be/internal/workspace"
)

const DefaultSystemPrompt = `你是若水（Water），一个运行在用户内网环境中的 AI 编程助手。回答要简洁、可靠、基于证据。你可以使用工具查看文件、目录和执行命令；需要真实信息时优先调用工具，不要猜测。信息不足以安全完成任务时，先提出 1-3 个关键问题。修改代码或配置后，优先读回关键文件或运行对应测试/构建来验证结果。`

const (
	defaultToolLoopMaxRounds  = 24
	approvedToolLoopMaxRounds = 12
	repeatedToolFailureLimit  = 2
	repeatedToolSuccessLimit  = 6
)

var errTurnInterrupted = errors.New("turn interrupted")

type EventAppender func(context.Context, event.AppendInput) (event.Event, error)

type Runner struct {
	db             *sql.DB
	appendEvent    EventAppender
	clientFactory  ClientFactory
	toolExecutor   *tools.Executor
	contextBuilder *contextpack.Builder
	systemPrompt   string
	requestTimeout time.Duration
}

type toolLoopObservation struct {
	name      string
	signature string
	args      string
	outcome   string
}

type toolLoopGuard struct {
	lastSignature string
	lastOutcome   string
	lastToolName  string
	lastToolArgs  string
	repeatCount   int
}

type toolLoopInterrupt struct {
	reason             string
	message            string
	continuationPrompt string
}

type ClientFactory func(provider.Provider) (llm.Client, error)

type RunTurnInput struct {
	RequestID    string
	TaskID       string
	TurnID       string
	TurnSequence int
	WorkspaceID  string
	UserInput    string
}

func NewRunner(db *sql.DB, appendEvent EventAppender) *Runner {
	return &Runner{
		db:          db,
		appendEvent: appendEvent,
		clientFactory: func(p provider.Provider) (llm.Client, error) {
			return llm.NewOpenAIClient(p)
		},
		toolExecutor:   tools.NewExecutor(sandbox.NewPermissionEngine(workspace.NewStore(db)), approval.NewStore(db)),
		contextBuilder: contextpack.NewBuilder(contextpack.NewStore(db)),
		systemPrompt:   DefaultSystemPrompt,
		requestTimeout: 10 * time.Minute,
	}
}

func (r *Runner) RunTurn(ctx context.Context, input RunTurnInput) {
	if r.requestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.requestTimeout)
		defer cancel()
	}

	if err := r.runTurn(ctx, input); err != nil {
		if errors.Is(err, context.Canceled) {
			if stopped, stopErr := r.turnStopped(context.Background(), input.TurnID); stopErr == nil && stopped {
				return
			}
			_ = r.interruptTurn(context.Background(), input, err)
			return
		}
		if errors.Is(err, errTurnInterrupted) {
			return
		}
		_ = r.failTurn(context.Background(), input, err)
	}
}

func (r *Runner) runTurn(ctx context.Context, input RunTurnInput) error {
	if input.TaskID == "" || input.TurnID == "" || input.WorkspaceID == "" {
		return errors.New("taskId, turnId and workspaceId are required")
	}

	if _, err := task.NewStore(r.db).UpdateActiveTurnStatus(ctx, input.TurnID, task.TurnStatusRunning); err != nil {
		if errors.Is(err, task.ErrTurnNotActive) {
			return errTurnInterrupted
		}
		return err
	}

	ws, err := workspace.NewStore(r.db).Get(ctx, input.WorkspaceID)
	if err != nil {
		return fmt.Errorf("load workspace: %w", err)
	}

	p, err := selectProvider(ctx, provider.NewStore(r.db), ws)
	if err != nil {
		return err
	}

	client, err := r.clientFactory(p)
	if err != nil {
		return fmt.Errorf("create llm client: %w", err)
	}

	currentTask, err := task.NewStore(r.db).Get(ctx, input.TaskID)
	if err != nil {
		return fmt.Errorf("load task: %w", err)
	}

	messages, err := r.buildMessages(ctx, input, ws, p, currentTask)
	if err != nil {
		return err
	}

	return r.continueWithMessages(ctx, client, messages, input, ws, currentTask, defaultToolLoopMaxRounds)
}

func (r *Runner) continueWithMessages(ctx context.Context, client llm.Client, messages []llm.Message, input RunTurnInput, ws workspace.Workspace, currentTask task.Task, maxRounds int) error {
	if maxRounds <= 0 {
		maxRounds = 1
	}
	guard := &toolLoopGuard{}
	for round := 0; round < maxRounds; round++ {
		if err := r.ensureTurnActive(ctx, input.TurnID); err != nil {
			return err
		}
		assistantMsg, toolCalls, finishReason, err := r.collectAssistantRound(ctx, client, messages, input, ws, currentTask)
		if err != nil {
			return err
		}
		messages = append(messages, assistantMsg)

		if len(toolCalls) == 0 {
			if assistantPromisedToolUse(assistantMsg.Content) {
				if _, err := task.NewStore(r.db).UpdateActiveTurnStatus(ctx, input.TurnID, task.TurnStatusInterrupted); err != nil {
					if errors.Is(err, task.ErrTurnNotActive) {
						return errTurnInterrupted
					}
					return err
				}
				return r.appendJSONEvent(ctx, input, "turn.interrupted", map[string]interface{}{
					"message": "模型表示需要查看工作区或环境信息，但没有实际发起工具调用，本轮未完成。",
				})
			}
			if err := r.appendTurnSummary(ctx, input, ws); err != nil {
				return err
			}
			if err := r.updateTaskRollingSummary(ctx, input, ws); err != nil {
				return err
			}
			if _, err := task.NewStore(r.db).UpdateActiveTurnStatus(ctx, input.TurnID, task.TurnStatusCompleted); err != nil {
				if errors.Is(err, task.ErrTurnNotActive) {
					return errTurnInterrupted
				}
				return err
			}
			return r.appendJSONEvent(ctx, input, "turn.completed", map[string]interface{}{
				"finishReason": finishReason,
			})
		}

		toolMessages, pendingApproval, observations, err := r.executeToolCalls(ctx, input, ws, currentTask, toolCalls)
		if err != nil {
			return err
		}
		if pendingApproval {
			if _, err := task.NewStore(r.db).UpdateActiveTurnStatus(ctx, input.TurnID, task.TurnStatusWaitingApproval); err != nil {
				if errors.Is(err, task.ErrTurnNotActive) {
					return errTurnInterrupted
				}
				return err
			}
			return nil
		}
		if interrupt, reason := guard.observe(observations); interrupt {
			if err := r.appendTurnSummary(ctx, input, ws); err != nil {
				return err
			}
			if err := r.updateTaskRollingSummary(ctx, input, ws); err != nil {
				return err
			}
			if _, err := task.NewStore(r.db).UpdateActiveTurnStatus(ctx, input.TurnID, task.TurnStatusInterrupted); err != nil {
				if errors.Is(err, task.ErrTurnNotActive) {
					return errTurnInterrupted
				}
				return err
			}
			return r.appendJSONEvent(ctx, input, "turn.interrupted", map[string]interface{}{
				"reason":             reason.reason,
				"message":            reason.message,
				"canContinue":        true,
				"continuationPrompt": reason.continuationPrompt,
				"blockedTool":        guard.lastToolName,
				"blockedTarget":      guard.lastToolArgs,
				"repeatCount":        guard.repeatCount,
			})
		}
		messages = append(messages, toolMessages...)
	}

	if err := r.appendTurnSummary(ctx, input, ws); err != nil {
		return err
	}
	if err := r.updateTaskRollingSummary(ctx, input, ws); err != nil {
		return err
	}
	return r.interruptToolLoopExceeded(ctx, input, maxRounds)
}

func (r *Runner) interruptToolLoopExceeded(ctx context.Context, input RunTurnInput, maxRounds int) error {
	if _, err := task.NewStore(r.db).UpdateActiveTurnStatus(ctx, input.TurnID, task.TurnStatusInterrupted); err != nil {
		if errors.Is(err, task.ErrTurnNotActive) {
			return errTurnInterrupted
		}
		return err
	}
	return r.appendJSONEvent(ctx, input, "turn.interrupted", map[string]interface{}{
		"reason":             "tool_round_limit",
		"message":            "本轮已运行较多步骤并暂告一段落。若任务还没完成，可以继续上一轮结果，若水会沿用当前上下文和已生成文件继续推进。",
		"maxRounds":          maxRounds,
		"canContinue":        true,
		"continuationPrompt": "继续上一轮任务，从最后一个未完成点接着做；先快速确认已生成的文件，再补齐剩余代码和验证，不要重复已完成工作。",
	})
}

func (g *toolLoopGuard) observe(observations []toolLoopObservation) (bool, toolLoopInterrupt) {
	for _, observation := range observations {
		if observation.signature == "" || observation.outcome == "" {
			continue
		}
		if observation.signature == g.lastSignature && observation.outcome == g.lastOutcome {
			g.repeatCount++
		} else {
			g.lastSignature = observation.signature
			g.lastOutcome = observation.outcome
			g.lastToolName = observation.name
			g.lastToolArgs = observation.args
			g.repeatCount = 1
		}

		if strings.HasPrefix(observation.outcome, "error:") && g.repeatCount >= repeatedToolFailureLimit {
			return true, toolLoopInterrupt{
				reason:             "tool_repeated_failure",
				message:            "同一工具调用连续失败，已经没有新的进展。若水会暂停本轮，避免继续在同一个错误上绕圈。",
				continuationPrompt: "先回到工作区根目录或已授权路径，确认当前任务目标，再继续下一步；不要重复刚才失败的路径或命令。",
			}
		}
		if !strings.HasPrefix(observation.outcome, "error:") && g.repeatCount >= repeatedToolSuccessLimit {
			return true, toolLoopInterrupt{
				reason:             "tool_repeated_output",
				message:            "同一工具调用连续返回相同结果，本轮没有新的信息。若水会暂停本轮，避免重复执行相同操作。",
				continuationPrompt: "换一个新的检查点继续：优先读取不同文件、确认当前工作区结构，或者根据已有结果直接推进实现。",
			}
		}
	}
	return false, toolLoopInterrupt{}
}

func assistantPromisedToolUse(content string) bool {
	text := strings.TrimSpace(strings.ToLower(content))
	if text == "" {
		return false
	}
	intentPhrases := []string{
		"让我先",
		"我先",
		"先查看",
		"先看看",
		"先检查",
		"先读取",
		"看一下",
		"查看你的",
		"查看当前",
		"检查当前",
		"读取工作区",
		"let me",
		"i will check",
		"i'll check",
		"i will inspect",
		"i'll inspect",
	}
	evidencePhrases := []string{
		"工作区",
		"当前项目",
		"目录",
		"文件",
		"环境",
		"系统信息",
		"workspace",
		"project",
		"directory",
		"file",
		"environment",
	}
	return containsAny(text, intentPhrases) && containsAny(text, evidencePhrases)
}

func (r *Runner) ResumeApprovedTool(ctx context.Context, appr approval.Approval, requestID string) {
	if appr.Status != approval.StatusApproved || appr.TaskID == "" || appr.TurnID == "" {
		return
	}
	if requestID == "" {
		requestID = appr.ID
	}
	if r.requestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.requestTimeout)
		defer cancel()
	}

	input, err := r.resumeApprovedTool(ctx, appr, requestID)
	if err != nil {
		if input.TurnID == "" {
			return
		}
		if errors.Is(err, context.Canceled) {
			_ = r.interruptTurn(context.Background(), input, err)
			return
		}
		if errors.Is(err, errTurnInterrupted) {
			return
		}
		_ = r.failTurn(context.Background(), input, err)
	}
}

func (r *Runner) resumeApprovedTool(ctx context.Context, appr approval.Approval, requestID string) (RunTurnInput, error) {
	req, err := toolRequestFromApproval(appr, requestID)
	if err != nil {
		return RunTurnInput{}, err
	}

	turn, err := task.NewStore(r.db).GetTurn(ctx, appr.TurnID)
	if err != nil {
		return RunTurnInput{}, fmt.Errorf("load turn: %w", err)
	}

	input := RunTurnInput{
		RequestID:    requestID,
		TaskID:       appr.TaskID,
		TurnID:       appr.TurnID,
		TurnSequence: turn.Sequence,
		WorkspaceID:  appr.WorkspaceID,
		UserInput:    turn.UserInput,
	}
	if _, err := task.NewStore(r.db).UpdateActiveTurnStatus(ctx, input.TurnID, task.TurnStatusRunning); err != nil {
		if errors.Is(err, task.ErrTurnNotActive) {
			return input, errTurnInterrupted
		}
		return input, err
	}

	ws, err := workspace.NewStore(r.db).Get(ctx, appr.WorkspaceID)
	if err != nil {
		return input, fmt.Errorf("load workspace: %w", err)
	}
	p, err := selectProvider(ctx, provider.NewStore(r.db), ws)
	if err != nil {
		return input, err
	}
	client, err := r.clientFactory(p)
	if err != nil {
		return input, fmt.Errorf("create llm client: %w", err)
	}
	currentTask, err := task.NewStore(r.db).Get(ctx, appr.TaskID)
	if err != nil {
		return input, fmt.Errorf("load task: %w", err)
	}
	messages, err := r.buildMessages(ctx, input, ws, p, currentTask)
	if err != nil {
		return input, err
	}
	assistantMsg, err := r.assistantMessageForApproval(ctx, appr, req)
	if err != nil {
		return input, err
	}
	messages = append(messages, assistantMsg)

	if err := r.appendJSONEvent(ctx, input, "approval.continuation.started", map[string]interface{}{
		"approvalId": appr.ID,
		"toolName":   req.Name,
	}); err != nil {
		return input, err
	}

	toolCall := llm.ToolCall{
		ID:   req.ToolCallID,
		Type: "function",
		Function: llm.ToolCallFunction{
			Name:      req.Name,
			Arguments: string(req.Arguments),
		},
	}
	toolMessages, pendingApproval, _, err := r.executeToolCalls(ctx, input, ws, currentTask, []llm.ToolCall{toolCall}, req.ApprovalID)
	if err != nil {
		return input, err
	}
	if pendingApproval {
		if _, err := task.NewStore(r.db).UpdateTurnStatus(ctx, input.TurnID, task.TurnStatusWaitingApproval); err != nil {
			return input, err
		}
		return input, nil
	}
	messages = append(messages, toolMessages...)
	return input, r.continueWithMessages(ctx, client, messages, input, ws, currentTask, approvedToolLoopMaxRounds)
}

func (r *Runner) buildMessages(ctx context.Context, input RunTurnInput, ws workspace.Workspace, p provider.Provider, currentTask task.Task) ([]llm.Message, error) {
	systemPrompt := r.systemPrompt
	if r.contextBuilder != nil {
		pack, err := r.contextBuilder.Build(ctx, contextpack.BuildInput{
			WorkspaceID:   ws.ID,
			TaskID:        input.TaskID,
			UserInput:     input.UserInput,
			ContextTokens: p.ContextWindowTokens,
		})
		if err != nil {
			return nil, err
		}
		if err := r.appendJSONEvent(ctx, input, "context.pack.built", map[string]interface{}{
			"estimatedTokens":     pack.EstimatedTokens,
			"tokenBudget":         pack.TokenBudget,
			"contextWindowTokens": p.ContextWindowTokens,
			"budgetRatio":         contextpack.DefaultBudgetRatio,
			"fileSummaryCount":    len(pack.FileSummaries),
			"selectedFilePaths":   contextPackFilePaths(pack.FileSummaries),
			"hasTaskSummary":      pack.TaskSummary != "",
			"truncated":           pack.Truncated,
		}); err != nil {
			return nil, err
		}
		contextText := renderContextPack(pack)
		if contextText != "" {
			systemPrompt += "\n\n" + contextText
		}
	}
	systemPrompt += fmt.Sprintf("\n\n当前运行环境：os=%s，arch=%s。用户询问“本机/电脑/当前机器”的系统信息时，指的是运行若水（Water）后端的这台机器。", runtime.GOOS, runtime.GOARCH)
	systemPrompt += "\n\n可用通用工具：list_dir、read_file、write_file、run_command。遇到文件、目录、磁盘空间、内存使用、CPU 使用率、系统信息、Git 状态、测试结果或其他真实环境信息时，先自己选择合适的通用工具，再基于工具结果继续推理；不要猜测，也不要等待专用工具。系统信息可优先用只读命令，例如磁盘 df -h /，macOS(darwin) 内存 vm_stat 与 sysctl hw.memsize、CPU top -l 1 -s 0 -n 0，Linux 内存 free -h、CPU top -bn1，Windows 内存 wmic OS get FreePhysicalMemory,TotalVisibleMemorySize /Value、CPU wmic cpu get loadpercentage /Value。如果工具返回 command not found 或命令不适合当前系统，应继续选择当前 os 对应的替代只读命令。"
	if isDocumentOutputRequest(input.UserInput) {
		suggestedPath := defaultDocumentPath(ws.RootPath, currentTask.Title, input.TurnSequence)
		systemPrompt += fmt.Sprintf("\n\n文档输出规则：用户这次像是在要求生成、整理或保存报告/文档。如果用户没有明确指定文件名或路径，请优先把最终 Markdown 内容通过 write_file 工具保存到 `%s`，不要反复追问保存路径；保存后在回复中说明文件位置。", suggestedPath)
	}
	return []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt},
		{Role: llm.RoleUser, Content: input.UserInput},
	}, nil
}

func (r *Runner) collectAssistantRound(ctx context.Context, client llm.Client, messages []llm.Message, input RunTurnInput, ws workspace.Workspace, currentTask task.Task) (llm.Message, []llm.ToolCall, string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream, err := client.ChatStream(ctx, llm.ChatRequest{
		Messages: messages,
		Tools:    tools.Definitions(),
	})
	if err != nil {
		return llm.Message{}, nil, "", fmt.Errorf("start chat stream: %w", err)
	}

	var full strings.Builder
	toolCallsByKey := make(map[string]llm.ToolCall)
	toolCallOrder := make([]string, 0)
	finishReason := ""
	sawToolCalls := false

	for item := range stream {
		if item.Err != nil {
			return llm.Message{}, nil, "", item.Err
		}
		if err := r.ensureTurnActive(ctx, input.TurnID); err != nil {
			return llm.Message{}, nil, "", err
		}
		switch item.Type {
		case "delta":
			if item.Delta == "" {
				continue
			}
			full.WriteString(item.Delta)
			if err := r.appendJSONEvent(ctx, input, "agent.message.delta", map[string]interface{}{"delta": item.Delta}); err != nil {
				return llm.Message{}, nil, "", err
			}
		case "tool_calls":
			sawToolCalls = true
			for _, call := range item.ToolCalls {
				key := toolCallKey(call)
				if existing, ok := toolCallsByKey[key]; ok {
					merged := existing
					if call.ID != "" {
						merged.ID = call.ID
					}
					if call.Type != "" {
						merged.Type = call.Type
					}
					if call.Function.Name != "" {
						merged.Function.Name = call.Function.Name
					}
					if call.Function.Arguments != "" {
						merged.Function.Arguments += call.Function.Arguments
					}
					if call.Index != 0 {
						merged.Index = call.Index
					}
					toolCallsByKey[key] = merged
					continue
				}
				toolCallsByKey[key] = call
				toolCallOrder = append(toolCallOrder, key)
			}
		case "completed":
			finishReason = item.FinishReason
		}
	}
	if err := r.ensureTurnActive(ctx, input.TurnID); err != nil {
		return llm.Message{}, nil, "", err
	}

	toolCalls := make([]llm.ToolCall, 0, len(toolCallOrder))
	for _, key := range toolCallOrder {
		toolCalls = append(toolCalls, toolCallsByKey[key])
	}
	if sawToolCalls {
		if err := r.appendJSONEvent(ctx, input, "agent.tool_calls.detected", map[string]interface{}{
			"toolCalls": toolCalls,
		}); err != nil {
			return llm.Message{}, nil, "", err
		}
	}
	if err := r.appendJSONEvent(ctx, input, "agent.message.completed", map[string]interface{}{
		"content":      full.String(),
		"finishReason": finishReason,
		"toolCalls":    toolCalls,
	}); err != nil {
		return llm.Message{}, nil, "", err
	}

	assistantMsg := llm.Message{
		Role:      llm.RoleAssistant,
		Content:   full.String(),
		ToolCalls: toolCalls,
	}
	return assistantMsg, toolCalls, finishReason, nil
}

func toolCallKey(call llm.ToolCall) string {
	if call.ID != "" {
		return "id:" + call.ID
	}
	return fmt.Sprintf("index:%d", call.Index)
}

func (r *Runner) assistantMessageForApproval(ctx context.Context, appr approval.Approval, req tools.Request) (llm.Message, error) {
	events, err := event.NewStore(r.db).ListByTask(ctx, appr.TaskID)
	if err != nil {
		return llm.Message{}, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		item := events[i]
		if item.TurnID != appr.TurnID || item.Type != "agent.message.completed" {
			continue
		}
		var payload struct {
			Content   string         `json:"content"`
			ToolCalls []llm.ToolCall `json:"toolCalls"`
		}
		if err := json.Unmarshal([]byte(item.PayloadJSON), &payload); err != nil {
			return llm.Message{}, fmt.Errorf("decode assistant event: %w", err)
		}
		if call, ok := matchingToolCall(payload.ToolCalls, req); ok {
			return llm.Message{
				Role:      llm.RoleAssistant,
				Content:   payload.Content,
				ToolCalls: []llm.ToolCall{call},
			}, nil
		}
	}
	if req.ToolCallID == "" {
		req.ToolCallID = appr.ID
	}
	return llm.Message{
		Role: llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{
			{
				ID:   req.ToolCallID,
				Type: "function",
				Function: llm.ToolCallFunction{
					Name:      req.Name,
					Arguments: string(req.Arguments),
				},
			},
		},
	}, nil
}

func matchingToolCall(calls []llm.ToolCall, req tools.Request) (llm.ToolCall, bool) {
	for _, call := range calls {
		if req.ToolCallID != "" && call.ID == req.ToolCallID {
			return call, true
		}
		if call.Function.Name == req.Name && normalizeJSONRaw(call.Function.Arguments) == normalizeJSONRaw(string(req.Arguments)) {
			return call, true
		}
	}
	return llm.ToolCall{}, false
}

func toolRequestFromApproval(appr approval.Approval, requestID string) (tools.Request, error) {
	var req tools.Request
	if strings.TrimSpace(appr.RequestJSON) == "" || strings.TrimSpace(appr.RequestJSON) == "{}" {
		return tools.Request{}, errors.New("approval has no tool request snapshot")
	}
	if err := json.Unmarshal([]byte(appr.RequestJSON), &req); err != nil {
		return tools.Request{}, fmt.Errorf("decode approval tool request: %w", err)
	}
	if strings.TrimSpace(req.Name) == "" {
		return tools.Request{}, errors.New("approval tool request has no name")
	}
	req.RequestID = requestID
	req.ApprovalID = appr.ID
	if req.ToolCallID == "" {
		req.ToolCallID = appr.ID
	}
	return req, nil
}

func normalizeJSONRaw(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "{}"
	}
	var value interface{}
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return raw
	}
	normalized, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return string(normalized)
}

type turnSummaryPayload struct {
	ChangedFiles []turnSummaryFile    `json:"changedFiles"`
	Validations  []turnSummaryCommand `json:"validations"`
	Commands     []turnSummaryCommand `json:"commands"`
}

type turnSummaryFile struct {
	Path        string `json:"path"`
	DisplayPath string `json:"displayPath"`
	Action      string `json:"action"`
	Bytes       int    `json:"bytes"`
	Additions   int    `json:"additions"`
	Deletions   int    `json:"deletions"`
}

type turnSummaryCommand struct {
	Command   string `json:"command"`
	Status    string `json:"status"`
	Summary   string `json:"summary,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

func (r *Runner) appendTurnSummary(ctx context.Context, input RunTurnInput, ws workspace.Workspace) error {
	summary, err := r.buildTurnSummary(ctx, input, ws)
	if err != nil {
		return err
	}
	if len(summary.ChangedFiles) == 0 && len(summary.Validations) == 0 && len(summary.Commands) == 0 {
		return nil
	}
	raw, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("encode turn summary: %w", err)
	}
	_, err = r.appendEvent(ctx, event.AppendInput{
		RequestID:   input.RequestID,
		WorkspaceID: input.WorkspaceID,
		TaskID:      input.TaskID,
		TurnID:      input.TurnID,
		Type:        "turn.summary",
		PayloadJSON: string(raw),
	})
	return err
}

func (r *Runner) buildTurnSummary(ctx context.Context, input RunTurnInput, ws workspace.Workspace) (turnSummaryPayload, error) {
	events, err := event.NewStore(r.db).ListByTask(ctx, input.TaskID)
	if err != nil {
		return turnSummaryPayload{}, err
	}

	summary := turnSummaryPayload{
		ChangedFiles: make([]turnSummaryFile, 0),
		Validations:  make([]turnSummaryCommand, 0),
		Commands:     make([]turnSummaryCommand, 0),
	}
	seenFiles := make(map[string]int)
	for _, item := range events {
		if item.TurnID != input.TurnID || item.Type != "tool.completed" {
			continue
		}
		var payload struct {
			Name   string                 `json:"name"`
			Output map[string]interface{} `json:"output"`
		}
		if err := json.Unmarshal([]byte(item.PayloadJSON), &payload); err != nil {
			return turnSummaryPayload{}, fmt.Errorf("decode tool event: %w", err)
		}
		switch payload.Name {
		case tools.NameWriteFile:
			file := summarizeWrittenFile(ws, payload.Output)
			if file.Path == "" {
				continue
			}
			if index, ok := seenFiles[file.Path]; ok {
				summary.ChangedFiles[index] = file
				continue
			}
			seenFiles[file.Path] = len(summary.ChangedFiles)
			summary.ChangedFiles = append(summary.ChangedFiles, file)
		case tools.NameRunCommand:
			command := summarizeCommand(payload.Output)
			if command.Command == "" {
				continue
			}
			summary.Commands = append(summary.Commands, command)
			if looksLikeValidationCommand(command.Command) {
				summary.Validations = append(summary.Validations, command)
			}
		}
	}
	return summary, nil
}

func (r *Runner) updateTaskRollingSummary(ctx context.Context, input RunTurnInput, ws workspace.Workspace) error {
	events, err := event.NewStore(r.db).ListByTask(ctx, input.TaskID)
	if err != nil {
		return err
	}
	summary := buildTaskRollingSummary(ws, events)
	if strings.TrimSpace(summary) == "" {
		return nil
	}
	hash := sha256.Sum256([]byte(summary))
	contentHash := fmt.Sprintf("%x", hash)
	if _, err := contextpack.NewStore(r.db).UpsertTaskSummary(ctx, contextpack.UpsertTaskSummaryInput{
		TaskID:      input.TaskID,
		ContentHash: contentHash,
		Summary:     summary,
	}); err != nil {
		return err
	}
	return r.appendJSONEvent(ctx, input, "context.summary.updated", map[string]interface{}{
		"contentHash": contentHash,
		"summary":     summary,
		"chars":       len([]rune(summary)),
	})
}

func buildTaskRollingSummary(ws workspace.Workspace, events []event.Event) string {
	userInputs := make([]string, 0)
	changedFiles := make([]turnSummaryFile, 0)
	changedByPath := make(map[string]int)
	validations := make([]turnSummaryCommand, 0)
	failures := make([]string, 0)

	for _, item := range events {
		switch item.Type {
		case "turn.started":
			var payload struct {
				UserInput string `json:"userInput"`
			}
			if err := json.Unmarshal([]byte(item.PayloadJSON), &payload); err == nil && strings.TrimSpace(payload.UserInput) != "" {
				userInputs = append(userInputs, compactSummaryLine(payload.UserInput, 160))
			}
		case "turn.summary":
			var payload turnSummaryPayload
			if err := json.Unmarshal([]byte(item.PayloadJSON), &payload); err != nil {
				continue
			}
			for _, file := range payload.ChangedFiles {
				if file.Path == "" {
					continue
				}
				if file.DisplayPath == "" {
					file.DisplayPath = displayPath(ws.RootPath, file.Path)
				}
				if index, ok := changedByPath[file.Path]; ok {
					changedFiles[index] = file
					continue
				}
				changedByPath[file.Path] = len(changedFiles)
				changedFiles = append(changedFiles, file)
			}
			for _, validation := range payload.Validations {
				if validation.Command != "" {
					validations = append(validations, validation)
				}
			}
		case "turn.failed", "turn.interrupted":
			var payload struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal([]byte(item.PayloadJSON), &payload); err == nil && strings.TrimSpace(payload.Message) != "" {
				failures = append(failures, compactSummaryLine(item.Type+": "+payload.Message, 180))
			}
		}
	}

	var out strings.Builder
	out.WriteString("任务滚动摘要（Harness 根据事件生成）\n")
	writeRecentLines(&out, "最近用户目标", userInputs, 6)
	writeChangedFiles(&out, changedFiles, 12)
	writeRecentCommands(&out, "验证结果", validations, 8)
	writeRecentLines(&out, "最近失败或中断", failures, 5)
	out.WriteString("下一轮注意:\n")
	out.WriteString("- 优先依据上述事实继续；信息不足时先读相关文件或向用户确认。\n")
	out.WriteString("- 修改代码后优先运行对应测试或构建，并把验证结果告诉用户。\n")

	text := strings.TrimSpace(out.String())
	if text == "任务滚动摘要（Harness 根据事件生成）\n下一轮注意:\n- 优先依据上述事实继续；信息不足时先读相关文件或向用户确认。\n- 修改代码后优先运行对应测试或构建，并把验证结果告诉用户。" {
		return ""
	}
	return text
}

func writeRecentLines(out *strings.Builder, title string, values []string, limit int) {
	if len(values) == 0 {
		return
	}
	out.WriteString(title)
	out.WriteString(":\n")
	start := len(values) - limit
	if start < 0 {
		start = 0
	}
	for _, value := range values[start:] {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out.WriteString("- ")
		out.WriteString(value)
		out.WriteString("\n")
	}
}

func writeChangedFiles(out *strings.Builder, files []turnSummaryFile, limit int) {
	if len(files) == 0 {
		return
	}
	out.WriteString("已修改文件:\n")
	start := len(files) - limit
	if start < 0 {
		start = 0
	}
	for _, file := range files[start:] {
		path := file.DisplayPath
		if path == "" {
			path = file.Path
		}
		out.WriteString("- ")
		out.WriteString(path)
		out.WriteString(" (")
		out.WriteString(stringWithDefault(file.Action, "modified"))
		if file.Additions != 0 || file.Deletions != 0 {
			out.WriteString(fmt.Sprintf(", +%d/-%d", file.Additions, file.Deletions))
		}
		out.WriteString(")\n")
	}
}

func writeRecentCommands(out *strings.Builder, title string, commands []turnSummaryCommand, limit int) {
	if len(commands) == 0 {
		return
	}
	out.WriteString(title)
	out.WriteString(":\n")
	start := len(commands) - limit
	if start < 0 {
		start = 0
	}
	for _, command := range commands[start:] {
		out.WriteString("- ")
		out.WriteString(command.Command)
		out.WriteString(": ")
		out.WriteString(stringWithDefault(command.Status, "unknown"))
		if command.Summary != "" {
			out.WriteString("\n  ")
			out.WriteString(compactSummaryLine(command.Summary, 220))
		}
		out.WriteString("\n")
	}
}

func compactSummaryLine(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if limit <= 0 || len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "..."
}

func summarizeWrittenFile(ws workspace.Workspace, output map[string]interface{}) turnSummaryFile {
	path := stringFromMap(output, "path")
	return turnSummaryFile{
		Path:        path,
		DisplayPath: displayPath(ws.RootPath, path),
		Action:      stringWithDefault(stringFromMap(output, "action"), "modified"),
		Bytes:       intFromMap(output, "bytes"),
		Additions:   intFromMap(output, "additions"),
		Deletions:   intFromMap(output, "deletions"),
	}
}

func summarizeCommand(output map[string]interface{}) turnSummaryCommand {
	command := stringFromMap(output, "command")
	if command == "" {
		return turnSummaryCommand{}
	}
	status := "passed"
	if stringFromMap(output, "error") != "" {
		status = "failed"
	}
	return turnSummaryCommand{
		Command:   command,
		Status:    status,
		Summary:   compactCommandOutput(stringFromMap(output, "output")),
		Truncated: boolFromMap(output, "truncated"),
	}
}

func displayPath(root string, path string) string {
	if path == "" {
		return ""
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return path
	}
	return rel
}

func looksLikeValidationCommand(command string) bool {
	lower := strings.ToLower(command)
	needles := []string{
		"go test",
		"go vet",
		"npm run build",
		"npm test",
		"npm run test",
		"pnpm test",
		"pnpm run build",
		"yarn test",
		"yarn build",
		"vite build",
		"vue-tsc",
	}
	for _, needle := range needles {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func compactCommandOutput(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	lines := make([]string, 0, 3)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, line)
		if len(lines) == 3 {
			break
		}
	}
	text := strings.Join(lines, "\n")
	if len([]rune(text)) <= 360 {
		return text
	}
	runes := []rune(text)
	return string(runes[:360]) + "\n..."
}

func stringFromMap(values map[string]interface{}, key string) string {
	if values == nil {
		return ""
	}
	raw, ok := values[key]
	if !ok || raw == nil {
		return ""
	}
	switch value := raw.(type) {
	case string:
		return value
	default:
		return fmt.Sprint(value)
	}
}

func intFromMap(values map[string]interface{}, key string) int {
	if values == nil {
		return 0
	}
	switch value := values[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		number, _ := value.Int64()
		return int(number)
	default:
		return 0
	}
}

func boolFromMap(values map[string]interface{}, key string) bool {
	if values == nil {
		return false
	}
	value, _ := values[key].(bool)
	return value
}

func stringWithDefault(value string, fallback string) string {
	if strings.TrimSpace(value) == "" || value == "<nil>" {
		return fallback
	}
	return value
}

func (r *Runner) executeToolCalls(ctx context.Context, input RunTurnInput, ws workspace.Workspace, currentTask task.Task, calls []llm.ToolCall, approvalID ...string) ([]llm.Message, bool, []toolLoopObservation, error) {
	if r.toolExecutor == nil {
		return nil, false, nil, nil
	}
	toolMessages := make([]llm.Message, 0, len(calls))
	observations := make([]toolLoopObservation, 0, len(calls))
	for _, call := range calls {
		if err := r.ensureTurnActive(ctx, input.TurnID); err != nil {
			return nil, false, nil, err
		}
		if err := r.appendJSONEvent(ctx, input, "tool.call.started", map[string]interface{}{
			"name":       call.Function.Name,
			"toolCallId": call.ID,
		}); err != nil {
			return nil, false, nil, err
		}
		reqApprovalID := ""
		if len(approvalID) > 0 {
			reqApprovalID = approvalID[0]
		}
		result, err := r.toolExecutor.Execute(ctx, tools.Context{
			Workspace: ws,
			Task:      currentTask,
			TurnID:    input.TurnID,
		}, tools.Request{
			RequestID:  input.RequestID,
			Name:       call.Function.Name,
			Arguments:  json.RawMessage(call.Function.Arguments),
			ApprovalID: reqApprovalID,
			ToolCallID: call.ID,
		})
		if errors.Is(err, tools.ErrApprovalRequired) {
			if err := r.appendJSONEvent(ctx, input, "approval.requested", map[string]interface{}{"approval": result.Approval}); err != nil {
				return nil, false, nil, err
			}
			return toolMessages, true, observations, nil
		}
		if err != nil {
			if appendErr := r.appendJSONEvent(ctx, input, "tool.failed", map[string]interface{}{
				"name":    call.Function.Name,
				"message": err.Error(),
				"hint":    toolFailureHint(err, ws),
			}); appendErr != nil {
				return nil, false, nil, appendErr
			}
			toolMessages = append(toolMessages, llm.Message{
				Role:       llm.RoleTool,
				ToolCallID: call.ID,
				Name:       call.Function.Name,
				Content:    toolErrorContent(err, ws),
			})
			observations = append(observations, toolLoopObservation{
				name:      call.Function.Name,
				signature: toolCallSignature(call),
				args:      strings.TrimSpace(call.Function.Arguments),
				outcome:   "error:" + normalizeLoopMessage(err.Error()),
			})
			continue
		}
		if err := r.appendJSONEvent(ctx, input, "tool.completed", map[string]interface{}{
			"name":   result.Name,
			"output": result.Output,
		}); err != nil {
			return nil, false, nil, err
		}
		toolMessages = append(toolMessages, llm.Message{
			Role:       llm.RoleTool,
			ToolCallID: call.ID,
			Name:       call.Function.Name,
			Content:    stringifyToolOutputForLLM(result.Output),
		})
		observations = append(observations, toolLoopObservation{
			name:      call.Function.Name,
			signature: toolCallSignature(call),
			args:      strings.TrimSpace(call.Function.Arguments),
			outcome:   "ok:" + toolOutputFingerprint(result.Output),
		})
	}
	return toolMessages, false, observations, nil
}

func toolCallSignature(call llm.ToolCall) string {
	return strings.TrimSpace(call.Function.Name) + "|" + normalizeJSONRaw(call.Function.Arguments)
}

func toolOutputFingerprint(output interface{}) string {
	raw, err := json.Marshal(output)
	if err != nil {
		return fmt.Sprintf("%T:%v", output, output)
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum[:])
}

func normalizeLoopMessage(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func toolFailureHint(err error, ws workspace.Workspace) string {
	if errors.Is(err, sandbox.ErrAccessDenied) {
		return "只能访问工作区根目录 " + ws.RootPath + " 或已经授权的外部路径；不要重复尝试未授权绝对路径。"
	}
	return ""
}

func toolErrorContent(err error, ws workspace.Workspace) string {
	hint := toolFailureHint(err, ws)
	payload := map[string]interface{}{
		"error": err.Error(),
	}
	if hint != "" {
		payload["hint"] = hint
	}
	raw, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		return fmt.Sprintf(`{"error":%s}`, quoteJSONString(err.Error()))
	}
	return string(raw)
}

func renderContextPack(pack contextpack.Pack) string {
	var out strings.Builder
	out.WriteString("Context Pack:\n")
	if pack.TaskSummary != "" {
		out.WriteString("任务摘要:\n")
		out.WriteString(pack.TaskSummary)
		out.WriteString("\n")
	}
	for _, item := range pack.FileSummaries {
		out.WriteString("文件: ")
		out.WriteString(item.Path)
		out.WriteString("\n摘要: ")
		out.WriteString(item.Summary)
		out.WriteString("\n")
	}
	if pack.Truncated {
		out.WriteString("注意: Context Pack 已按预算截断。\n")
	}
	return strings.TrimSpace(out.String())
}

func contextPackFilePaths(items []contextpack.FileSummary) []string {
	paths := make([]string, 0, len(items))
	for _, item := range items {
		paths = append(paths, item.Path)
	}
	return paths
}

func (r *Runner) failTurn(ctx context.Context, input RunTurnInput, cause error) error {
	if _, err := task.NewStore(r.db).UpdateActiveTurnStatus(ctx, input.TurnID, task.TurnStatusFailed); err != nil {
		if errors.Is(err, task.ErrTurnNotActive) {
			return nil
		}
		return err
	}
	return r.appendJSONEvent(ctx, input, "turn.failed", map[string]interface{}{
		"message": cause.Error(),
	})
}

func (r *Runner) interruptTurn(ctx context.Context, input RunTurnInput, cause error) error {
	if stopped, err := r.turnStopped(ctx, input.TurnID); err == nil && stopped {
		return nil
	}
	if _, err := task.NewStore(r.db).UpdateActiveTurnStatus(ctx, input.TurnID, task.TurnStatusInterrupted); err != nil {
		if errors.Is(err, task.ErrTurnNotActive) {
			return nil
		}
		return err
	}
	return r.appendJSONEvent(ctx, input, "turn.interrupted", map[string]interface{}{
		"message": cause.Error(),
	})
}

func (r *Runner) ensureTurnActive(ctx context.Context, turnID string) error {
	stopped, err := r.turnStopped(ctx, turnID)
	if err != nil {
		return err
	}
	if stopped {
		return errTurnInterrupted
	}
	return nil
}

func (r *Runner) turnStopped(ctx context.Context, turnID string) (bool, error) {
	turn, err := task.NewStore(r.db).GetTurn(ctx, turnID)
	if err != nil {
		return false, err
	}
	switch turn.Status {
	case task.TurnStatusInterrupted, task.TurnStatusCompleted, task.TurnStatusFailed:
		return true, nil
	default:
		return false, nil
	}
}

func (r *Runner) appendJSONEvent(ctx context.Context, input RunTurnInput, eventType string, payload map[string]interface{}) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode event payload: %w", err)
	}
	_, err = r.appendEvent(ctx, event.AppendInput{
		RequestID:   input.RequestID,
		WorkspaceID: input.WorkspaceID,
		TaskID:      input.TaskID,
		TurnID:      input.TurnID,
		Type:        eventType,
		PayloadJSON: string(raw),
	})
	return err
}

func selectProvider(ctx context.Context, store *provider.Store, ws workspace.Workspace) (provider.Provider, error) {
	if ws.DefaultProviderID != "" {
		return store.Get(ctx, ws.DefaultProviderID)
	}
	p, err := store.GetDefault(ctx)
	if errors.Is(err, provider.ErrNotFound) {
		return provider.Provider{}, errors.New("no provider configured")
	}
	return p, err
}

func stringifyToolOutput(value interface{}) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return `{}`
	}
	return string(raw)
}

func stringifyToolOutputForLLM(value interface{}) string {
	normalized := limitToolOutputForLLM(value)
	raw, err := json.Marshal(normalized)
	if err != nil {
		return `{}`
	}
	return string(raw)
}

func limitToolOutputForLLM(value interface{}) interface{} {
	const maxOutputChars = 6000
	data, ok := value.(map[string]interface{})
	if !ok {
		return value
	}
	output, ok := data["output"].(string)
	if !ok || len(output) <= maxOutputChars {
		return value
	}
	limited := make(map[string]interface{}, len(data)+1)
	for key, item := range data {
		limited[key] = item
	}
	limited["output"] = output[:maxOutputChars]
	limited["outputTruncatedForLLM"] = true
	limited["originalOutputChars"] = len(output)
	return limited
}

func quoteJSONString(value string) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(raw)
}

func isDocumentOutputRequest(input string) bool {
	text := strings.ToLower(strings.TrimSpace(input))
	if text == "" {
		return false
	}
	documentHints := []string{
		"报告",
		"文档",
		"总结",
		"分析",
		"整理",
		"markdown",
		"md",
		"readme",
		"report",
		"document",
		"summary",
	}
	outputHints := []string{
		"生成",
		"写",
		"保存",
		"输出",
		"导出",
		"整理",
		"create",
		"write",
		"save",
		"export",
	}
	return containsAny(text, documentHints) && containsAny(text, outputHints)
}

func defaultDocumentPath(rootPath string, taskTitle string, turnSequence int) string {
	sequence := turnSequence
	if sequence <= 0 {
		sequence = 1
	}
	baseName := sanitizeDocumentBaseName(taskTitle)
	if baseName == "" {
		baseName = "report"
	}
	return filepath.Join(rootPath, "reports", fmt.Sprintf("%s-turn-%d.md", baseName, sequence))
}

func sanitizeDocumentBaseName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var out strings.Builder
	lastDash := false
	written := 0
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(r)
			lastDash = false
			written++
		} else if !lastDash && out.Len() > 0 {
			out.WriteByte('-')
			lastDash = true
		}
		if written >= 48 {
			break
		}
	}
	return strings.Trim(out.String(), "-")
}

func containsAny(text string, values []string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}
