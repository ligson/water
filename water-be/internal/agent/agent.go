package agent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"

	"github.com/ligson/water/water-be/internal/approval"
	"github.com/ligson/water/water-be/internal/contextpack"
	"github.com/ligson/water/water-be/internal/document"
	"github.com/ligson/water/water-be/internal/event"
	"github.com/ligson/water/water-be/internal/llm"
	"github.com/ligson/water/water-be/internal/provider"
	"github.com/ligson/water/water-be/internal/sandbox"
	"github.com/ligson/water/water-be/internal/skill"
	"github.com/ligson/water/water-be/internal/task"
	"github.com/ligson/water/water-be/internal/taskcontract"
	"github.com/ligson/water/water-be/internal/taskplan"
	"github.com/ligson/water/water-be/internal/tools"
	"github.com/ligson/water/water-be/internal/workspace"
)

const DefaultSystemPrompt = `你是若水（Water），一个运行在用户内网环境中的 AI 编程助手。回答要简洁、可靠、基于证据。你可以使用工具查看文件、目录和执行命令；需要真实信息时优先调用工具，不要猜测。信息不足以安全完成任务时，先提出 1-3 个关键问题。修改代码或配置后，优先读回关键文件或运行对应测试/构建来验证结果。`

const (
	defaultToolLoopMaxRounds  = 24
	approvedToolLoopMaxRounds = 12
	maxExecutionPhases        = 3
	continuedPhaseMaxRounds   = 8
	repeatedToolFailureLimit  = 2
	repeatedToolSuccessLimit  = 3
)

var errTurnInterrupted = errors.New("turn interrupted")

type EventAppender func(context.Context, event.AppendInput) (event.Event, error)

type Runner struct {
	db             *sql.DB
	appendEvent    EventAppender
	clientFactory  ClientFactory
	toolExecutor   *tools.Executor
	contextBuilder *contextpack.Builder
	skillStore     *skill.Store
	systemPrompt   string
	requestTimeout time.Duration
}

type toolLoopObservation struct {
	name           string
	signature      string
	args           string
	outcome        string
	hypothesisID   string
	resource       string
	operation      string
	newInformation bool
	repeatCount    int
	evidenceID     string
}

type cachedToolResult struct {
	result      tools.Result
	fingerprint string
}

type toolExecutionState struct {
	readCache     map[string]cachedToolResult
	resourceCache map[string]cachedToolResult
}

type toolLoopGuard struct {
	lastSignature string
	lastOutcome   string
	lastToolName  string
	lastToolArgs  string
	repeatCount   int
	repeatCounts  map[string]int
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
	Attachments  []task.Attachment
}

