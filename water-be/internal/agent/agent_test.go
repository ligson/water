package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ligson/water/water-be/internal/event"
	"github.com/ligson/water/water-be/internal/llm"
	"github.com/ligson/water/water-be/internal/store"
	"github.com/ligson/water/water-be/internal/task"
	"github.com/ligson/water/water-be/internal/taskcontract"
	"github.com/ligson/water/water-be/internal/taskplan"
	"github.com/ligson/water/water-be/internal/tools"
	"github.com/ligson/water/water-be/internal/workspace"
)

func TestStringifyToolOutputForLLMLimitsLongCommandOutput(t *testing.T) {
	content := stringifyToolOutputForLLM(map[string]interface{}{
		"command": "top -l 1 -s 0",
		"output":  strings.Repeat("x", 7000),
	})

	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(content), &decoded); err != nil {
		t.Fatalf("decode limited output: %v", err)
	}
	if len(decoded["output"].(string)) != 6000 {
		t.Fatalf("expected output to be limited to 6000 chars, got %d", len(decoded["output"].(string)))
	}
	if decoded["outputTruncatedForLLM"] != true {
		t.Fatalf("expected truncation marker")
	}
}

func TestEstimateChatRequestTokensIncludesToolsAndChangesWithMessages(t *testing.T) {
	base := []llm.Message{{Role: llm.RoleSystem, Content: "系统提示"}, {Role: llm.RoleUser, Content: "检查登录"}}
	withLongerQuestion := []llm.Message{{Role: llm.RoleSystem, Content: "系统提示"}, {Role: llm.RoleUser, Content: strings.Repeat("检查登录接口和用户信息", 8)}}
	withoutTools := estimateChatRequestTokens(base, nil)
	withTools := estimateChatRequestTokens(base, []llm.Tool{{
		Type:     "function",
		Function: llm.ToolFunction{Name: "read_file", Description: "读取文件", Parameters: []byte(`{"type":"object"}`)},
	}})
	if withTools <= withoutTools {
		t.Fatalf("expected tool definitions to count toward context estimate: without=%d with=%d", withoutTools, withTools)
	}
	if estimateChatRequestTokens(withLongerQuestion, nil) <= withoutTools {
		t.Fatalf("expected a longer user question to change the estimate: base=%d longer=%d", withoutTools, estimateChatRequestTokens(withLongerQuestion, nil))
	}
}

func TestRenderSkillContextListsOnlyEnabledSkillMetadata(t *testing.T) {
	db, _ := openAgentTestDB(t)
	defer db.Close()
	now := "2026-08-15T00:00:00Z"
	_, err := db.Exec(`INSERT INTO skills (id, name, version, description, keywords_json, instructions, source, source_url, package_path, sha256, enabled, installed_at, updated_at) VALUES
		('enabled-skill', '启用 Skill', '1.0.0', '', '[]', 'ENABLED_SKILL_INSTRUCTION', 'upload', '', '/tmp/enabled.zip', 'abc', 1, ?, ?),
		('disabled-skill', '停用 Skill', '1.0.0', '', '[]', 'DISABLED_SKILL_INSTRUCTION', 'upload', '', '/tmp/disabled.zip', 'def', 0, ?, ?)`, now, now, now, now)
	if err != nil {
		t.Fatalf("insert skills: %v", err)
	}

	content, err := NewRunner(db, nil).renderSkillContext(context.Background())
	if err != nil {
		t.Fatalf("render skill context: %v", err)
	}
	if !strings.Contains(content, "enabled-skill") || strings.Contains(content, "disabled-skill") {
		t.Fatalf("unexpected skill context: %s", content)
	}
	if strings.Contains(content, "ENABLED_SKILL_INSTRUCTION") {
		t.Fatalf("skill instructions must be loaded progressively: %s", content)
	}
	if !strings.Contains(content, "read_skill") {
		t.Fatalf("expected read_skill guidance in catalog: %s", content)
	}
	if !strings.Contains(content, "不能绕过工作区边界") {
		t.Fatalf("expected Harness boundary in skill context: %s", content)
	}
}

