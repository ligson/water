package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ligson/water/water-be/internal/event"
	"github.com/ligson/water/water-be/internal/llm"
	"github.com/ligson/water/water-be/internal/taskcontract"
	"github.com/ligson/water/water-be/internal/taskplan"
	"github.com/ligson/water/water-be/internal/tools"
)

type completionOutcome string

const (
	completionCompleted completionOutcome = "completed"
	completionContinue  completionOutcome = "continue"
	completionBlocked   completionOutcome = "blocked"
)

type completionDecision struct {
	Outcome       completionOutcome
	Reason        string
	MissingInputs []string
}

type completionEvidence struct {
	writtenFiles          int
	successfulTools       int
	failedTools           int
	successfulValidations int
	failedValidations     int
	pathAccessDenied      bool
}

func (r *Runner) ensureTaskContract(ctx context.Context, input RunTurnInput) (taskcontract.Contract, error) {
	store := taskcontract.NewStore(r.db)
	contract, err := store.Get(ctx, input.TaskID)
	if err == nil {
		if isGenericContinuationInput(input.UserInput) && contract.Stage == taskcontract.StageBlocked {
			plan, planErr := taskplan.NewStore(r.db).Get(ctx, input.TaskID, contract.Goal)
			if planErr == nil && plan.Status == taskplan.StatusCompleted {
				contract.Stage = taskcontract.StageCompleted
				contract.MissingInputs = nil
				contract, err = store.Upsert(ctx, contract)
				if err == nil {
					err = r.appendContractEvent(ctx, input, contract)
				}
				return contract, err
			}
			if planErr != nil && !errors.Is(planErr, taskplan.ErrNotFound) {
				return taskcontract.Contract{}, planErr
			}
		}
		if !isGenericContinuationInput(input.UserInput) && contract.Stage == taskcontract.StageCompleted {
			contract = taskcontract.Build(input.TaskID, input.UserInput)
			contract, err = store.Upsert(ctx, contract)
			if err == nil {
				err = r.appendContractEvent(ctx, input, contract)
			}
		} else if !isGenericContinuationInput(input.UserInput) && contract.Stage == taskcontract.StageBlocked {
			contract.Stage = taskcontract.StageCollectingEvidence
			contract.MissingInputs = nil
			contract, err = store.Upsert(ctx, contract)
			if err == nil {
				err = r.appendContractEvent(ctx, input, contract)
			}
		}
		return contract, err
	}
	if err != taskcontract.ErrNotFound {
		return taskcontract.Contract{}, err
	}
	goal, err := r.contractGoal(ctx, input)
	if err != nil {
		return taskcontract.Contract{}, err
	}
	contract = taskcontract.Build(input.TaskID, goal)
	contract, err = store.Upsert(ctx, contract)
	if err != nil {
		return taskcontract.Contract{}, err
	}
	if err := r.appendContractEvent(ctx, input, contract); err != nil {
		return taskcontract.Contract{}, err
	}
	return contract, nil
}

func (r *Runner) contractGoal(ctx context.Context, input RunTurnInput) (string, error) {
	if !isGenericContinuationInput(input.UserInput) {
		return input.UserInput, nil
	}
	items, err := event.NewStore(r.db).ListByTask(ctx, input.TaskID)
	if err != nil {
		return "", err
	}
	bestGoal := ""
	bestScore := -1
	for index := 0; index < len(items); index++ {
		if items[index].Type != "turn.started" {
			continue
		}
		var payload struct {
			UserInput string `json:"userInput"`
		}
		if json.Unmarshal([]byte(items[index].PayloadJSON), &payload) == nil && !isGenericContinuationInput(payload.UserInput) && strings.TrimSpace(payload.UserInput) != "" {
			score := legacyGoalScopeScore(payload.UserInput)
			if score > bestScore {
				bestScore = score
				bestGoal = payload.UserInput
			}
		}
	}
	if bestGoal != "" {
		return bestGoal, nil
	}
	return input.UserInput, nil
}

func legacyGoalScopeScore(value string) int {
	lower := strings.ToLower(strings.TrimSpace(value))
	score := min(len([]rune(lower))/24, 4)
	groups := [][]string{
		{"登录", "login"},
		{"注册", "register"},
		{"用户管理", "用户信息", "crud", "user management"},
		{"前端", "vue", "react"},
		{"后端", "spring", "golang", "go "},
		{"测试", "联调", "验收"},
	}
	for _, group := range groups {
		if containsAny(lower, group) {
			score += 3
		}
	}
	if containsAny(lower, []string{"为什么", "为何", "报错", "错误", "401", "403", "完成了吗", "启动"}) {
		score--
	}
	return score
}