func NewRunner(db *sql.DB, appendEvent EventAppender) *Runner {
	return &Runner{
		db:          db,
		appendEvent: appendEvent,
		clientFactory: func(p provider.Provider) (llm.Client, error) {
			return llm.NewOpenAIClient(p)
		},
		toolExecutor: tools.NewExecutor(
			sandbox.NewPermissionEngine(workspace.NewStore(db)),
			approval.NewStore(db),
			tools.WithSkillReader(skill.NewStore(db, "")),
		),
		contextBuilder: contextpack.NewBuilder(contextpack.NewStore(db)),
		skillStore:     skill.NewStore(db, ""),
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
	contract, err := r.ensureTaskContract(ctx, input)
	if err != nil {
		return fmt.Errorf("prepare task contract: %w", err)
	}
	if contract.Stage == taskcontract.StageBlocked && isGenericContinuationInput(input.UserInput) {
		return r.blockTurn(ctx, input, ws, contract, completionDecision{
			Outcome:       completionBlocked,
			Reason:        "missing_required_input",
			MissingInputs: contract.MissingInputs,
		})
	}
	if _, err := r.ensureTaskPlan(ctx, input, contract); err != nil {
		return fmt.Errorf("prepare task plan: %w", err)
	}
	if err := r.ensureHypothesisLedger(ctx, input, contract); err != nil {
		return fmt.Errorf("prepare hypothesis ledger: %w", err)
	}
	contract, err = r.updateContractStage(ctx, input, contract, taskcontract.StageCollectingEvidence, nil)
	if err != nil {
		return err
	}

	messages, err := r.buildMessages(ctx, input, ws, p, currentTask, contract)
	if err != nil {
		return err
	}

	return r.continueWithMessages(ctx, client, messages, input, ws, currentTask, contract, p, defaultToolLoopMaxRounds)
}

func (r *Runner) continueWithMessages(ctx context.Context, client llm.Client, messages []llm.Message, input RunTurnInput, ws workspace.Workspace, currentTask task.Task, contract taskcontract.Contract, p provider.Provider, maxRounds int) error {
	return r.continueExecutionPhase(ctx, client, messages, input, ws, currentTask, contract, p, maxRounds, maxRounds, 1, nil)
}

func (r *Runner) continueExecutionPhase(ctx context.Context, client llm.Client, messages []llm.Message, input RunTurnInput, ws workspace.Workspace, currentTask task.Task, contract taskcontract.Contract, p provider.Provider, maxRounds int, initialMaxRounds int, phase int, deferredToolCalls []llm.ToolCall) error {
	if maxRounds <= 0 {
		maxRounds = 1
	}
	guard := &toolLoopGuard{}
	toolState := newToolExecutionState()
	lastDroppedMessages := 0
	if len(deferredToolCalls) > 0 {
		if err := r.appendJSONEvent(ctx, input, "agent.deferred_tool_calls.executing", map[string]interface{}{
			"phase":     phase,
			"toolCalls": deferredToolCalls,
			"message":   "若水正在执行上一阶段末尾提出的有效工具动作，然后从结果继续。",
		}); err != nil {
			return err
		}
		messages = append(messages, llm.Message{Role: llm.RoleAssistant, ToolCalls: deferredToolCalls})
		toolMessages, pendingApproval, _, err := r.executeToolCalls(ctx, input, ws, currentTask, contract, toolState, deferredToolCalls)
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
		messages = append(messages, toolMessages...)
	}
	for round := 0; round < maxRounds; round++ {
		if err := r.ensureTurnActive(ctx, input.TurnID); err != nil {
			return err
		}
		requestMessages, compaction := compactToolLoopMessages(messages, p.ContextWindowTokens)
		if compaction.DroppedMessages > lastDroppedMessages {
			if err := r.appendJSONEvent(ctx, input, "context.turn.compacted", map[string]interface{}{
				"round":                    round + 1,
				"contextWindowTokens":      p.ContextWindowTokens,
				"tokenBudget":              compaction.TokenBudget,
				"originalEstimatedTokens":  compaction.OriginalEstimatedTokens,
				"compactedEstimatedTokens": compaction.CompactedEstimatedTokens,
				"droppedMessages":          compaction.DroppedMessages,
			}); err != nil {
				return err
			}
			lastDroppedMessages = compaction.DroppedMessages
		}

		finalRound := round == maxRounds-1
		requestTools := tools.Definitions()
		if finalRound {
			requestMessages = append(requestMessages, llm.Message{Role: llm.RoleSystem, Content: toolLoopFinalInstruction(maxRounds)})
			requestTools = nil
		} else if instruction := toolLoopConvergenceInstruction(round, maxRounds); instruction != "" {
			requestMessages = append(requestMessages, llm.Message{Role: llm.RoleSystem, Content: instruction})
		}
		if err := r.appendContextRequestEstimate(ctx, input, p, requestMessages, requestTools, phase, round+1); err != nil {
			return err
		}

		assistantMsg, toolCalls, finishReason, err := r.collectAssistantRound(ctx, client, requestMessages, requestTools, input, ws, currentTask)
		if err != nil {
			return err
		}
		messages = append(messages, assistantMsg)

		if len(toolCalls) == 0 {
			decision, err := r.arbitrateCompletion(ctx, input, contract, assistantMsg.Content)
			if err != nil {
				return err
			}
			if decision.Outcome == completionCompleted {
				return r.completeTurn(ctx, input, ws, contract, finishReason, finalRound, "")
			}
			if decision.Outcome == completionBlocked {
				return r.blockTurn(ctx, input, ws, contract, decision)
			}
			if finalRound {
				return r.continueNextExecutionPhase(ctx, client, input, ws, currentTask, contract, p, maxRounds, initialMaxRounds, phase, decision.Reason, nil)
			}
			messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: completionRetryInstruction(decision, contract)})
			continue
		}
		if finalRound {
			if err := r.appendJSONEvent(ctx, input, "agent.final_tool_calls.deferred", map[string]interface{}{
				"phase":     phase,
				"toolCalls": toolCalls,
				"message":   "最终回合工具已关闭，若水已保留模型提出的工具动作，并将在下一阶段优先执行。",
			}); err != nil {
				return err
			}
			return r.continueNextExecutionPhase(ctx, client, input, ws, currentTask, contract, p, maxRounds, initialMaxRounds, phase, "tool_requested_after_final_round", toolCalls)
		}
		if stage := contractStageForToolCalls(toolCalls); stage != "" && stage != contract.Stage {
			contract, err = r.updateContractStage(ctx, input, contract, stage, nil)
			if err != nil {
				return err
			}
		}

		toolMessages, pendingApproval, observations, err := r.executeToolCalls(ctx, input, ws, currentTask, contract, toolState, toolCalls)
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
		messages = append(messages, toolMessages...)
		if action, reason := semanticNoProgress(observations); action != "" {
			if action == "replan" {
				if err := r.appendJSONEvent(ctx, input, "agent.replan.requested", map[string]interface{}{
					"reason":  reason.reason,
					"message": reason.message,
				}); err != nil {
					return err
				}
				messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: evidenceReplanInstruction(reason)})
				continue
			}
			guard.captureSemanticObservation(observations)
			return r.finalizeAfterToolLoopGuard(ctx, client, messages, input, ws, currentTask, contract, p, maxRounds, initialMaxRounds, phase, guard, reason)
		}
		if interrupt, reason := guard.observe(observations); interrupt {
			return r.finalizeAfterToolLoopGuard(ctx, client, messages, input, ws, currentTask, contract, p, maxRounds, initialMaxRounds, phase, guard, reason)
		}
	}

	if err := r.appendTurnSummary(ctx, input, ws); err != nil {
		return err
	}
	if err := r.updateTaskRollingSummary(ctx, input, ws); err != nil {
		return err
	}
	return r.continueNextExecutionPhase(ctx, client, input, ws, currentTask, contract, p, maxRounds, initialMaxRounds, phase, "tool_round_limit", nil)
}

func (r *Runner) finalizeAfterToolLoopGuard(
	ctx context.Context,
	client llm.Client,
	messages []llm.Message,
	input RunTurnInput,
	ws workspace.Workspace,
	currentTask task.Task,
	contract taskcontract.Contract,
	p provider.Provider,
	maxRounds int,
	initialMaxRounds int,
	phase int,
	guard *toolLoopGuard,
	reason toolLoopInterrupt,
) error {
	if err := r.appendJSONEvent(ctx, input, "agent.loop.guard.triggered", map[string]interface{}{
		"reason":        reason.reason,
		"message":       reason.message,
		"blockedTool":   guard.lastToolName,
		"blockedTarget": guard.lastToolArgs,
		"repeatCount":   guard.repeatCount,
		"action":        "force_final_response",
	}); err != nil {
		return err
	}
	requestMessages, _ := compactToolLoopMessages(messages, p.ContextWindowTokens)
	requestMessages = append(requestMessages, llm.Message{
		Role:    llm.RoleSystem,
		Content: toolLoopGuardFinalInstruction(reason),
	})
	if err := r.appendContextRequestEstimate(ctx, input, p, requestMessages, nil, phase, 0); err != nil {
		return err
	}
	assistantMsg, toolCalls, finishReason, err := r.collectAssistantRound(
		ctx,
		client,
		requestMessages,
		nil,
		input,
		ws,
		currentTask,
	)
	if err != nil {
		return err
	}
	if len(toolCalls) > 0 || strings.TrimSpace(assistantMsg.Content) == "" {
		return r.failIncompleteTurn(ctx, input, ws, contract, reason.reason, executionRoundBudget(initialMaxRounds))
	}
	decision, err := r.arbitrateCompletion(ctx, input, contract, assistantMsg.Content)
	if err != nil {
		return err
	}
	if decision.Outcome == completionBlocked {
		return r.blockTurn(ctx, input, ws, contract, decision)
	}
	if decision.Outcome != completionCompleted {
		return r.failIncompleteTurn(ctx, input, ws, contract, decision.Reason, executionRoundBudget(initialMaxRounds))
	}
	return r.completeTurn(ctx, input, ws, contract, finishReason, true, reason.reason)
}