func TestBuildUserMessageIncludesImageAndAttachmentPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "screen.png")
	if err := os.WriteFile(path, []byte("png-bytes"), 0o644); err != nil {
		t.Fatalf("write attachment: %v", err)
	}
	message := buildUserMessage(RunTurnInput{
		UserInput: "分析截图",
		Attachments: []task.Attachment{{
			ID:       "att_test",
			Name:     "screen.png",
			MIMEType: "image/png",
			Kind:     "image",
			Path:     path,
			Size:     9,
		}},
	})
	if !strings.Contains(message.Content, path) {
		t.Fatalf("expected attachment path in user message, got %s", message.Content)
	}
	if len(message.ContentParts) != 2 || message.ContentParts[1].ImageURL == nil {
		t.Fatalf("expected text and image content parts, got %#v", message.ContentParts)
	}
	if !strings.HasPrefix(message.ContentParts[1].ImageURL.URL, "data:image/png;base64,") {
		t.Fatalf("unexpected image data url: %s", message.ContentParts[1].ImageURL.URL)
	}
}

func TestCachedToolOutputRetainsPriorEvidenceForReuse(t *testing.T) {
	content := cachedToolOutputForLLM(map[string]interface{}{
		"path":    "/workspace/large.log",
		"content": strings.Repeat("sensitive-context-noise", 100),
	}, "abc123")
	if !strings.Contains(content, "sensitive-context-noise") {
		t.Fatalf("expected cached tool result to retain prior evidence")
	}
	if !strings.Contains(content, "/workspace/large.log") || !strings.Contains(content, "abc123") {
		t.Fatalf("expected cached result to retain path and hash, got %s", content)
	}
}

func TestAssistantPromisedToolUse(t *testing.T) {
	if !assistantPromisedToolUse("让我先看看你的当前工作空间，看看有没有相关系统信息文件需要处理：") {
		t.Fatalf("expected workspace inspection promise to require tool use")
	}
	if assistantPromisedToolUse("vd 开头的设备通常是 virtio 虚拟磁盘设备。") {
		t.Fatalf("expected direct answer not to require tool use")
	}
}

func TestDocumentOutputRequestGetsDefaultPath(t *testing.T) {
	if !isDocumentOutputRequest("帮我总结电脑分析报告并生成文档") {
		t.Fatalf("expected report document request to be detected")
	}
	if isDocumentOutputRequest("查询一下 CPU 使用率") {
		t.Fatalf("expected plain system query to stay non-document")
	}

	path := defaultDocumentPath("/workspace/water", "电脑分析报告", 2)
	expected := filepath.Join("/workspace/water", "reports", "电脑分析报告-turn-2.md")
	if path != expected {
		t.Fatalf("expected default document path %q, got %q", expected, path)
	}
}

func TestToolCallKeyUsesStableIndex(t *testing.T) {
	first := toolCallKey(llm.ToolCall{ID: "call_read_file", Index: 2, Function: llm.ToolCallFunction{Name: "read_file"}})
	second := toolCallKey(llm.ToolCall{Index: 2, Function: llm.ToolCallFunction{Name: "read_file", Arguments: `{"path":"/tmp"}`}})
	third := toolCallKey(llm.ToolCall{Index: 7, Function: llm.ToolCallFunction{Name: "list_dir"}})

	if first != second {
		t.Fatalf("expected same key for same index fragments, got %q and %q", first, second)
	}
	if first == third {
		t.Fatalf("expected different keys for different indices")
	}
}

func TestToolLoopGuardDetectsInterleavedRepeatedFailures(t *testing.T) {
	guard := &toolLoopGuard{}
	observations := []toolLoopObservation{
		{name: "list_dir", signature: "list_dir|{}", args: "{}", outcome: "error:path access denied"},
		{name: "", signature: `|{"path":"/workspace"}`, args: `{"path":"/workspace"}`, outcome: `error:unsupported tool ""`},
	}

	if interrupted, _ := guard.observe(observations); interrupted {
		t.Fatalf("expected first failures to be reported back to the model")
	}
	interrupted, reason := guard.observe(observations)
	if !interrupted {
		t.Fatalf("expected interleaved repeated failures to interrupt")
	}
	if reason.reason != "tool_repeated_failure" {
		t.Fatalf("expected repeated failure reason, got %q", reason.reason)
	}
}

