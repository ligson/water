package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ligson/water/water-be/internal/event"
	"github.com/ligson/water/water-be/internal/llm"
	"github.com/ligson/water/water-be/internal/taskcontract"
	"github.com/ligson/water/water-be/internal/taskplan"
	"github.com/ligson/water/water-be/internal/taskreplay"
	"github.com/ligson/water/water-be/internal/tools"
)

func (r *Runner) ensureTaskPlan(ctx context.Context, input RunTurnInput, contract taskcontract.Contract) (taskplan.Plan, error) {
	plan, created, err := taskplan.NewStore(r.db).Ensure(ctx, input.TaskID, contract.Goal, contract.TaskType)
	if err != nil {
		return taskplan.Plan{}, err
	}
	if created {
		if err := r.appendJSONEvent(ctx, input, "agent.plan.created", planPayload(plan)); err != nil {
			return taskplan.Plan{}, err
		}
	}
	plan, recoveredSteps, err := r.recoverTaskPlanFromHistory(ctx, input.TaskID, contract.Goal, plan)
	if err != nil {
		return taskplan.Plan{}, err
	}
	if recoveredSteps > 0 {
		if err := r.appendJSONEvent(ctx, input, "agent.plan.history.recovered", map[string]interface{}{
			"planId":         plan.ID,
			"recoveredSteps": recoveredSteps,
			"nextStep":       currentPlanStep(plan),
			"planComplete":   plan.Status == taskplan.StatusCompleted,
			"message":        "若水已根据历史成功工具结果恢复执行计划进度，不会重复已通过的验收步骤。",
		}); err != nil {
			return taskplan.Plan{}, err
		}
	}
	if err := r.appendJSONEvent(ctx, input, "agent.plan.snapshot", planPayload(plan)); err != nil {
		return taskplan.Plan{}, err
	}
	items, err := event.NewStore(r.db).ListByTask(ctx, input.TaskID)
	if err != nil {
		return taskplan.Plan{}, err
	}
	report := taskreplay.Assess(items)
	if err := r.appendJSONEvent(ctx, input, "agent.replay.assessed", map[string]interface{}{
		"score":               report.Score,
		"turns":               report.Turns,
		"completedTurns":      report.CompletedTurns,
		"interruptedTurns":    report.InterruptedTurns,
		"failedTurns":         report.FailedTurns,
		"toolCalls":           report.ToolCalls,
		"toolFailures":        report.ToolFailures,
		"correctedToolCalls":  report.CorrectedToolCalls,
		"cachedReads":         report.CachedReads,
		"structuredErrors":    report.StructuredErrors,
		"replans":             report.Replans,
		"recoverySuggestions": report.RecoverySuggestions,
		"repeatedReads":       report.RepeatedReads,
		"writes":              report.Writes,
		"validations":         report.Validations,
		"failedValidations":   report.FailedValidations,
		"endToEndVerified":    report.EndToEndVerified,
		"findings":            report.Findings,
	}); err != nil {
		return taskplan.Plan{}, err
	}
	return plan, nil
}

func (r *Runner) recoverTaskPlanFromHistory(ctx context.Context, taskID string, goal string, plan taskplan.Plan) (taskplan.Plan, int, error) {
	if plan.Status == taskplan.StatusCompleted {
		return plan, 0, nil
	}
	items, err := event.NewStore(r.db).ListByTask(ctx, taskID)
	if err != nil {
		return taskplan.Plan{}, 0, err
	}
	store := taskplan.NewStore(r.db)
	recoveredSteps := 0
	for _, item := range items {
		if item.Type != "tool.completed" || item.CreatedAt.Before(plan.CreatedAt) {
			continue
		}
		var payload struct {
			Name   string                 `json:"name"`
			Output map[string]interface{} `json:"output"`
		}
		if json.Unmarshal([]byte(item.PayloadJSON), &payload) != nil || strings.TrimSpace(payload.Name) == "" {
			continue
		}
		updated, advanced, err := store.Assess(ctx, taskID, goal, taskplan.ToolObservation{
			ToolName:   payload.Name,
			OutputText: stringifyToolOutput(payload.Output),
			Succeeded:  toolResultSucceeded(payload.Output),
		})
		if err != nil {
			return taskplan.Plan{}, recoveredSteps, err
		}
		if !advanced {
			continue
		}
		recoveredSteps += completedStepCount(updated) - completedStepCount(plan)
		plan = updated
		if plan.Status == taskplan.StatusCompleted {
			break
		}
	}
	return plan, recoveredSteps, nil
}

