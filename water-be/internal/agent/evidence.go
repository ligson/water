package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ligson/water/water-be/internal/hypothesisledger"
	"github.com/ligson/water/water-be/internal/llm"
	"github.com/ligson/water/water-be/internal/taskcontract"
	"github.com/ligson/water/water-be/internal/tools"
	"github.com/ligson/water/water-be/internal/workspace"
)

const evidenceSnapshotLimit = 12

var shellTokenPattern = regexp.MustCompile(`(?:"[^"]*"|'[^']*'|[^\s|;&]+)`)

type resourceIntent struct {
	Purpose      string
	HypothesisID string
	Operation    string
	Kind         string
	Resource     string
}

func (r *Runner) ensureHypothesisLedger(ctx context.Context, input RunTurnInput, contract taskcontract.Contract) error {
	store := hypothesisledger.NewStore(r.db)
	if err := store.ReopenBlocked(ctx, input.TaskID, contract.Goal); err != nil {
		return err
	}
	items := seedHypotheses(contract)
	for _, seed := range items {
		item, created, err := store.Ensure(ctx, input.TaskID, contract.Goal, seed.claim, seed.missingEvidence)
		if err != nil {
			return err
		}
		if created {
			if err := r.appendJSONEvent(ctx, input, "agent.hypothesis.updated", map[string]interface{}{
				"id":              item.ID,
				"claim":           item.Claim,
				"status":          item.Status,
				"missingEvidence": item.MissingEvidence,
			}); err != nil {
				return err
			}
		}
	}
	return r.appendLedgerSnapshotEvent(ctx, input, contract)
}

func (r *Runner) appendLedgerSnapshotEvent(ctx context.Context, input RunTurnInput, contract taskcontract.Contract) error {
	snapshot, err := hypothesisledger.NewStore(r.db).Snapshot(ctx, input.TaskID, contract.Goal, evidenceSnapshotLimit)
	if err != nil {
		return err
	}
	return r.appendJSONEvent(ctx, input, "agent.ledger.snapshot", map[string]interface{}{
		"hypotheses": snapshot.Hypotheses,
		"evidence":   snapshot.Evidence,
	})
}

func (r *Runner) updateHypothesesForTurnOutcome(ctx context.Context, input RunTurnInput, contract taskcontract.Contract, status string, missingEvidence []string) error {
	from := []string{hypothesisledger.StatusOpen, hypothesisledger.StatusSupported, hypothesisledger.StatusContradicted}
	items, err := hypothesisledger.NewStore(r.db).UpdateGoalStatus(ctx, input.TaskID, contract.Goal, from, status, missingEvidence)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Status != status {
			continue
		}
		if err := r.appendJSONEvent(ctx, input, "agent.hypothesis.updated", map[string]interface{}{
			"id":              item.ID,
			"claim":           item.Claim,
			"status":          item.Status,
			"missingEvidence": item.MissingEvidence,
		}); err != nil {
			return err
		}
	}
	return r.appendLedgerSnapshotEvent(ctx, input, contract)
}

type hypothesisSeed struct {
	claim           string
	missingEvidence []string
}

func seedHypotheses(contract taskcontract.Contract) []hypothesisSeed {
	seeds := []hypothesisSeed{{claim: contract.Goal, missingEvidence: contract.DoneWhen}}
	lower := strings.ToLower(contract.Goal)
	if contract.TaskType == taskcontract.TypeDiagnostic && strings.Contains(lower, "401") {
		seeds = append(seeds,
			hypothesisSeed{
				claim:           "401 由用户或账号不存在导致",
				missingEvidence: []string{"可复现问题的用户名或测试账号", "用户查询或认证日志"},
			},
			hypothesisSeed{
				claim:           "401 由密码或凭据不匹配导致",
				missingEvidence: []string{"对应的测试凭据", "失败请求的完整响应体"},
			},
		)
	}
	return seeds
}