func (r *Runner) continueNextExecutionPhase(ctx context.Context, client llm.Client, input RunTurnInput, ws workspace.Workspace, currentTask task.Task, contract taskcontract.Contract, p provider.Provider, maxRounds int, initialMaxRounds int, phase int, reason string, deferredToolCalls []llm.ToolCall) error {
	if phase >= maxExecutionPhases {
		if len(deferredToolCalls) > 0 {
			return r.finalizeDeferredToolCalls(ctx, client, input, ws, currentTask, contract, p, deferredToolCalls, reason, initialMaxRounds)
		}
		return r.failIncompleteTurn(ctx, input, ws, contract, reason, executionRoundBudget(initialMaxRounds))
	}
	refreshedContract, err := taskcontract.NewStore(r.db).Get(ctx, input.TaskID)
	if err != nil {
		return err
	}
	plan, err := taskplan.NewStore(r.db).Get(ctx, input.TaskID, refreshedContract.Goal)
	if err != nil {
		return err
	}
	if err := r.appendJSONEvent(ctx, input, "agent.execution.phase.continued", map[string]interface{}{
		"completedPhase": phase,
		"nextPhase":      phase + 1,
		"reason":         reason,
		"deferredTools":  len(deferredToolCalls),
		"currentStep":    currentPlanStep(plan),
		"message":        "当前任务尚未完成，若水已清理本阶段工具上下文并继续执行当前计划。",
	}); err != nil {
		return err
	}
	messages, err := r.buildMessages(ctx, input, ws, p, currentTask, refreshedContract)
	if err != nil {
		return err
	}
	messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: executionPhaseRestartInstruction(phase+1, plan, ws)})
	nextMaxRounds := min(maxRounds, continuedPhaseMaxRounds)
	return r.continueExecutionPhase(ctx, client, messages, input, ws, currentTask, refreshedContract, p, nextMaxRounds, initialMaxRounds, phase+1, deferredToolCalls)
}

func (r *Runner) finalizeDeferredToolCalls(ctx context.Context, client llm.Client, input RunTurnInput, ws workspace.Workspace, currentTask task.Task, contract taskcontract.Contract, p provider.Provider, toolCalls []llm.ToolCall, reason string, initialMaxRounds int) error {
	if err := r.appendJSONEvent(ctx, input, "agent.deferred_tool_calls.executing", map[string]interface{}{
		"phase":     maxExecutionPhases,
		"toolCalls": toolCalls,
		"message":   "若水正在执行最后阶段提出的有效工具动作，并将根据结果做最终判断。",
	}); err != nil {
		return err
	}
	messages, err := r.buildMessages(ctx, input, ws, p, currentTask, contract)
	if err != nil {
		return err
	}
	messages = append(messages, llm.Message{Role: llm.RoleAssistant, ToolCalls: toolCalls})
	toolMessages, pendingApproval, _, err := r.executeToolCalls(ctx, input, ws, currentTask, contract, newToolExecutionState(), toolCalls)
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
	messages = append(messages, toolMessages...)
	messages = append(messages, llm.Message{
		Role:    llm.RoleSystem,
		Content: "这是本轮最后一次收敛判断。请严格基于刚才的工具结果判断当前计划是否完成；不得继续请求工具。若尚未完成，准确说明下一未完成步骤，不要宣称完成。",
	})
	requestMessages, _ := compactToolLoopMessages(messages, p.ContextWindowTokens)
	if err := r.appendContextRequestEstimate(ctx, input, p, requestMessages, nil, maxExecutionPhases, 0); err != nil {
		return err
	}
	assistantMsg, extraToolCalls, finishReason, err := r.collectAssistantRound(ctx, client, requestMessages, nil, input, ws, currentTask)
	if err != nil {
		return err
	}
	if len(extraToolCalls) == 0 && strings.TrimSpace(assistantMsg.Content) != "" {
		decision, err := r.arbitrateCompletion(ctx, input, contract, assistantMsg.Content)
		if err != nil {
			return err
		}
		if decision.Outcome == completionCompleted {
			return r.completeTurn(ctx, input, ws, contract, finishReason, true, reason)
		}
		if decision.Outcome == completionBlocked {
			return r.blockTurn(ctx, input, ws, contract, decision)
		}
	}
	return r.failIncompleteTurn(ctx, input, ws, contract, reason, executionRoundBudget(initialMaxRounds))
}

func executionRoundBudget(initialMaxRounds int) int {
	if initialMaxRounds <= 0 {
		initialMaxRounds = 1
	}
	return initialMaxRounds + (maxExecutionPhases-1)*min(initialMaxRounds, continuedPhaseMaxRounds)
}

func (r *Runner) completeTurn(ctx context.Context, input RunTurnInput, ws workspace.Workspace, contract taskcontract.Contract, finishReason string, forcedFinal bool, forcedReason string) error {
	if err := r.appendTurnSummary(ctx, input, ws); err != nil {
		return err
	}
	if err := r.updateTaskRollingSummary(ctx, input, ws); err != nil {
		return err
	}
	if _, err := r.updateContractStage(ctx, input, contract, taskcontract.StageCompleted, nil); err != nil {
		return err
	}
	if err := r.updateHypothesesForTurnOutcome(ctx, input, contract, "resolved", nil); err != nil {
		return err
	}
	if _, err := task.NewStore(r.db).UpdateActiveTurnStatus(ctx, input.TurnID, task.TurnStatusCompleted); err != nil {
		if errors.Is(err, task.ErrTurnNotActive) {
			return errTurnInterrupted
		}
		return err
	}
	return r.appendJSONEvent(ctx, input, "turn.completed", map[string]interface{}{
		"finishReason":      finishReason,
		"forcedFinal":       forcedFinal,
		"forcedFinalReason": forcedReason,
		"arbiter":           "completion_criteria_satisfied",
	})
}