func TestCommandResourceIntentNormalizesDifferentReadersToSameFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "WebSecurityConfig.java")
	if err := os.WriteFile(path, []byte("class WebSecurityConfig {}\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	commands := []string{
		"grep -n Security WebSecurityConfig.java",
		"sed -n '1,20p' WebSecurityConfig.java",
		"head -20 WebSecurityConfig.java",
		"tail -20 WebSecurityConfig.java",
	}
	for _, command := range commands {
		_, resource := commandResourceIntent(command, root)
		if resource != path {
			t.Fatalf("command %q: expected resource %q, got %q", command, path, resource)
		}
	}
}

func TestSemanticNoProgressReplansThenStops(t *testing.T) {
	base := toolLoopObservation{
		name:           "read_file",
		resource:       "/workspace/WebSecurityConfig.java",
		operation:      "read",
		outcome:        "ok:unchanged",
		newInformation: false,
	}
	base.repeatCount = 2
	action, reason := semanticNoProgress([]toolLoopObservation{base})
	if action != "replan" || reason.reason != "semantic_replan_required" {
		t.Fatalf("expected second observation to request replan, got %q %#v", action, reason)
	}

	base.repeatCount = 3
	action, reason = semanticNoProgress([]toolLoopObservation{base})
	if action != "stop" || reason.reason != "semantic_no_progress" {
		t.Fatalf("expected third observation to stop, got %q %#v", action, reason)
	}
}

func TestSemanticNoProgressKeepsMixedBatchWithNewEvidence(t *testing.T) {
	action, _ := semanticNoProgress([]toolLoopObservation{
		{
			name:           "read_file",
			resource:       "/workspace/repeated.java",
			outcome:        "ok:unchanged",
			newInformation: false,
			repeatCount:    4,
		},
		{
			name:           "read_file",
			resource:       "/workspace/new.java",
			outcome:        "ok:new",
			newInformation: true,
			repeatCount:    1,
		},
	})
	if action != "" {
		t.Fatalf("expected useful evidence to keep the batch progressing, got %q", action)
	}
}

func TestSeedHypothesesForLogin401(t *testing.T) {
	seeds := seedHypotheses(taskcontract.Contract{
		Goal:     "排查登录接口 401",
		TaskType: taskcontract.TypeDiagnostic,
		DoneWhen: []string{"定位原因"},
	})
	if len(seeds) != 3 {
		t.Fatalf("expected goal plus two login hypotheses, got %#v", seeds)
	}
	joined := seeds[1].claim + "\n" + seeds[2].claim
	for _, expected := range []string{"用户或账号不存在", "密码或凭据不匹配"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("expected hypotheses to contain %q, got %q", expected, joined)
		}
	}
}