func (r *Runner) hypothesisContext(ctx context.Context, taskID string, contract taskcontract.Contract) (string, error) {
	snapshot, err := hypothesisledger.NewStore(r.db).Snapshot(ctx, taskID, contract.Goal, evidenceSnapshotLimit)
	if err != nil {
		return "", err
	}
	if len(snapshot.Hypotheses) == 0 {
		return "", nil
	}
	var out strings.Builder
	out.WriteString("假设与证据账本（Harness 持久化，不受上下文压缩影响）：\n")
	for _, item := range snapshot.Hypotheses {
		out.WriteString(fmt.Sprintf("- [%s] %s (%s)", item.ID, item.Claim, item.Status))
		if len(item.MissingEvidence) > 0 {
			out.WriteString("；待补证据：")
			out.WriteString(strings.Join(item.MissingEvidence, "、"))
		}
		out.WriteString("\n")
	}
	if len(snapshot.Evidence) > 0 {
		out.WriteString("最近证据：\n")
		for index := len(snapshot.Evidence) - 1; index >= 0; index-- {
			item := snapshot.Evidence[index]
			out.WriteString(fmt.Sprintf("- [%s] %s %s: %s\n", item.Outcome, item.Operation, item.Resource, item.Summary))
		}
	}
	out.WriteString("调用工具时优先填写 purpose 和对应 hypothesisId。每次调用必须能增加新证据、推翻旧判断或推进实现；如果资源已经有相同 content hash，直接复用最近证据，不要换 grep/sed/head/tail 参数重复读取相同资源。工具失败后必须按错误中的 suggestedTool、hint 或 recovery 建议切换路径；同一错误不要原样重试。")
	return strings.TrimSpace(out.String()), nil
}

func toolResourceIntent(call llm.ToolCall, ws workspace.Workspace) resourceIntent {
	var args map[string]interface{}
	_ = json.Unmarshal([]byte(call.Function.Arguments), &args)
	intent := resourceIntent{
		Purpose:      strings.TrimSpace(stringFromMap(args, "purpose")),
		HypothesisID: strings.TrimSpace(stringFromMap(args, "hypothesisId")),
		Kind:         "command",
		Operation:    "validate",
	}
	name := strings.TrimSpace(call.Function.Name)
	switch name {
	case tools.NameReadFile, tools.NameReadDocument:
		intent.Kind = "file"
		intent.Operation = "read"
		intent.Resource = normalizeResourcePath(stringFromMap(args, "path"), ws.RootPath)
	case tools.NameListDir:
		intent.Kind = "directory"
		intent.Operation = "read"
		intent.Resource = normalizeResourcePath(stringFromMap(args, "path"), ws.RootPath)
	case tools.NameWriteFile:
		intent.Kind = "file"
		intent.Operation = "mutate"
		intent.Resource = normalizeResourcePath(stringFromMap(args, "path"), ws.RootPath)
	case tools.NameRunCommand:
		command := strings.TrimSpace(stringFromMap(args, "command"))
		workingDir := normalizeResourcePath(stringFromMap(args, "workingDir"), ws.RootPath)
		if workingDir == "" {
			workingDir = ws.RootPath
		}
		intent.Operation, intent.Resource = commandResourceIntent(command, workingDir)
		if intent.Resource == "" {
			intent.Resource = normalizeCommandFamily(command)
		}
	}
	if intent.Resource == "" {
		intent.Resource = name
	}
	return intent
}

func commandResourceIntent(command string, workingDir string) (string, string) {
	tokens := shellTokenPattern.FindAllString(command, -1)
	if len(tokens) == 0 {
		return "validate", ""
	}
	operation := "validate"
	for _, token := range tokens {
		base := strings.ToLower(filepath.Base(strings.Trim(token, `"'`)))
		switch base {
		case "cat", "head", "tail", "sed":
			operation = "read"
		case "grep", "rg":
			operation = "search"
		}
	}
	for index := len(tokens) - 1; index >= 0; index-- {
		token := strings.Trim(tokens[index], `"'`)
		if token == "" || strings.HasPrefix(token, "-") || strings.Contains(token, "=") {
			continue
		}
		candidate := normalizeResourcePath(token, workingDir)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return operation, candidate
		}
	}
	return operation, ""
}

func normalizeResourcePath(path string, workingDir string) string {
	path = strings.TrimSpace(strings.Trim(path, `"'`))
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(workingDir, path)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(path)
}

func normalizeCommandFamily(command string) string {
	tokens := shellTokenPattern.FindAllString(strings.TrimSpace(command), -1)
	if len(tokens) == 0 {
		return "command"
	}
	for _, token := range tokens {
		base := strings.ToLower(filepath.Base(strings.Trim(token, `"'`)))
		if base == "rtk" || base == "sudo" || base == "env" || base == "cd" {
			continue
		}
		return "command:" + base
	}
	return "command"
}

func semanticContentHash(intent resourceIntent, output interface{}, executionErr error) string {
	if intent.Resource != "" && (intent.Operation == "read" || intent.Operation == "search") {
		if raw, err := os.ReadFile(intent.Resource); err == nil {
			return hashBytes(raw)
		}
		if entries, err := os.ReadDir(intent.Resource); err == nil {
			values := make([]string, 0, len(entries))
			for _, entry := range entries {
				info, infoErr := entry.Info()
				if infoErr == nil {
					values = append(values, fmt.Sprintf("%s:%t:%d", entry.Name(), entry.IsDir(), info.Size()))
				}
			}
			sort.Strings(values)
			return hashBytes([]byte(strings.Join(values, "\n")))
		}
	}
	if executionErr != nil {
		return hashBytes([]byte(normalizeLoopMessage(executionErr.Error())))
	}
	return toolOutputFingerprint(output)
}

func hashBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return fmt.Sprintf("%x", sum[:])
}

func (r *Runner) recordToolEvidence(ctx context.Context, input RunTurnInput, contract taskcontract.Contract, call llm.ToolCall, intent resourceIntent, output interface{}, executionErr error) (toolLoopObservation, error) {
	store := hypothesisledger.NewStore(r.db)
	hypothesis, err := r.resolveToolHypothesis(ctx, input, contract, intent)
	if err != nil {
		return toolLoopObservation{}, err
	}
	if values, ok := output.(map[string]interface{}); ok && stringFromMap(values, "correctedTo") == tools.NameListDir {
		intent.Kind = "directory"
		intent.Operation = "read"
	}
	contentHash := semanticContentHash(intent, output, executionErr)
	previousCount, err := store.CountRecentMatchingEvidence(ctx, hypothesis.ID, input.TurnID, intent.Resource, contentHash)
	if err != nil {
		return toolLoopObservation{}, err
	}
	outcome := evidenceOutcome(call.Function.Name, intent, output, executionErr)
	summary := summarizeEvidence(call.Function.Name, intent, output, executionErr)
	evidence := hypothesisledger.Evidence{
		TaskID:       input.TaskID,
		TurnID:       input.TurnID,
		HypothesisID: hypothesis.ID,
		Kind:         intent.Kind,
		Operation:    intent.Operation,
		Source:       call.Function.Name,
		Resource:     intent.Resource,
		ContentHash:  contentHash,
		Summary:      summary,
		Outcome:      outcome,
	}
	recorded, err := store.AddEvidence(ctx, evidence)
	if err != nil {
		return toolLoopObservation{}, err
	}
	status := hypothesis.Status
	if outcome == hypothesisledger.OutcomeSupports {
		status = hypothesisledger.StatusSupported
	} else if outcome == hypothesisledger.OutcomeContradicts {
		status = hypothesisledger.StatusContradicted
	}
	if status != hypothesis.Status {
		hypothesis, err = store.UpdateStatus(ctx, hypothesis.ID, status, hypothesis.MissingEvidence)
		if err != nil {
			return toolLoopObservation{}, err
		}
		if err := r.appendJSONEvent(ctx, input, "agent.hypothesis.updated", map[string]interface{}{
			"id":              hypothesis.ID,
			"claim":           hypothesis.Claim,
			"status":          hypothesis.Status,
			"missingEvidence": hypothesis.MissingEvidence,
		}); err != nil {
			return toolLoopObservation{}, err
		}
	}
	newInformation := previousCount == 0
	evidenceEvent, err := r.appendJSONEventResult(ctx, input, "agent.evidence.recorded", map[string]interface{}{
		"id":             recorded.ID,
		"hypothesisId":   recorded.HypothesisID,
		"kind":           recorded.Kind,
		"operation":      recorded.Operation,
		"source":         recorded.Source,
		"resource":       recorded.Resource,
		"contentHash":    recorded.ContentHash,
		"summary":        recorded.Summary,
		"outcome":        recorded.Outcome,
		"purpose":        intent.Purpose,
		"newInformation": newInformation,
	})
	if err != nil {
		return toolLoopObservation{}, err
	}
	if err := store.UpdateEvidenceSequence(ctx, recorded.ID, evidenceEvent.Sequence); err != nil {
		return toolLoopObservation{}, err
	}
	if err := r.appendJSONEvent(ctx, input, "agent.progress.assessed", map[string]interface{}{
		"hypothesisId":   hypothesis.ID,
		"resource":       intent.Resource,
		"operation":      intent.Operation,
		"newInformation": newInformation,
		"repeatCount":    previousCount + 1,
		"action":         progressAction(previousCount + 1),
	}); err != nil {
		return toolLoopObservation{}, err
	}
	if err := r.appendLedgerSnapshotEvent(ctx, input, contract); err != nil {
		return toolLoopObservation{}, err
	}
	return toolLoopObservation{
		name:           call.Function.Name,
		signature:      toolCallSignature(call),
		args:           strings.TrimSpace(call.Function.Arguments),
		outcome:        loopOutcome(executionErr, contentHash),
		hypothesisID:   hypothesis.ID,
		resource:       intent.Resource,
		operation:      intent.Operation,
		newInformation: newInformation,
		repeatCount:    previousCount + 1,
		evidenceID:     recorded.ID,
	}, nil
}