func (r *Runner) updateContractStage(ctx context.Context, input RunTurnInput, contract taskcontract.Contract, stage string, missingInputs []string) (taskcontract.Contract, error) {
	contract.Stage = stage
	contract.MissingInputs = uniqueNonEmpty(missingInputs)
	updated, err := taskcontract.NewStore(r.db).Upsert(ctx, contract)
	if err != nil {
		return taskcontract.Contract{}, err
	}
	if err := r.appendContractEvent(ctx, input, updated); err != nil {
		return taskcontract.Contract{}, err
	}
	return updated, nil
}

func (r *Runner) appendContractEvent(ctx context.Context, input RunTurnInput, contract taskcontract.Contract) error {
	return r.appendJSONEvent(ctx, input, "agent.contract.updated", map[string]interface{}{
		"goal":          contract.Goal,
		"taskType":      contract.TaskType,
		"stage":         contract.Stage,
		"doneWhen":      contract.DoneWhen,
		"missingInputs": contract.MissingInputs,
	})
}

func (r *Runner) arbitrateCompletion(ctx context.Context, input RunTurnInput, contract taskcontract.Contract, assistantContent string) (completionDecision, error) {
	evidence, err := r.collectCompletionEvidence(ctx, input, contract)
	if err != nil {
		return completionDecision{}, err
	}
	if !hasSubstantiveAnswer(assistantContent) {
		return completionDecision{Outcome: completionContinue, Reason: "empty_or_nonfinal_answer"}, nil
	}
	planCompleted := false
	if plan, err := taskplan.NewStore(r.db).Get(ctx, input.TaskID, contract.Goal); err == nil {
		planCompleted = plan.Status == taskplan.StatusCompleted
		if planCompleted && hasFeatureAcceptancePlan(plan) {
			return completionDecision{Outcome: completionCompleted, Reason: "plan_acceptance_completed"}, nil
		}
		if plan.Status != taskplan.StatusCompleted && hasFeatureAcceptancePlan(plan) {
			missingInputs := inferMissingInputs(assistantContent, evidence)
			if len(missingInputs) > 0 {
				return completionDecision{Outcome: completionBlocked, Reason: "missing_required_input", MissingInputs: missingInputs}, nil
			}
			return completionDecision{Outcome: completionContinue, Reason: "plan_acceptance_not_completed"}, nil
		}
	} else if !errors.Is(err, taskplan.ErrNotFound) {
		return completionDecision{}, err
	}
	if !planCompleted {
		missingInputs := inferMissingInputs(assistantContent, evidence)
		if len(missingInputs) > 0 {
			return completionDecision{Outcome: completionBlocked, Reason: "missing_required_input", MissingInputs: missingInputs}, nil
		}
	}

	switch contract.TaskType {
	case taskcontract.TypeCodeChange:
		if evidence.writtenFiles == 0 {
			return completionDecision{Outcome: completionContinue, Reason: "implementation_not_observed"}, nil
		}
		if evidence.successfulValidations == 0 || evidence.failedValidations > 0 {
			return completionDecision{Outcome: completionContinue, Reason: "validation_not_passed"}, nil
		}
	case taskcontract.TypeDocument:
		if evidence.writtenFiles == 0 {
			return completionDecision{Outcome: completionContinue, Reason: "document_not_written"}, nil
		}
	case taskcontract.TypeDiagnostic, taskcontract.TypeAnalysis:
		if evidence.successfulTools == 0 {
			return completionDecision{Outcome: completionContinue, Reason: "evidence_not_collected"}, nil
		}
		if evidence.failedValidations > 0 {
			return completionDecision{Outcome: completionContinue, Reason: "verification_failed"}, nil
		}
	}
	return completionDecision{Outcome: completionCompleted, Reason: "completion_criteria_satisfied"}, nil
}

func hasFeatureAcceptancePlan(plan taskplan.Plan) bool {
	for _, step := range plan.Steps {
		if step.GateType == taskplan.GateRegister || step.GateType == taskplan.GateLogin || step.GateType == taskplan.GateUserCRUD {
			return true
		}
	}
	return false
}