func TestCompactToolLoopMessagesPreservesLatestCompleteToolRound(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: strings.Repeat("system ", 40)},
		{Role: llm.RoleUser, Content: "排查登录 401"},
	}
	for index := range 8 {
		callID := fmt.Sprintf("call-%d", index)
		messages = append(messages,
			llm.Message{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{{
					ID:   callID,
					Type: "function",
					Function: llm.ToolCallFunction{
						Name:      "read_file",
						Arguments: fmt.Sprintf(`{"path":"/workspace/log-%d.txt"}`, index),
					},
				}},
			},
			llm.Message{
				Role:       llm.RoleTool,
				Name:       "read_file",
				ToolCallID: callID,
				Content:    fmt.Sprintf(`{"content":"%s-%d"}`, strings.Repeat("log output ", 140), index),
			},
		)
	}

	compacted, stats := compactToolLoopMessages(messages, 4096)
	if stats.DroppedMessages == 0 {
		t.Fatalf("expected old tool messages to be compacted")
	}
	if stats.CompactedEstimatedTokens >= stats.OriginalEstimatedTokens {
		t.Fatalf("expected compaction to reduce estimated tokens: %#v", stats)
	}
	if compacted[0].Content != messages[0].Content || compacted[1].Content != messages[1].Content {
		t.Fatalf("expected system instruction and user input to be preserved")
	}
	if compacted[2].Role != llm.RoleSystem || !strings.Contains(compacted[2].Content, "已经执行过的检查") {
		t.Fatalf("expected deterministic history summary, got %#v", compacted[2])
	}
	latest := compacted[len(compacted)-1]
	if latest.Role != llm.RoleTool || latest.ToolCallID != "call-7" || !strings.Contains(latest.Content, "-7") {
		t.Fatalf("expected latest tool result to be preserved, got %#v", latest)
	}
	previous := compacted[len(compacted)-2]
	if previous.Role != llm.RoleAssistant || len(previous.ToolCalls) != 1 || previous.ToolCalls[0].ID != latest.ToolCallID {
		t.Fatalf("expected latest assistant tool call to stay paired with its result")
	}
}

func TestToolLoopInstructionsConvergeAndFinalize(t *testing.T) {
	if got := toolLoopConvergenceInstruction(11, 24); got != "" {
		t.Fatalf("expected no convergence instruction before half the rounds, got %q", got)
	}
	if got := toolLoopConvergenceInstruction(12, 24); !strings.Contains(got, "必须收敛") {
		t.Fatalf("expected convergence instruction at half the rounds, got %q", got)
	}
	if got := toolLoopFinalInstruction(24); !strings.Contains(got, "工具已关闭") || !strings.Contains(got, "最终答复") {
		t.Fatalf("expected forced final instruction, got %q", got)
	}
}

func TestInferMissingInputsForLoginEvidence(t *testing.T) {
	missing := inferMissingInputs("无法确定 401 原因，请提供测试用户名、密码和完整响应体。", completionEvidence{})
	joined := strings.Join(missing, "\n")
	for _, expected := range []string{"用户名", "测试凭据", "响应体"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("expected missing inputs to contain %q, got %#v", expected, missing)
		}
	}
}

func TestInferMissingInputsDoesNotRequestKnownWorkspaceRoot(t *testing.T) {
	missing := inferMissingInputs(
		"请提供工作区内的正确路径，当前工作区路径尚未确认。",
		completionEvidence{pathAccessDenied: true},
	)
	if len(missing) != 0 {
		t.Fatalf("expected Harness-provided workspace root not to be requested, got %#v", missing)
	}
}

func TestInferMissingInputsAllowsExplicitExternalPathAuthorization(t *testing.T) {
	missing := inferMissingInputs(
		"请授权需要读取的工作区外路径 /srv/private-config。",
		completionEvidence{pathAccessDenied: true},
	)
	if len(missing) != 1 || !strings.Contains(missing[0], "工作区外路径") {
		t.Fatalf("expected explicit external path authorization to remain blocking, got %#v", missing)
	}
}

func TestLegacyGoalScopeScorePrefersOriginalFeatureGoal(t *testing.T) {
	original := "帮我用 Spring Boot 写用户登录、注册和用户管理 CRUD，后端和 Vue 前端分目录"
	recent := "登录为什么一直返回 401"
	if legacyGoalScopeScore(original) <= legacyGoalScopeScore(recent) {
		t.Fatalf("expected broad original goal to outrank recent symptom: original=%d recent=%d",
			legacyGoalScopeScore(original), legacyGoalScopeScore(recent))
	}
}