func (r *Runner) blockTurn(ctx context.Context, input RunTurnInput, ws workspace.Workspace, contract taskcontract.Contract, decision completionDecision) error {
	if err := r.appendTurnSummary(ctx, input, ws); err != nil {
		return err
	}
	if err := r.updateTaskRollingSummary(ctx, input, ws); err != nil {
		return err
	}
	if _, err := r.updateContractStage(ctx, input, contract, taskcontract.StageBlocked, decision.MissingInputs); err != nil {
		return err
	}
	if err := r.updateHypothesesForTurnOutcome(ctx, input, contract, "blocked", decision.MissingInputs); err != nil {
		return err
	}
	if _, err := task.NewStore(r.db).UpdateActiveTurnStatus(ctx, input.TurnID, task.TurnStatusBlocked); err != nil {
		if errors.Is(err, task.ErrTurnNotActive) {
			return errTurnInterrupted
		}
		return err
	}
	return r.appendJSONEvent(ctx, input, "turn.blocked", map[string]interface{}{
		"reason":        decision.Reason,
		"message":       "继续任务需要你补充关键信息。",
		"missingInputs": decision.MissingInputs,
	})
}

func (r *Runner) failIncompleteTurn(ctx context.Context, input RunTurnInput, ws workspace.Workspace, contract taskcontract.Contract, reason string, maxRounds int) error {
	if err := r.appendTurnSummary(ctx, input, ws); err != nil {
		return err
	}
	if err := r.updateTaskRollingSummary(ctx, input, ws); err != nil {
		return err
	}
	if _, err := task.NewStore(r.db).UpdateActiveTurnStatus(ctx, input.TurnID, task.TurnStatusPaused); err != nil {
		if errors.Is(err, task.ErrTurnNotActive) {
			return errTurnInterrupted
		}
		return err
	}
	plan, planErr := taskplan.NewStore(r.db).Get(ctx, input.TaskID, contract.Goal)
	if planErr != nil && !errors.Is(planErr, taskplan.ErrNotFound) {
		return planErr
	}
	message := "本轮已达到安全执行上限，但任务尚未完成。当前计划和证据已经保留，可从未通过的步骤继续。"
	if reason == "semantic_no_progress" || reason == "semantic_replan_required" {
		message = "若水连续多个执行阶段没有获得足以推进任务的新证据，本轮已暂停。任务仍未完成，当前计划和证据已经保留。"
	}
	return r.appendJSONEvent(ctx, input, "turn.paused", map[string]interface{}{
		"reason":             reason,
		"message":            message,
		"maxRounds":          maxRounds,
		"doneWhen":           contract.DoneWhen,
		"currentStep":        currentPlanStep(plan),
		"canContinue":        true,
		"continuationPrompt": "继续当前任务，从持久化计划中尚未通过的步骤开始；优先运行项目已有测试或验收脚本，不要重复读取已有证据。",
	})
}