func (r *Runner) collectCompletionEvidence(ctx context.Context, input RunTurnInput, contract taskcontract.Contract) (completionEvidence, error) {
	items, err := event.NewStore(r.db).ListByTask(ctx, input.TaskID)
	if err != nil {
		return completionEvidence{}, err
	}
	var evidence completionEvidence
	contractStartSequence := 0
	for _, item := range items {
		if item.Type != "agent.contract.updated" {
			continue
		}
		var payload struct {
			Goal  string `json:"goal"`
			Stage string `json:"stage"`
		}
		if json.Unmarshal([]byte(item.PayloadJSON), &payload) == nil && payload.Goal == contract.Goal && payload.Stage == taskcontract.StageUnderstanding {
			contractStartSequence = item.Sequence
		}
	}
	for _, item := range items {
		if item.Sequence <= contractStartSequence {
			continue
		}
		switch item.Type {
		case "tool.failed":
			evidence.failedTools++
			var payload struct {
				Message string `json:"message"`
			}
			if json.Unmarshal([]byte(item.PayloadJSON), &payload) == nil && strings.Contains(strings.ToLower(payload.Message), "access denied") {
				evidence.pathAccessDenied = true
			}
		case "tool.completed":
			var payload struct {
				Name   string                 `json:"name"`
				Output map[string]interface{} `json:"output"`
			}
			if err := json.Unmarshal([]byte(item.PayloadJSON), &payload); err != nil {
				return completionEvidence{}, fmt.Errorf("decode completion evidence: %w", err)
			}
			evidence.successfulTools++
			if payload.Name == tools.NameWriteFile {
				evidence.writtenFiles++
			}
			if payload.Name == tools.NameRunCommand && looksLikeValidationCommand(stringFromMap(payload.Output, "command")) {
				if toolResultSucceeded(payload.Output) {
					evidence.successfulValidations++
					evidence.pathAccessDenied = false
				} else {
					evidence.failedValidations++
				}
			}
		}
	}
	return evidence, nil
}

func inferMissingInputs(content string, evidence completionEvidence) []string {
	requestText := explicitInputRequestText(content)
	if requestText == "" {
		return nil
	}
	missing := make([]string, 0, 4)
	mentionsKnownWorkspacePath := containsAny(requestText, []string{
		"工作区路径", "工作区根目录", "工作区内的正确路径", "workspace root",
	})
	mentionsExternalPath := containsAny(requestText, []string{
		"工作区外", "外部路径", "外部目录", "external path", "outside the workspace", "outside workspace",
	})
	asksForPathAccess := containsAny(requestText, []string{
		"路径", "目录", "path", "directory",
	}) && containsAny(requestText, []string{
		"授权", "未授权", "permission", "authorize", "allow access", "access denied",
	})
	asksForExternalPath := !mentionsKnownWorkspacePath &&
		((mentionsExternalPath && asksForPathAccess) || (evidence.pathAccessDenied && asksForPathAccess))
	if asksForExternalPath {
		missing = append(missing, "授权需要访问的工作区外路径")
	}
	if containsAny(requestText, []string{"用户名", "账号", "username", "account"}) {
		missing = append(missing, "可复现问题的用户名或测试账号")
	}
	if containsAny(requestText, []string{"密码", "凭据", "password", "credential"}) {
		missing = append(missing, "对应的测试凭据")
	}
	if containsAny(requestText, []string{"响应体", "返回内容", "response body"}) {
		missing = append(missing, "失败请求的完整响应体")
	}
	if containsAny(requestText, []string{"日志", "堆栈", "log", "stack trace"}) {
		missing = append(missing, "失败时的相关日志或错误堆栈")
	}
	if len(missing) == 0 && !mentionsKnownWorkspacePath {
		missing = append(missing, "继续判断所需的关键业务输入")
	}
	return uniqueNonEmpty(missing)
}

func explicitInputRequestText(content string) string {
	parts := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(content)), func(r rune) bool {
		switch r {
		case '\n', '\r', '。', '！', '？', '!', '?', ';', '；':
			return true
		default:
			return false
		}
	})
	requests := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if containsAny(part, []string{
			"请提供", "请补充", "请授权", "需要你提供", "需要你补充", "需要你授权", "需要用户提供", "需要用户补充", "需要用户授权", "当前缺少", "还缺少", "因缺少",
			"please provide", "please authorize", "cannot continue without", "need your permission", "missing input",
		}) {
			requests = append(requests, part)
		}
	}
	return strings.Join(requests, "\n")
}

func hasSubstantiveAnswer(content string) bool {
	content = strings.TrimSpace(content)
	if len([]rune(content)) < 2 {
		return false
	}
	return !assistantPromisedToolUse(content)
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func completionRetryInstruction(decision completionDecision, contract taskcontract.Contract) string {
	return fmt.Sprintf(
		"Harness 尚未接受完成申请（原因：%s）。当前目标：%s。完成条件：%s。"+
			"请立即推进尚未满足的条件；不要重复读取没有变化的资源，也不要只写总结。",
		decision.Reason,
		contract.Goal,
		strings.Join(contract.DoneWhen, "；"),
	)
}

func contractStageForToolCalls(calls []llm.ToolCall) string {
	stage := taskcontract.StageCollectingEvidence
	for _, call := range calls {
		switch strings.TrimSpace(call.Function.Name) {
		case tools.NameWriteFile:
			return taskcontract.StageImplementing
		case tools.NameRunCommand:
			var args struct {
				Command string `json:"command"`
			}
			if json.Unmarshal([]byte(call.Function.Arguments), &args) == nil && looksLikeValidationCommand(args.Command) {
				stage = taskcontract.StageVerifying
			}
		}
	}
	return stage
}