func TestContractGoalRecoversBroadLegacyGoal(t *testing.T) {
	db, taskID := openAgentTestDB(t)
	ctx := context.Background()
	events := event.NewStore(db)
	inputs := []string{
		"帮我用 Spring Boot 写一个用户登录、注册、用户管理 CRUD，后端和 Vue 前端分目录",
		"注册接口为什么 403",
		"登录为什么一直返回 401",
		"继续任务",
	}
	for index, userInput := range inputs {
		_, err := events.Append(ctx, event.AppendInput{
			RequestID: fmt.Sprintf("request-%d", index), TaskID: taskID,
			Type: "turn.started", PayloadJSON: mustTestJSON(t, map[string]interface{}{"userInput": userInput}),
		})
		if err != nil {
			t.Fatalf("append legacy event: %v", err)
		}
	}

	goal, err := NewRunner(db, nil).contractGoal(ctx, RunTurnInput{TaskID: taskID, UserInput: "继续任务"})
	if err != nil {
		t.Fatalf("recover legacy goal: %v", err)
	}
	if goal != inputs[0] {
		t.Fatalf("expected original broad goal, got %q", goal)
	}
}

func TestCompletionArbiterRequiresFeaturePlanAcceptance(t *testing.T) {
	db, taskID := openAgentTestDB(t)
	ctx := context.Background()
	goal := "实现登录、注册和用户信息 CRUD"
	contract := taskcontract.Build(taskID, goal)
	contract.TaskType = taskcontract.TypeCodeChange
	if _, err := taskcontract.NewStore(db).Upsert(ctx, contract); err != nil {
		t.Fatalf("upsert contract: %v", err)
	}
	if _, _, err := taskplan.NewStore(db).Ensure(ctx, taskID, goal, contract.TaskType); err != nil {
		t.Fatalf("ensure task plan: %v", err)
	}

	decision, err := NewRunner(db, nil).arbitrateCompletion(ctx, RunTurnInput{TaskID: taskID}, contract, "功能已经完成。")
	if err != nil {
		t.Fatalf("arbitrate completion: %v", err)
	}
	if decision.Outcome != completionContinue || decision.Reason != "plan_acceptance_not_completed" {
		t.Fatalf("expected incomplete feature plan to force continuation, got %#v", decision)
	}
}

func TestToolOutputRecoverySuggestsAlternativeForFailedCommand(t *testing.T) {
	kind, suggestion, ok := toolOutputRecovery(map[string]interface{}{
		"command":   "free -h",
		"success":   false,
		"errorKind": "process_exit",
		"hint":      "当前系统是 macOS(darwin)，请尝试 vm_stat。",
	})
	if !ok || kind != "command_recovery" || !strings.Contains(suggestion, "vm_stat") || strings.Contains(suggestion, "原样重试") == false {
		t.Fatalf("expected alternative command recovery suggestion, got kind=%q suggestion=%q ok=%v", kind, suggestion, ok)
	}
}

func TestToolResultSucceededRejectsExplicitFalseSuccess(t *testing.T) {
	if toolResultSucceeded(map[string]interface{}{"success": false}) {
		t.Fatal("expected explicit success=false to be rejected")
	}
	if !toolResultSucceeded(map[string]interface{}{"success": true}) {
		t.Fatal("expected explicit success=true to be accepted")
	}
}