func completedStepCount(plan taskplan.Plan) int {
	count := 0
	for _, step := range plan.Steps {
		if step.Status == taskplan.StatusCompleted {
			count++
		}
	}
	return count
}

func (r *Runner) appendPlanSnapshot(ctx context.Context, input RunTurnInput, plan taskplan.Plan) error {
	return r.appendJSONEvent(ctx, input, "agent.plan.snapshot", planPayload(plan))
}

func planPayload(plan taskplan.Plan) map[string]interface{} {
	return map[string]interface{}{
		"id":           plan.ID,
		"contractGoal": plan.ContractGoal,
		"version":      plan.Version,
		"status":       plan.Status,
		"steps":        plan.Steps,
	}
}

func renderTaskPlan(plan taskplan.Plan) string {
	var out strings.Builder
	out.WriteString("持久化执行计划（Harness 维护，必须按顺序通过验收闸门）：\n")
	for _, step := range plan.Steps {
		marker := "[ ]"
		if step.Status == taskplan.StatusCompleted {
			marker = "[x]"
		} else if step.Status == taskplan.StatusInProgress {
			marker = "[>]"
		} else if step.Status == taskplan.StatusBlocked {
			marker = "[!]"
		}
		out.WriteString(fmt.Sprintf("%s %d. %s (%s)\n", marker, step.Position, step.Title, step.GateType))
		if step.Status == taskplan.StatusInProgress {
			out.WriteString("   当前唯一主步骤：")
			out.WriteString(step.Description)
			out.WriteString("\n   验收条件：")
			out.WriteString(strings.Join(step.Acceptance, "；"))
			out.WriteString("\n")
		}
	}
	out.WriteString("不要提前执行后续步骤。当前步骤未通过时，应修复该步骤或明确阻塞输入；禁止只读文件后宣称功能完成。优先查找并运行项目已有的测试或验收脚本；存在 UserManagementIntegrationTest 时，CRUD 和端到端阶段应明确运行该测试，不要继续手写零散 curl。验收命令也可在目的或输出中使用 verify:register、verify:login、verify:user_crud、verify:e2e 和 WATER_E2E_OK 标记。")
	return strings.TrimSpace(out.String())
}

func (r *Runner) assessTaskPlan(ctx context.Context, input RunTurnInput, contract taskcontract.Contract, call llm.ToolCall, result tools.Result, executionErr error, evidenceID string) (string, error) {
	if executionErr != nil {
		return "", nil
	}
	outputText := stringifyToolOutput(result.Output)
	observation := taskplan.ToolObservation{
		ToolName:   call.Function.Name,
		Arguments:  call.Function.Arguments,
		Purpose:    toolPurpose(call.Function.Arguments),
		OutputText: outputText,
		Succeeded:  toolResultSucceeded(result.Output),
		EvidenceID: evidenceID,
	}
	updated, advanced, err := taskplan.NewStore(r.db).Assess(ctx, input.TaskID, contract.Goal, observation)
	if err != nil {
		return "", err
	}
	if !advanced {
		return "", nil
	}
	if err := r.appendJSONEvent(ctx, input, "agent.plan.step.completed", map[string]interface{}{
		"planId":       updated.ID,
		"evidenceId":   evidenceID,
		"nextStep":     currentPlanStep(updated),
		"planComplete": updated.Status == taskplan.StatusCompleted,
	}); err != nil {
		return "", err
	}
	if err := r.appendPlanSnapshot(ctx, input, updated); err != nil {
		return "", err
	}
	return "Harness 已验收当前步骤并推进计划。\n" + renderTaskPlan(updated), nil
}

func currentPlanStep(plan taskplan.Plan) interface{} {
	for _, step := range plan.Steps {
		if step.Status == taskplan.StatusInProgress || step.Status == taskplan.StatusBlocked {
			return step
		}
	}
	return nil
}

func toolResultSucceeded(output interface{}) bool {
	values, ok := output.(map[string]interface{})
	if !ok {
		return true
	}
	if success, exists := values["success"]; exists {
		if value, ok := success.(bool); ok && !value {
			return false
		}
	}
	return strings.TrimSpace(stringFromMap(values, "error")) == ""
}

func toolPurpose(arguments string) string {
	var values map[string]interface{}
	if json.Unmarshal([]byte(arguments), &values) != nil {
		return ""
	}
	return strings.TrimSpace(stringFromMap(values, "purpose"))
}