func (g *toolLoopGuard) observe(observations []toolLoopObservation) (bool, toolLoopInterrupt) {
	if g.repeatCounts == nil {
		g.repeatCounts = make(map[string]int)
	}
	for _, observation := range observations {
		if observation.signature == "" || observation.outcome == "" {
			continue
		}
		key := observation.signature + "\x00" + observation.outcome
		g.repeatCounts[key]++
		g.lastSignature = observation.signature
		g.lastOutcome = observation.outcome
		g.lastToolName = observation.name
		g.lastToolArgs = observation.args
		g.repeatCount = g.repeatCounts[key]

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

func (g *toolLoopGuard) captureSemanticObservation(observations []toolLoopObservation) {
	for _, observation := range observations {
		if observation.resource == "" {
			continue
		}
		g.lastToolName = observation.name
		g.lastToolArgs = observation.resource
		g.lastOutcome = observation.outcome
		g.repeatCount = observation.repeatCount
	}
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
		Attachments:  turn.Attachments,
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
	contract, err := r.ensureTaskContract(ctx, input)
	if err != nil {
		return input, fmt.Errorf("prepare task contract: %w", err)
	}
	if _, err := r.ensureTaskPlan(ctx, input, contract); err != nil {
		return input, fmt.Errorf("prepare task plan: %w", err)
	}
	if err := r.ensureHypothesisLedger(ctx, input, contract); err != nil {
		return input, fmt.Errorf("prepare hypothesis ledger: %w", err)
	}
	messages, err := r.buildMessages(ctx, input, ws, p, currentTask, contract)
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
	toolMessages, pendingApproval, _, err := r.executeToolCalls(ctx, input, ws, currentTask, contract, newToolExecutionState(), []llm.ToolCall{toolCall}, req.ApprovalID)
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
	return input, r.continueWithMessages(ctx, client, messages, input, ws, currentTask, contract, p, approvedToolLoopMaxRounds)
}

func (r *Runner) buildMessages(ctx context.Context, input RunTurnInput, ws workspace.Workspace, p provider.Provider, currentTask task.Task, contract taskcontract.Contract) ([]llm.Message, error) {
	systemPrompt := r.systemPrompt
	if r.contextBuilder != nil {
		pack, err := r.contextBuilder.Build(ctx, contextpack.BuildInput{
			WorkspaceID:   ws.ID,
			RootPath:      ws.RootPath,
			TaskID:        input.TaskID,
			UserInput:     input.UserInput,
			ContextTokens: p.ContextWindowTokens,
		})
		if err != nil {
			return nil, err
		}
		if err := r.appendJSONEvent(ctx, input, "context.pack.built", map[string]interface{}{
			"estimatedTokens":     pack.EstimatedTokens,
			"packEstimatedTokens": pack.EstimatedTokens,
			"tokenBudget":         pack.TokenBudget,
			"contextWindowTokens": p.ContextWindowTokens,
			"budgetRatio":         contextpack.DefaultBudgetRatio,
			"fileSummaryCount":    len(pack.FileSummaries),
			"selectedFilePaths":   contextPackFilePaths(pack.FileSummaries),
			"hasTaskSummary":      pack.TaskSummary != "",
			"truncated":           pack.Truncated,
			"indexStats":          pack.IndexStats,
		}); err != nil {
			return nil, err
		}
		contextText := renderContextPack(pack)
		if contextText != "" {
			systemPrompt += "\n\n" + contextText
		}
	}
	skillContext, err := r.renderSkillContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("build skill context: %w", err)
	}
	if skillContext != "" {
		systemPrompt += "\n\n" + skillContext
	}
	systemPrompt += fmt.Sprintf("\n\n当前运行环境：os=%s，arch=%s。用户询问“本机/电脑/当前机器”的系统信息时，指的是运行若水（Water）后端的这台机器。", runtime.GOOS, runtime.GOARCH)
	systemPrompt += fmt.Sprintf("\n\n服务边界：当前工作区根目录是 `%s`。若水后端是控制面，不是工作区里的业务后端。测试工作区 API 前，必须从 application.yml、application.properties、Vite 配置、启动脚本或真实启动输出确认业务端口；禁止把若水自身 API 地址当成业务地址，禁止用猜测账号反复请求。", ws.RootPath)
	systemPrompt += " 当前工作区根目录已由 Harness 提供，不得向用户追问工作区路径；只有确实需要访问该根目录之外且尚未授权的路径时，才请求具体的外部路径授权。"
	systemPrompt += fmt.Sprintf(
		"\n\n任务契约（由 Harness 维护，不可忽略）：\n目标：%s\n任务类型：%s\n当前阶段：%s\n完成条件：\n- %s\n"+
			"你不能自行决定数据库中的完成状态。停止调用工具时，Harness 会根据真实工具事件复核这些条件；缺少用户输入时必须明确列出具体缺失项。",
		contract.Goal,
		contract.TaskType,
		contract.Stage,
		strings.Join(contract.DoneWhen, "\n- "),
	)
	ledgerContext, err := r.hypothesisContext(ctx, input.TaskID, contract)
	if err != nil {
		return nil, fmt.Errorf("build hypothesis context: %w", err)
	}
	if ledgerContext != "" {
		systemPrompt += "\n\n" + ledgerContext
	}
	plan, err := taskplan.NewStore(r.db).Get(ctx, input.TaskID, contract.Goal)
	if err != nil {
		return nil, fmt.Errorf("load task plan: %w", err)
	}
	systemPrompt += "\n\n" + renderTaskPlan(plan)
	systemPrompt += "\n\n可用通用工具：list_dir、read_file、read_document、read_skill、write_file、run_command。文本和代码使用 read_file；PDF、DOCX、XLS/XLSX、PPTX 必须使用 read_document，并在 truncated=true 时按 nextOffset 继续读取；任务匹配已启用 Skill 时使用 read_skill 按需加载完整说明。遇到文件、目录、磁盘空间、内存使用、CPU 使用率、系统信息、Git 状态、测试结果或其他真实环境信息时，先自己选择合适的通用工具，再基于工具结果继续推理；不要猜测，也不要等待专用工具。系统信息可优先用只读命令，例如磁盘 df -h /，macOS(darwin) 内存 vm_stat 与 sysctl hw.memsize、CPU top -l 1 -s 0 -n 0，Linux 内存 free -h、CPU top -bn1，Windows 内存 wmic OS get FreePhysicalMemory,TotalVisibleMemorySize /Value、CPU wmic cpu get loadpercentage /Value。如果工具返回 command not found 或命令不适合当前系统，应继续选择当前 os 对应的替代只读命令。"
	if isDocumentOutputRequest(input.UserInput) {
		suggestedPath := defaultDocumentPath(ws.RootPath, currentTask.Title, input.TurnSequence)
		systemPrompt += fmt.Sprintf("\n\n文档输出规则：用户这次像是在要求生成、整理或保存报告/文档。如果用户没有明确指定文件名或路径，请优先把最终 Markdown 内容通过 write_file 工具保存到 `%s`，不要反复追问保存路径；保存后在回复中说明文件位置。", suggestedPath)
	}
	return []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt},
		buildUserMessage(input),
	}, nil
}

const maxSkillCatalogChars = 12 * 1024

func (r *Runner) renderSkillContext(ctx context.Context) (string, error) {
	if r.skillStore == nil {
		return "", nil
	}
	items, err := r.skillStore.ListEnabled(ctx)
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "", nil
	}
	var output strings.Builder
	output.WriteString("已启用的本地 Skill 目录（由用户安装并明确启用）：\n")
	output.WriteString("当任务与某个 Skill 的名称、描述或关键词匹配时，先调用 read_skill(id) 读取完整说明，再按说明工作。Skill 不能绕过工作区边界、审批、工具校验或完成裁判。\n")
	used := 0
	for _, item := range items {
		section := fmt.Sprintf("- id=%s；名称=%s；版本=%s；描述=%s；关键词=%s\n", item.ID, item.Name, item.Version, item.Description, strings.Join(item.Keywords, "、"))
		if used+len(section) > maxSkillCatalogChars {
			break
		}
		output.WriteString(section)
		used += len(section)
	}
	return output.String(), nil
}