func TestCompletionArbiterAcceptsCompletedFeaturePlanBeforeTextHeuristics(t *testing.T) {
	db, taskID := openAgentTestDB(t)
	ctx := context.Background()
	goal := "实现登录、注册和用户信息 CRUD"
	contract := taskcontract.Build(taskID, goal)
	contract.TaskType = taskcontract.TypeCodeChange
	if _, err := taskcontract.NewStore(db).Upsert(ctx, contract); err != nil {
		t.Fatalf("upsert contract: %v", err)
	}
	plan, _, err := taskplan.NewStore(db).Ensure(ctx, taskID, goal, contract.TaskType)
	if err != nil {
		t.Fatalf("ensure task plan: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE task_plan_steps SET status = ? WHERE plan_id = ?`, taskplan.StatusCompleted, plan.ID); err != nil {
		t.Fatalf("complete plan steps: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE task_plans SET status = ? WHERE id = ?`, taskplan.StatusCompleted, plan.ID); err != nil {
		t.Fatalf("complete plan: %v", err)
	}

	events := event.NewStore(db)
	if _, err := events.Append(ctx, event.AppendInput{
		RequestID: "request-denied", TaskID: taskID, Type: "tool.failed",
		PayloadJSON: mustTestJSON(t, map[string]interface{}{"message": "path access denied"}),
	}); err != nil {
		t.Fatalf("append denied event: %v", err)
	}
	if _, err := events.Append(ctx, event.AppendInput{
		RequestID: "request-e2e", TaskID: taskID, Type: "tool.completed",
		PayloadJSON: mustTestJSON(t, map[string]interface{}{
			"name": tools.NameRunCommand,
			"output": map[string]interface{}{
				"command": "mvn -q -Dtest=UserManagementIntegrationTest test",
				"output":  "verify:register REGISTER_OK HTTP_STATUS:201\nverify:login LOGIN_OK JWT_OK HTTP_STATUS:200\nverify:user_crud USER_CRUD_OK\nverify:e2e WATER_E2E_OK",
			},
		}),
	}); err != nil {
		t.Fatalf("append e2e event: %v", err)
	}

	content := "功能已完成。项目路径、测试账号和验证日志均已核对；请提供这些信息的说法不适用于本次结果。"
	decision, err := NewRunner(db, nil).arbitrateCompletion(ctx, RunTurnInput{TaskID: taskID}, contract, content)
	if err != nil {
		t.Fatalf("arbitrate completion: %v", err)
	}
	if decision.Outcome != completionCompleted || decision.Reason != "plan_acceptance_completed" {
		t.Fatalf("expected completed plan and E2E evidence to outrank text heuristics, got %#v", decision)
	}
}

func TestCompletionEvidenceSuccessfulValidationClearsHistoricalPathDenial(t *testing.T) {
	db, taskID := openAgentTestDB(t)
	ctx := context.Background()
	contract := taskcontract.Build(taskID, "修复后端测试")
	contract.TaskType = taskcontract.TypeCodeChange
	if _, err := taskcontract.NewStore(db).Upsert(ctx, contract); err != nil {
		t.Fatalf("upsert contract: %v", err)
	}
	events := event.NewStore(db)
	fixtures := []struct {
		eventType string
		payload   map[string]interface{}
	}{
		{eventType: "tool.failed", payload: map[string]interface{}{"message": "path access denied"}},
		{eventType: "tool.completed", payload: map[string]interface{}{"name": tools.NameWriteFile, "output": map[string]interface{}{"path": "/workspace/result.go"}}},
		{eventType: "tool.completed", payload: map[string]interface{}{"name": tools.NameRunCommand, "output": map[string]interface{}{"command": "go test ./...", "output": "ok"}}},
	}
	for index, fixture := range fixtures {
		if _, err := events.Append(ctx, event.AppendInput{
			RequestID: fmt.Sprintf("request-%d", index), TaskID: taskID, Type: fixture.eventType,
			PayloadJSON: mustTestJSON(t, fixture.payload),
		}); err != nil {
			t.Fatalf("append completion evidence: %v", err)
		}
	}

	evidence, err := NewRunner(db, nil).collectCompletionEvidence(ctx, RunTurnInput{TaskID: taskID}, contract)
	if err != nil {
		t.Fatalf("collect completion evidence: %v", err)
	}
	if evidence.pathAccessDenied {
		t.Fatalf("expected later successful validation to clear historical path denial: %#v", evidence)
	}
}

func TestEnsureTaskContractReconcilesFalseBlockedStateFromCompletedPlan(t *testing.T) {
	db, taskID := openAgentTestDB(t)
	ctx := context.Background()
	goal := "实现登录、注册和用户信息 CRUD"
	contract := taskcontract.Build(taskID, goal)
	contract.Stage = taskcontract.StageBlocked
	contract.MissingInputs = []string{"授权目标路径，或提供工作区内的正确路径"}
	if _, err := taskcontract.NewStore(db).Upsert(ctx, contract); err != nil {
		t.Fatalf("upsert blocked contract: %v", err)
	}
	plan, _, err := taskplan.NewStore(db).Ensure(ctx, taskID, goal, contract.TaskType)
	if err != nil {
		t.Fatalf("ensure task plan: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE task_plan_steps SET status = ? WHERE plan_id = ?`, taskplan.StatusCompleted, plan.ID); err != nil {
		t.Fatalf("complete plan steps: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE task_plans SET status = ? WHERE id = ?`, taskplan.StatusCompleted, plan.ID); err != nil {
		t.Fatalf("complete plan: %v", err)
	}
	turn, err := task.NewStore(db).CreateTurn(ctx, task.CreateTurnInput{TaskID: taskID, UserInput: "继续任务"})
	if err != nil {
		t.Fatalf("create continuation turn: %v", err)
	}

	runner := NewRunner(db, event.NewStore(db).Append)
	updated, err := runner.ensureTaskContract(ctx, RunTurnInput{
		TaskID: taskID, TurnID: turn.ID, RequestID: "request-test", UserInput: "继续任务",
	})
	if err != nil {
		t.Fatalf("reconcile blocked contract: %v", err)
	}
	if updated.Stage != taskcontract.StageCompleted || len(updated.MissingInputs) != 0 {
		t.Fatalf("expected completed plan to repair false blocked state, got %#v", updated)
	}
}

func TestRecoverTaskPlanFromHistoryStopsAtFirstUnprovenGate(t *testing.T) {
	db, taskID := openAgentTestDB(t)
	ctx := context.Background()
	goal := "实现登录、注册和用户信息 CRUD"
	plan, _, err := taskplan.NewStore(db).Ensure(ctx, taskID, goal, taskcontract.TypeCodeChange)
	if err != nil {
		t.Fatalf("ensure task plan: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE task_plan_steps SET status = ? WHERE plan_id = ?`, taskplan.StatusPending, plan.ID); err != nil {
		t.Fatalf("reset steps: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE task_plan_steps SET status = ? WHERE plan_id = ? AND position = 2`, taskplan.StatusInProgress, plan.ID); err != nil {
		t.Fatalf("activate register step: %v", err)
	}
	plan, err = taskplan.NewStore(db).Get(ctx, taskID, goal)
	if err != nil {
		t.Fatalf("reload plan: %v", err)
	}
	events := event.NewStore(db)
	for index, output := range []map[string]interface{}{
		{"command": "curl -X POST http://localhost:8889/api/users/register", "output": "{\"id\":67}\n201"},
		{"command": "curl -X POST http://localhost:8889/api/users/login", "output": "{\"token\":\"jwt\"}\n200"},
		{"command": "npm run build", "output": "built successfully"},
		{"command": "curl http://localhost:8889/api/users", "output": "HTTP 403"},
	} {
		if _, err := events.Append(ctx, event.AppendInput{
			RequestID: fmt.Sprintf("request-%d", index), TaskID: taskID, Type: "tool.completed",
			PayloadJSON: mustTestJSON(t, map[string]interface{}{"name": "run_command", "output": output}),
		}); err != nil {
			t.Fatalf("append history event: %v", err)
		}
	}

	recovered, count, err := NewRunner(db, nil).recoverTaskPlanFromHistory(ctx, taskID, goal, plan)
	if err != nil {
		t.Fatalf("recover plan: %v", err)
	}
	if count != 2 || recovered.Steps[1].Status != taskplan.StatusCompleted || recovered.Steps[2].Status != taskplan.StatusCompleted {
		t.Fatalf("expected registration and login recovery, count=%d plan=%#v", count, recovered)
	}
	if recovered.Steps[3].Status != taskplan.StatusInProgress || recovered.Steps[4].Status != taskplan.StatusPending {
		t.Fatalf("expected recovery to stop at unproven CRUD gate, got %#v", recovered)
	}
}

func openAgentTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "agent-test.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(db); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	ws, err := workspace.NewStore(db).Create(context.Background(), workspace.CreateInput{Name: "test", RootPath: t.TempDir()})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	item, err := task.NewStore(db).Create(context.Background(), task.CreateInput{WorkspaceID: ws.ID, Title: "test"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return db, item.ID
}

func TestContractStageForToolCalls(t *testing.T) {
	if stage := contractStageForToolCalls([]llm.ToolCall{{Function: llm.ToolCallFunction{Name: "write_file"}}}); stage != "implementing" {
		t.Fatalf("expected write_file to enter implementing, got %q", stage)
	}
	if stage := contractStageForToolCalls([]llm.ToolCall{{Function: llm.ToolCallFunction{Name: "run_command", Arguments: `{"command":"go test ./..."}`}}}); stage != "verifying" {
		t.Fatalf("expected test command to enter verifying, got %q", stage)
	}
}

func TestEstimateTextTokensAccountsForASCIIAndChinese(t *testing.T) {
	if got := estimateTextTokens(strings.Repeat("a", 400)); got != 100 {
		t.Fatalf("expected 400 ASCII characters to estimate as 100 tokens, got %d", got)
	}
	if got := estimateTextTokens("若水编程"); got != 4 {
		t.Fatalf("expected four Chinese characters to estimate as four tokens, got %d", got)
	}
}

func TestBuildTaskRollingSummaryFromEvents(t *testing.T) {
	ws := workspace.Workspace{RootPath: "/workspace/water"}
	summaryEvent := turnSummaryPayload{
		ChangedFiles: []turnSummaryFile{
			{
				Path:      "/workspace/water/water-fe/src/App.vue",
				Action:    "modified",
				Additions: 12,
				Deletions: 3,
			},
		},
		Validations: []turnSummaryCommand{
			{
				Command: "npm run build",
				Status:  "passed",
				Summary: "✓ built in 200ms",
			},
		},
	}
	events := []event.Event{
		{
			Type:        "turn.started",
			PayloadJSON: mustTestJSON(t, map[string]interface{}{"userInput": "把前端设置页整理得更清楚"}),
		},
		{
			Type:        "turn.summary",
			PayloadJSON: mustTestJSON(t, summaryEvent),
		},
	}

	summary := buildTaskRollingSummary(ws, events)
	for _, expected := range []string{
		"最近用户目标",
		"把前端设置页整理得更清楚",
		"water-fe/src/App.vue",
		"modified, +12/-3",
		"npm run build: passed",
		"信息不足时先读相关文件或向用户确认",
	} {
		if !strings.Contains(summary, expected) {
			t.Fatalf("expected summary to contain %q, got:\n%s", expected, summary)
		}
	}
}

func TestBuildTaskRollingSummaryDropsGenericContinuationsAndDuplicateValidations(t *testing.T) {
	ws := workspace.Workspace{RootPath: "/workspace/water"}
	validation := turnSummaryCommand{Command: "npm run build", Status: "passed", Summary: "built"}
	events := []event.Event{
		{Type: "turn.started", PayloadJSON: mustTestJSON(t, map[string]interface{}{"userInput": "登录返回 401"})},
		{Type: "turn.started", PayloadJSON: mustTestJSON(t, map[string]interface{}{"userInput": "继续任务"})},
		{Type: "turn.started", PayloadJSON: mustTestJSON(t, map[string]interface{}{"userInput": "继续上一轮任务，从最后一个未完成点接着做"})},
		{Type: "turn.summary", PayloadJSON: mustTestJSON(t, turnSummaryPayload{Validations: []turnSummaryCommand{validation}})},
		{Type: "turn.summary", PayloadJSON: mustTestJSON(t, turnSummaryPayload{Validations: []turnSummaryCommand{validation}})},
	}

	summary := buildTaskRollingSummary(ws, events)
	if strings.Contains(summary, "继续任务") || strings.Contains(summary, "继续上一轮任务") {
		t.Fatalf("expected generic continuation prompts to be omitted, got:\n%s", summary)
	}
	if count := strings.Count(summary, "npm run build"); count != 1 {
		t.Fatalf("expected duplicate validation to appear once, got %d:\n%s", count, summary)
	}
	if !strings.Contains(summary, "登录返回 401") {
		t.Fatalf("expected concrete user goal to remain, got:\n%s", summary)
	}
}

func mustTestJSON(t *testing.T, value interface{}) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return string(raw)
}
