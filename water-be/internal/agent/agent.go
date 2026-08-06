package agent

import (
	"context"
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

const DefaultSystemPrompt = `你是若水（Water），一个运行在用户内网环境中的 AI 编程助手。回答要简洁、可靠。你可以使用工具查看文件、目录和执行命令；需要真实信息时优先调用工具，不要猜测。`

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
			_ = r.interruptTurn(context.Background(), input, err)
			return
		}
		_ = r.failTurn(context.Background(), input, err)
	}
}

func (r *Runner) runTurn(ctx context.Context, input RunTurnInput) error {
	if input.TaskID == "" || input.TurnID == "" || input.WorkspaceID == "" {
		return errors.New("taskId, turnId and workspaceId are required")
	}

	if _, err := task.NewStore(r.db).UpdateTurnStatus(ctx, input.TurnID, task.TurnStatusRunning); err != nil {
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

	return r.continueWithMessages(ctx, client, messages, input, ws, currentTask, 6)
}

func (r *Runner) continueWithMessages(ctx context.Context, client llm.Client, messages []llm.Message, input RunTurnInput, ws workspace.Workspace, currentTask task.Task, maxRounds int) error {
	if maxRounds <= 0 {
		maxRounds = 1
	}
	for round := 0; round < maxRounds; round++ {
		assistantMsg, toolCalls, finishReason, err := r.collectAssistantRound(ctx, client, messages, input, ws, currentTask)
		if err != nil {
			return err
		}
		messages = append(messages, assistantMsg)

		if len(toolCalls) == 0 {
			if _, err := task.NewStore(r.db).UpdateTurnStatus(ctx, input.TurnID, task.TurnStatusCompleted); err != nil {
				return err
			}
			return r.appendJSONEvent(ctx, input, "turn.completed", map[string]interface{}{
				"finishReason": finishReason,
			})
		}

		toolMessages, pendingApproval, err := r.executeToolCalls(ctx, input, ws, currentTask, toolCalls)
		if err != nil {
			return err
		}
		if pendingApproval {
			if _, err := task.NewStore(r.db).UpdateTurnStatus(ctx, input.TurnID, task.TurnStatusWaitingApproval); err != nil {
				return err
			}
			return nil
		}
		messages = append(messages, toolMessages...)
	}

	return errors.New("tool loop exceeded maximum rounds")
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
	if _, err := task.NewStore(r.db).UpdateTurnStatus(ctx, input.TurnID, task.TurnStatusRunning); err != nil {
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
	toolMessages, pendingApproval, err := r.executeToolCalls(ctx, input, ws, currentTask, []llm.ToolCall{toolCall}, req.ApprovalID)
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
	return input, r.continueWithMessages(ctx, client, messages, input, ws, currentTask, 5)
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

	for item := range stream {
		if item.Err != nil {
			return llm.Message{}, nil, "", item.Err
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
			if err := r.appendJSONEvent(ctx, input, "agent.tool_calls.detected", map[string]interface{}{"toolCalls": item.ToolCalls}); err != nil {
				return llm.Message{}, nil, "", err
			}
		case "completed":
			finishReason = item.FinishReason
		}
	}

	toolCalls := make([]llm.ToolCall, 0, len(toolCallOrder))
	for _, key := range toolCallOrder {
		toolCalls = append(toolCalls, toolCallsByKey[key])
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
	return fmt.Sprintf("index:%d:%s", call.Index, call.Function.Name)
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

func (r *Runner) executeToolCalls(ctx context.Context, input RunTurnInput, ws workspace.Workspace, currentTask task.Task, calls []llm.ToolCall, approvalID ...string) ([]llm.Message, bool, error) {
	if r.toolExecutor == nil {
		return nil, false, nil
	}
	toolMessages := make([]llm.Message, 0, len(calls))
	for _, call := range calls {
		if err := r.appendJSONEvent(ctx, input, "tool.call.started", map[string]interface{}{
			"name":       call.Function.Name,
			"toolCallId": call.ID,
		}); err != nil {
			return nil, false, err
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
				return nil, false, err
			}
			return toolMessages, true, nil
		}
		if err != nil {
			if appendErr := r.appendJSONEvent(ctx, input, "tool.failed", map[string]interface{}{
				"name":    call.Function.Name,
				"message": err.Error(),
			}); appendErr != nil {
				return nil, false, appendErr
			}
			toolMessages = append(toolMessages, llm.Message{
				Role:       llm.RoleTool,
				ToolCallID: call.ID,
				Name:       call.Function.Name,
				Content:    fmt.Sprintf(`{"error":%s}`, quoteJSONString(err.Error())),
			})
			continue
		}
		if err := r.appendJSONEvent(ctx, input, "tool.completed", map[string]interface{}{
			"name":   result.Name,
			"output": result.Output,
		}); err != nil {
			return nil, false, err
		}
		toolMessages = append(toolMessages, llm.Message{
			Role:       llm.RoleTool,
			ToolCallID: call.ID,
			Name:       call.Function.Name,
			Content:    stringifyToolOutputForLLM(result.Output),
		})
	}
	return toolMessages, false, nil
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

func (r *Runner) failTurn(ctx context.Context, input RunTurnInput, cause error) error {
	_, _ = task.NewStore(r.db).UpdateTurnStatus(ctx, input.TurnID, task.TurnStatusFailed)
	return r.appendJSONEvent(ctx, input, "turn.failed", map[string]interface{}{
		"message": cause.Error(),
	})
}

func (r *Runner) interruptTurn(ctx context.Context, input RunTurnInput, cause error) error {
	_, _ = task.NewStore(r.db).UpdateTurnStatus(ctx, input.TurnID, task.TurnStatusInterrupted)
	return r.appendJSONEvent(ctx, input, "turn.interrupted", map[string]interface{}{
		"message": cause.Error(),
	})
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