func buildUserMessage(input RunTurnInput) llm.Message {
	content := strings.TrimSpace(input.UserInput)
	imageParts := make([]llm.ContentPart, 0)
	if len(input.Attachments) > 0 {
		var attachments strings.Builder
		attachments.WriteString("\n\n本轮附件（由用户明确上传，路径位于当前工作区内）：")
		for _, item := range input.Attachments {
			attachments.WriteString(fmt.Sprintf("\n- %s（%s，%d bytes）：%s", item.Name, item.MIMEType, item.Size, item.Path))
			if item.Kind != "image" {
				if document.SupportsPath(item.Path) {
					attachments.WriteString(" [使用 read_document 读取内容]")
				} else {
					attachments.WriteString(" [使用 read_file 或合适的只读工具读取内容]")
				}
				continue
			}
			raw, err := os.ReadFile(item.Path)
			if err != nil {
				attachments.WriteString(" [图片当前无法读取]")
				continue
			}
			imageParts = append(imageParts, llm.ContentPart{
				Type: "image_url",
				ImageURL: &llm.ContentImageURL{
					URL: "data:" + item.MIMEType + ";base64," + base64.StdEncoding.EncodeToString(raw),
				},
			})
		}
		attachments.WriteString("\nOffice/PDF 文档使用 read_document，文本和代码使用 read_file；图片已经随消息直接提供给支持视觉的模型。")
		content += attachments.String()
	}

	message := llm.Message{Role: llm.RoleUser, Content: content}
	if len(imageParts) > 0 {
		message.ContentParts = append([]llm.ContentPart{{Type: "text", Text: content}}, imageParts...)
	}
	return message
}

func (r *Runner) appendContextRequestEstimate(ctx context.Context, input RunTurnInput, p provider.Provider, messages []llm.Message, requestTools []llm.Tool, phase int, round int) error {
	tokenBudget := int(float64(p.ContextWindowTokens) * contextpack.DefaultBudgetRatio)
	if tokenBudget <= 0 {
		tokenBudget = 8192
	}
	return r.appendJSONEvent(ctx, input, "context.request.estimated", map[string]interface{}{
		"estimatedTokens":     estimateChatRequestTokens(messages, requestTools),
		"tokenBudget":         tokenBudget,
		"contextWindowTokens": p.ContextWindowTokens,
		"round":               round,
		"phase":               phase,
		"messageCount":        len(messages),
		"toolCount":           len(requestTools),
		"source":              "llm_request",
	})
}