func (r *Runner) resolveToolHypothesis(ctx context.Context, input RunTurnInput, contract taskcontract.Contract, intent resourceIntent) (hypothesisledger.Hypothesis, error) {
	store := hypothesisledger.NewStore(r.db)
	if intent.HypothesisID != "" {
		item, err := store.Get(ctx, intent.HypothesisID)
		if err == nil && item.TaskID == input.TaskID && item.ContractGoal == contract.Goal {
			return item, nil
		}
		if err != nil && !errors.Is(err, hypothesisledger.ErrNotFound) {
			return hypothesisledger.Hypothesis{}, err
		}
	}
	item, err := store.LatestOpen(ctx, input.TaskID, contract.Goal)
	if err == nil {
		return item, nil
	}
	if !errors.Is(err, hypothesisledger.ErrNotFound) {
		return hypothesisledger.Hypothesis{}, err
	}
	item, _, err = store.Ensure(ctx, input.TaskID, contract.Goal, contract.Goal, contract.DoneWhen)
	return item, err
}

func evidenceOutcome(toolName string, intent resourceIntent, output interface{}, executionErr error) string {
	if executionErr != nil {
		return hypothesisledger.OutcomeNeutral
	}
	if toolName == tools.NameWriteFile {
		return hypothesisledger.OutcomeSupports
	}
	if toolName == tools.NameRunCommand && looksLikeValidationCommand(commandFromOutput(output)) {
		if outputMap, ok := output.(map[string]interface{}); ok && stringFromMap(outputMap, "error") != "" {
			return hypothesisledger.OutcomeContradicts
		}
		return hypothesisledger.OutcomeSupports
	}
	return hypothesisledger.OutcomeNeutral
}

func commandFromOutput(output interface{}) string {
	if values, ok := output.(map[string]interface{}); ok {
		return stringFromMap(values, "command")
	}
	return ""
}

func summarizeEvidence(toolName string, intent resourceIntent, output interface{}, executionErr error) string {
	if executionErr != nil {
		return compactSummaryLine(executionErr.Error(), 220)
	}
	if toolName == tools.NameWriteFile {
		if values, ok := output.(map[string]interface{}); ok {
			return fmt.Sprintf("%s %s (+%d/-%d)", stringWithDefault(stringFromMap(values, "action"), "modified"), intent.Resource, intFromMap(values, "additions"), intFromMap(values, "deletions"))
		}
	}
	if toolName == tools.NameRunCommand {
		if values, ok := output.(map[string]interface{}); ok {
			return compactSummaryLine(strings.TrimSpace(stringFromMap(values, "output")), 220)
		}
	}
	return fmt.Sprintf("已通过 %s 检查 %s", toolName, intent.Resource)
}

func loopOutcome(executionErr error, contentHash string) string {
	if executionErr != nil {
		return "error:" + normalizeLoopMessage(executionErr.Error())
	}
	return "ok:" + contentHash
}

func progressAction(repeatCount int) string {
	switch {
	case repeatCount >= 3:
		return "stop_no_progress"
	case repeatCount == 2:
		return "replan"
	default:
		return "continue"
	}
}

func semanticNoProgress(observations []toolLoopObservation) (string, toolLoopInterrupt) {
	// Local models may mix a stale read with useful new work in one batch.
	// Preserve that progress and intervene only when the entire batch is stale.
	for _, item := range observations {
		if item.newInformation && !strings.HasPrefix(item.outcome, "error:") {
			return "", toolLoopInterrupt{}
		}
	}
	var replanCandidate *toolLoopObservation
	for _, item := range observations {
		if item.newInformation || item.resource == "" || strings.HasPrefix(item.outcome, "error:") {
			continue
		}
		if item.repeatCount >= 3 {
			return "stop", toolLoopInterrupt{
				reason:  "semantic_no_progress",
				message: fmt.Sprintf("围绕同一假设重复检查资源 %s，内容未变化且没有新增证据。", item.resource),
			}
		}
		if item.repeatCount == 2 {
			candidate := item
			replanCandidate = &candidate
		}
	}
	if replanCandidate != nil {
		return "replan", toolLoopInterrupt{
			reason:  "semantic_replan_required",
			message: fmt.Sprintf("资源 %s 已有相同证据，必须更换假设、资源或验证方式。", replanCandidate.resource),
		}
	}
	return "", toolLoopInterrupt{}
}

func evidenceReplanInstruction(reason toolLoopInterrupt) string {
	return "Harness 检测到无信息增益：" + reason.message +
		" 请基于假设与证据账本重新规划。下一步必须验证不同假设、读取不同关键资源、实施修改或明确列出缺失的用户输入；不要再次读取同一资源，也不要只改变命令参数或工具别名。"
}