func (r *Runner) collectAssistantRound(ctx context.Context, client llm.Client, messages []llm.Message, requestTools []llm.Tool, input RunTurnInput, ws workspace.Workspace, currentTask task.Task) (llm.Message, []llm.ToolCall, string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream, err := client.ChatStream(ctx, llm.ChatRequest{
		Messages: messages,
		Tools:    requestTools,
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
	validationByCommand := make(map[string]int)
	failures := make([]string, 0)
	seenUserInputs := make(map[string]struct{})
	seenFailures := make(map[string]struct{})

	for _, item := range events {
		switch item.Type {
		case "turn.started":
			var payload struct {
				UserInput string `json:"userInput"`
			}
			if err := json.Unmarshal([]byte(item.PayloadJSON), &payload); err == nil {
				input := compactSummaryLine(payload.UserInput, 160)
				if input != "" && !isGenericContinuationInput(input) {
					if _, exists := seenUserInputs[input]; !exists {
						seenUserInputs[input] = struct{}{}
						userInputs = append(userInputs, input)
					}
				}
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
				command := strings.TrimSpace(validation.Command)
				if command == "" {
					continue
				}
				if index, exists := validationByCommand[command]; exists {
					validations[index] = validation
					continue
				}
				validationByCommand[command] = len(validations)
				validations = append(validations, validation)
			}
		case "turn.failed", "turn.interrupted":
			var payload struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal([]byte(item.PayloadJSON), &payload); err == nil && strings.TrimSpace(payload.Message) != "" {
				failure := compactSummaryLine(item.Type+": "+payload.Message, 180)
				if _, exists := seenFailures[failure]; !exists {
					seenFailures[failure] = struct{}{}
					failures = append(failures, failure)
				}
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

func isGenericContinuationInput(value string) bool {
	value = strings.TrimSpace(value)
	if value == "继续" || value == "继续任务" || value == "继续修复" {
		return true
	}
	return strings.HasPrefix(value, "继续上一轮任务")
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
		"go build",
		"npm run build",
		"npm test",
		"npm run test",
		"npm run lint",
		"pnpm test",
		"pnpm run build",
		"pnpm run lint",
		"yarn test",
		"yarn build",
		"yarn lint",
		"vite build",
		"vue-tsc",
		"mvn test",
		"mvn verify",
		"mvn package",
		"mvn compile",
		"mvnw test",
		"mvnw verify",
		"mvnw package",
		"mvnw compile",
		"gradle test",
		"gradle build",
		"gradlew test",
		"gradlew build",
		"cargo test",
		"cargo check",
		"cargo build",
		"pytest",
		"python -m pytest",
		"python3 -m pytest",
		"unittest",
		"dotnet test",
		"dotnet build",
		"make test",
		"make check",
		"verify:e2e",
		"verify-e2e",
		"verify:register",
		"verify:login",
		"verify:user_crud",
		"acceptance",
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

func (r *Runner) executeToolCalls(ctx context.Context, input RunTurnInput, ws workspace.Workspace, currentTask task.Task, contract taskcontract.Contract, state *toolExecutionState, calls []llm.ToolCall, approvalID ...string) ([]llm.Message, bool, []toolLoopObservation, error) {
	if r.toolExecutor == nil {
		return nil, false, nil, nil
	}
	toolMessages := make([]llm.Message, 0, len(calls))
	observations := make([]toolLoopObservation, 0, len(calls))
	for _, call := range calls {
		if err := r.ensureTurnActive(ctx, input.TurnID); err != nil {
			return nil, false, nil, err
		}
		originalCall := call
		call, normalization := normalizeToolCall(call, ws.RootPath)
		if normalization.Corrected() {
			if err := r.appendJSONEvent(ctx, input, "tool.call.corrected", map[string]interface{}{
				"from":            normalization.OriginalName,
				"to":              normalization.CanonicalName,
				"argumentAliases": normalization.ArgumentAliases,
				"defaulted":       normalization.Defaults,
				"toolCallId":      call.ID,
				"message":         "已在 Harness 边界自动归一化工具名称或参数。",
			}); err != nil {
				return nil, false, nil, err
			}
		}
		signature := toolCallSignature(call)
		intent := toolResourceIntent(call, ws)
		if state != nil && isCacheableReadTool(call.Function.Name) {
			if state.readCache == nil {
				state.readCache = make(map[string]cachedToolResult)
			}
			if state.resourceCache == nil {
				state.resourceCache = make(map[string]cachedToolResult)
			}
			var cached *cachedToolResult
			cacheReason := ""
			if item, ok := state.readCache[signature]; ok {
				cachedCopy := item
				cached = &cachedCopy
				cacheReason = "same_call"
			} else if key := resourceCacheKey(intent); key != "" {
				if item, ok := state.resourceCache[key]; ok {
					cachedCopy := item
					cached = &cachedCopy
					cacheReason = "same_resource"
				}
			}
			if cached != nil {
				if err := r.appendJSONEvent(ctx, input, "tool.call.cached", map[string]interface{}{
					"name":        call.Function.Name,
					"toolCallId":  call.ID,
					"signature":   signature,
					"contentHash": cached.fingerprint,
					"cacheReason": cacheReason,
					"message":     "资源未发生已知修改，复用本轮已有结果；模型应直接使用缓存结果推进。",
				}); err != nil {
					return nil, false, nil, err
				}
				toolMessages = append(toolMessages, llm.Message{
					Role:       llm.RoleTool,
					ToolCallID: call.ID,
					Name:       call.Function.Name,
					Content:    cachedToolOutputForLLM(cached.result.Output, cached.fingerprint),
				})
				observation, evidenceErr := r.recordToolEvidence(ctx, input, contract, call, intent, cached.result.Output, nil)
				if evidenceErr != nil {
					return nil, false, nil, evidenceErr
				}
				observations = append(observations, observation)
				planMessage, planErr := r.assessTaskPlan(ctx, input, contract, call, cached.result, nil, observation.evidenceID)
				if planErr != nil {
					return nil, false, nil, planErr
				}
				if planMessage != "" {
					toolMessages = append(toolMessages, llm.Message{Role: llm.RoleSystem, Content: planMessage})
				}
				continue
			}
		}
		if err := r.appendJSONEvent(ctx, input, "tool.call.started", map[string]interface{}{
			"name":         call.Function.Name,
			"toolCallId":   call.ID,
			"originalName": originalCall.Function.Name,
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
		if code, suggestedTool, ok := tools.ErrorDetails(err); ok && suggestedTool == tools.NameListDir {
			if appendErr := r.appendJSONEvent(ctx, input, "tool.call.corrected", map[string]interface{}{
				"from":       call.Function.Name,
				"to":         suggestedTool,
				"code":       code,
				"toolCallId": call.ID,
			}); appendErr != nil {
				return nil, false, nil, appendErr
			}
			result, err = r.toolExecutor.Execute(ctx, tools.Context{
				Workspace: ws,
				Task:      currentTask,
				TurnID:    input.TurnID,
			}, tools.Request{
				RequestID:  input.RequestID,
				Name:       suggestedTool,
				Arguments:  json.RawMessage(call.Function.Arguments),
				ApprovalID: reqApprovalID,
				ToolCallID: call.ID,
			})
			if err == nil {
				result.Output = correctedToolOutput(result.Output, call.Function.Name, suggestedTool)
			}
		}
		if errors.Is(err, tools.ErrApprovalRequired) {
			if err := r.appendJSONEvent(ctx, input, "approval.requested", map[string]interface{}{"approval": result.Approval}); err != nil {
				return nil, false, nil, err
			}
			return toolMessages, true, observations, nil
		}
		if err != nil {
			code, suggestedTool, retryable, structuredHint, structured := tools.ErrorMetadata(err)
			hint := toolFailureHint(err, ws)
			if structuredHint != "" {
				hint = structuredHint
			}
			if appendErr := r.appendJSONEvent(ctx, input, "tool.failed", map[string]interface{}{
				"name":          call.Function.Name,
				"message":       err.Error(),
				"hint":          hint,
				"code":          code,
				"suggestedTool": suggestedTool,
				"retryable":     retryable,
				"structured":    structured,
			}); appendErr != nil {
				return nil, false, nil, appendErr
			}
			toolMessages = append(toolMessages, llm.Message{
				Role:       llm.RoleTool,
				ToolCallID: call.ID,
				Name:       call.Function.Name,
				Content:    toolErrorContent(err, ws),
			})
			observation, evidenceErr := r.recordToolEvidence(ctx, input, contract, call, intent, nil, err)
			if evidenceErr != nil {
				return nil, false, nil, evidenceErr
			}
			observations = append(observations, observation)
			continue
		}
		if state != nil && !isCacheableReadTool(call.Function.Name) {
			clear(state.readCache)
			clear(state.resourceCache)
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
		if kind, suggestion, ok := toolOutputRecovery(result.Output); ok {
			if err := r.appendJSONEvent(ctx, input, "agent.recovery.suggested", map[string]interface{}{
				"tool":       call.Function.Name,
				"kind":       kind,
				"suggestion": suggestion,
				"toolCallId": call.ID,
			}); err != nil {
				return nil, false, nil, err
			}
			toolMessages = append(toolMessages, llm.Message{
				Role:    llm.RoleSystem,
				Content: suggestion,
			})
		}
		observation, evidenceErr := r.recordToolEvidence(ctx, input, contract, call, intent, result.Output, nil)
		if evidenceErr != nil {
			return nil, false, nil, evidenceErr
		}
		observations = append(observations, observation)
		planMessage, planErr := r.assessTaskPlan(ctx, input, contract, call, result, nil, observation.evidenceID)
		if planErr != nil {
			return nil, false, nil, planErr
		}
		if planMessage != "" {
			toolMessages = append(toolMessages, llm.Message{Role: llm.RoleSystem, Content: planMessage})
		}
		if state != nil && isCacheableReadTool(call.Function.Name) {
			cached := cachedToolResult{
				result:      result,
				fingerprint: toolOutputFingerprint(result.Output),
			}
			state.readCache[toolCallSignature(call)] = cached
			cacheIntent := intent
			if values, ok := result.Output.(map[string]interface{}); ok && stringFromMap(values, "correctedTo") == tools.NameListDir {
				cacheIntent.Kind = "directory"
			}
			if key := resourceCacheKey(cacheIntent); key != "" {
				state.resourceCache[key] = cached
			}
		}
	}
	return toolMessages, false, observations, nil
}

func newToolExecutionState() *toolExecutionState {
	return &toolExecutionState{
		readCache:     make(map[string]cachedToolResult),
		resourceCache: make(map[string]cachedToolResult),
	}
}

func normalizeToolCall(call llm.ToolCall, workspaceRoot string) (llm.ToolCall, tools.Normalization) {
	req, normalization := tools.NormalizeRequest(tools.Request{
		Name:      call.Function.Name,
		Arguments: json.RawMessage(call.Function.Arguments),
	}, workspaceRoot)
	call.Function.Name = req.Name
	call.Function.Arguments = string(req.Arguments)
	return call, normalization
}

func resourceCacheKey(intent resourceIntent) string {
	if intent.Resource == "" || (intent.Kind != "file" && intent.Kind != "directory") {
		return ""
	}
	return intent.Kind + "|" + filepath.Clean(intent.Resource)
}

func isCacheableReadTool(name string) bool {
	return name == tools.NameReadFile || name == tools.NameListDir
}

func cachedToolOutputForLLM(output interface{}, fingerprint string) string {
	value := map[string]interface{}{
		"cached":      true,
		"contentHash": fingerprint,
		"message":     "该资源在本轮没有已知修改，以下是此前读取到的真实结果；直接复用它推进，不要继续重复读取。",
		"output":      output,
	}
	if outputMap, ok := output.(map[string]interface{}); ok {
		if path := stringFromMap(outputMap, "path"); path != "" {
			value["path"] = path
		}
	}
	return stringifyToolOutputForLLM(value)
}

func correctedToolOutput(output interface{}, from string, to string) interface{} {
	value, ok := output.(map[string]interface{})
	if !ok {
		return map[string]interface{}{
			"output":        output,
			"correctedFrom": from,
			"correctedTo":   to,
		}
	}
	corrected := make(map[string]interface{}, len(value)+2)
	for key, item := range value {
		corrected[key] = item
	}
	corrected["correctedFrom"] = from
	corrected["correctedTo"] = to
	return corrected
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
	if _, _, _, hint, ok := tools.ErrorMetadata(err); ok && hint != "" {
		return hint
	}
	if errors.Is(err, sandbox.ErrAccessDenied) {
		return "只能访问工作区根目录 " + ws.RootPath + " 或已经授权的外部路径；不要重复尝试未授权绝对路径。"
	}
	if code, suggestedTool, _, _, ok := tools.ErrorMetadata(err); ok {
		switch code {
		case "target_is_directory":
			return "目标是目录，请改用 " + suggestedTool + " 查看目录内容。"
		case "missing_path":
			return "先使用 " + stringWithDefault(suggestedTool, tools.NameListDir) + " 确认工作区内的具体路径。"
		case "invalid_arguments":
			return "修正参数 JSON，并使用工具定义中的标准字段名后重试。"
		}
	}
	return ""
}

func toolOutputRecovery(output interface{}) (kind string, suggestion string, ok bool) {
	values, exists := output.(map[string]interface{})
	if !exists || values == nil {
		return "", "", false
	}
	if success, exists := values["success"]; exists {
		if value, isBool := success.(bool); !isBool || value {
			return "", "", false
		}
	} else if stringFromMap(values, "error") == "" {
		return "", "", false
	}
	command := stringFromMap(values, "command")
	errorKind := stringFromMap(values, "errorKind")
	hint := stringFromMap(values, "hint")
	switch errorKind {
	case "timeout":
		return "timeout", fmt.Sprintf("命令 `%s` 超时。不要原样重试；请拆分为更小的检查或测试，缩小范围并设置明确的 timeoutMs。", command), true
	case "canceled":
		return "canceled", "命令被取消。请确认当前任务仍在运行，再选择一个可在前台结束的短命令继续。", true
	}
	if hint != "" {
		return "command_recovery", "命令未成功，禁止原样重试。请按 Harness 提供的替代建议执行：" + hint, true
	}
	if command != "" {
		return "command_failure", fmt.Sprintf("命令 `%s` 未成功。请先读取失败输出涉及的文件或配置，修正原因后再运行同一个验证；不要连续重复失败命令。", command), true
	}
	return "command_failure", "工具返回 success=false。请根据错误输出切换到具体的替代路径，不要原样重试。", true
}

func toolErrorContent(err error, ws workspace.Workspace) string {
	hint := toolFailureHint(err, ws)
	payload := map[string]interface{}{
		"error":   err.Error(),
		"success": false,
	}
	if code, suggestedTool, retryable, _, ok := tools.ErrorMetadata(err); ok {
		payload["code"] = code
		payload["suggestedTool"] = suggestedTool
		payload["retryable"] = retryable
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
		if item.MatchReason != "" {
			out.WriteString("\n命中原因: ")
			out.WriteString(item.MatchReason)
		}
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
	case task.TurnStatusBlocked, task.TurnStatusPaused, task.TurnStatusInterrupted, task.TurnStatusCompleted, task.TurnStatusFailed:
		return true, nil
	default:
		return false, nil
	}
}

func (r *Runner) appendJSONEvent(ctx context.Context, input RunTurnInput, eventType string, payload map[string]interface{}) error {
	_, err := r.appendJSONEventResult(ctx, input, eventType, payload)
	return err
}

func (r *Runner) appendJSONEventResult(ctx context.Context, input RunTurnInput, eventType string, payload map[string]interface{}) (event.Event, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return event.Event{}, fmt.Errorf("encode event payload: %w", err)
	}
	item, err := r.appendEvent(ctx, event.AppendInput{
		RequestID:   input.RequestID,
		WorkspaceID: input.WorkspaceID,
		TaskID:      input.TaskID,
		TurnID:      input.TurnID,
		Type:        eventType,
		PayloadJSON: string(raw),
	})
	return item, err
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
