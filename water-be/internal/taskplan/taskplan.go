package taskplan

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ligson/water/water-be/internal/dbutil"
	"github.com/ligson/water/water-be/internal/uid"
)

const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
	StatusBlocked    = "blocked"

	GateEvidence       = "evidence"
	GateRegister       = "register_acceptance"
	GateLogin          = "login_acceptance"
	GateUserCRUD       = "user_crud_acceptance"
	GateFrontendBuild  = "frontend_build"
	GateVerification   = "verification"
	GateEndToEnd       = "end_to_end"
	GateImplementation = "implementation"
)

var ErrNotFound = errors.New("task plan not found")

var httpSuccessPattern = regexp.MustCompile(`(?m)(?:^|\s)http(?:_status)?[: ]*2\d\d(?:\s|$)|(?:^|\n)2\d\d(?:\n|$)`)

type Plan struct {
	ID           string    `json:"id"`
	TaskID       string    `json:"taskId"`
	ContractGoal string    `json:"contractGoal"`
	Version      int       `json:"version"`
	Status       string    `json:"status"`
	Steps        []Step    `json:"steps"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type Step struct {
	ID                  string    `json:"id"`
	PlanID              string    `json:"planId"`
	Position            int       `json:"position"`
	Title               string    `json:"title"`
	Description         string    `json:"description"`
	Status              string    `json:"status"`
	GateType            string    `json:"gateType"`
	Acceptance          []string  `json:"acceptance"`
	CompletedEvidenceID string    `json:"completedEvidenceId,omitempty"`
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

type ToolObservation struct {
	ToolName   string
	Arguments  string
	Purpose    string
	OutputText string
	Succeeded  bool
	EvidenceID string
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func (s *Store) Ensure(ctx context.Context, taskID string, goal string, taskType string) (Plan, bool, error) {
	item, err := s.Get(ctx, taskID, goal)
	if err == nil {
		return item, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Plan{}, false, err
	}
	item = Build(taskID, goal, taskType)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Plan{}, false, fmt.Errorf("begin task plan transaction: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
INSERT INTO task_plans (id, task_id, contract_goal, version, status, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`, item.ID, item.TaskID, item.ContractGoal, item.Version, item.Status,
		dbutil.FormatTime(item.CreatedAt), dbutil.FormatTime(item.UpdatedAt))
	if err != nil {
		return Plan{}, false, fmt.Errorf("insert task plan: %w", err)
	}
	for _, step := range item.Steps {
		acceptanceJSON, marshalErr := json.Marshal(step.Acceptance)
		if marshalErr != nil {
			return Plan{}, false, fmt.Errorf("encode plan acceptance: %w", marshalErr)
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO task_plan_steps (id, plan_id, position, title, description, status, gate_type, acceptance_json, completed_evidence_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?)`, step.ID, step.PlanID, step.Position, step.Title,
			step.Description, step.Status, step.GateType, string(acceptanceJSON), step.CompletedEvidenceID,
			dbutil.FormatTime(step.CreatedAt), dbutil.FormatTime(step.UpdatedAt))
		if err != nil {
			return Plan{}, false, fmt.Errorf("insert task plan step: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Plan{}, false, fmt.Errorf("commit task plan: %w", err)
	}
	created, err := s.Get(ctx, taskID, goal)
	return created, true, err
}

func Build(taskID string, goal string, taskType string) Plan {
	now := time.Now()
	planID := uid.New("plan")
	definitions := genericDefinitions(taskType)
	if isUserManagementFeature(goal) {
		definitions = userManagementDefinitions()
	}
	steps := make([]Step, 0, len(definitions))
	for index, definition := range definitions {
		status := StatusPending
		if index == 0 {
			status = StatusInProgress
		}
		steps = append(steps, Step{
			ID:          uid.New("step"),
			PlanID:      planID,
			Position:    index + 1,
			Title:       definition.title,
			Description: definition.description,
			Status:      status,
			GateType:    definition.gate,
			Acceptance:  definition.acceptance,
			CreatedAt:   now,
			UpdatedAt:   now,
		})
	}
	return Plan{
		ID:           planID,
		TaskID:       taskID,
		ContractGoal: strings.TrimSpace(goal),
		Version:      1,
		Status:       StatusInProgress,
		Steps:        steps,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

type stepDefinition struct {
	title       string
	description string
	gate        string
	acceptance  []string
}

func userManagementDefinitions() []stepDefinition {
	return []stepDefinition{
		{
			title:       "确认现状与未完成项",
			description: "读取项目规则、认证配置、接口和现有测试，形成缺口结论后再修改。",
			gate:        GateEvidence,
			acceptance:  []string{"至少取得一条工作区代码或配置证据", "明确注册、登录、认证和 CRUD 的现状"},
		},
		{
			title:       "打通用户注册",
			description: "修复注册 API 与前端调用，并用唯一测试账号真实请求注册接口。",
			gate:        GateRegister,
			acceptance:  []string{"注册请求返回 2xx", "响应包含新用户标识", "失败状态能显示明确原因"},
		},
		{
			title:       "打通登录与 JWT 认证",
			description: "用刚注册的账号登录，取得 token，并确认服务端能解析 token 建立身份。",
			gate:        GateLogin,
			acceptance:  []string{"登录返回 2xx 和非空 token", "携带 token 可访问受保护接口"},
		},
		{
			title:       "验证用户信息 CRUD",
			description: "优先运行项目已有的用户管理集成测试；否则在认证状态下依次验证列表、详情、修改和删除，任何 HTTP 非 2xx 都视为失败。",
			gate:        GateUserCRUD,
			acceptance:  []string{"列表和详情查询通过", "修改后读回一致", "删除后不可再查询"},
		},
		{
			title:       "验证前端登录注册与用户管理",
			description: "完成前端生产构建，确认 token 注入、401 处理、表单和 CRUD 入口可用。",
			gate:        GateFrontendBuild,
			acceptance:  []string{"前端类型检查和生产构建通过", "登录注册及用户管理路由可达"},
		},
		{
			title:       "完成端到端回归",
			description: "前端构建通过后，运行明确覆盖注册、登录、鉴权和 CRUD 的现有集成测试或可重复验收脚本。",
			gate:        GateEndToEnd,
			acceptance:  []string{"验收命令退出码为 0", "输出 WATER_E2E_OK", "服务重启后测试数据策略符合配置"},
		},
	}
}

func genericDefinitions(taskType string) []stepDefinition {
	definitions := []stepDefinition{
		{title: "确认现状与约束", description: "读取直接相关资源并明确缺口。", gate: GateEvidence, acceptance: []string{"取得直接证据"}},
	}
	if taskType == "code_change" || taskType == "document" {
		definitions = append(definitions, stepDefinition{title: "实施必要修改", description: "只修改完成目标所需的文件。", gate: GateImplementation, acceptance: []string{"观察到目标文件写入"}})
	}
	definitions = append(definitions,
		stepDefinition{title: "运行对应验证", description: "运行测试、构建或可复现检查。", gate: GateVerification, acceptance: []string{"验证命令退出码为 0"}},
		stepDefinition{title: "核对完成条件", description: "对照任务契约整理结果和未完成项。", gate: GateEndToEnd, acceptance: []string{"所有前置步骤已通过"}},
	)
	return definitions
}

func isUserManagementFeature(goal string) bool {
	lower := strings.ToLower(goal)
	hasLogin := containsAny(lower, []string{"登录", "login"})
	hasRegister := containsAny(lower, []string{"注册", "register"})
	hasUsers := containsAny(lower, []string{"用户管理", "用户信息", "crud", "user management"})
	return hasLogin && hasRegister && hasUsers
}

func (s *Store) Get(ctx context.Context, taskID string, goal string) (Plan, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, task_id, contract_goal, version, status, created_at, updated_at
FROM task_plans WHERE task_id = ? AND contract_goal = ? ORDER BY version DESC LIMIT 1`, taskID, goal)
	var item Plan
	var createdAt, updatedAt string
	if err := row.Scan(&item.ID, &item.TaskID, &item.ContractGoal, &item.Version, &item.Status, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Plan{}, ErrNotFound
		}
		return Plan{}, fmt.Errorf("scan task plan: %w", err)
	}
	item.CreatedAt = dbutil.ParseTime(createdAt)
	item.UpdatedAt = dbutil.ParseTime(updatedAt)
	steps, err := s.listSteps(ctx, item.ID)
	if err != nil {
		return Plan{}, err
	}
	item.Steps = steps
	return item, nil
}

func (s *Store) CurrentStep(ctx context.Context, taskID string, goal string) (Step, error) {
	plan, err := s.Get(ctx, taskID, goal)
	if err != nil {
		return Step{}, err
	}
	for _, step := range plan.Steps {
		if step.Status == StatusInProgress || step.Status == StatusBlocked || step.Status == StatusPending {
			return step, nil
		}
	}
	return Step{}, ErrNotFound
}

func (s *Store) IsComplete(ctx context.Context, taskID string, goal string) (bool, error) {
	plan, err := s.Get(ctx, taskID, goal)
	if err != nil {
		return false, err
	}
	return plan.Status == StatusCompleted, nil
}

func (s *Store) Assess(ctx context.Context, taskID string, goal string, observation ToolObservation) (Plan, bool, error) {
	plan, err := s.Get(ctx, taskID, goal)
	if err != nil {
		return Plan{}, false, err
	}
	currentIndex := -1
	for index, step := range plan.Steps {
		if step.Status == StatusInProgress {
			currentIndex = index
			break
		}
	}
	if currentIndex < 0 {
		return plan, false, nil
	}
	satisfied, err := s.gateSatisfied(ctx, plan.Steps[currentIndex], observation)
	if err != nil {
		return Plan{}, false, err
	}
	if !satisfied {
		return plan, false, nil
	}
	now := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Plan{}, false, fmt.Errorf("begin plan progress transaction: %w", err)
	}
	defer tx.Rollback()
	current := plan.Steps[currentIndex]
	_, err = tx.ExecContext(ctx, `
UPDATE task_plan_steps SET status = ?, completed_evidence_id = NULLIF(?, ''), updated_at = ? WHERE id = ?`,
		StatusCompleted, observation.EvidenceID, dbutil.FormatTime(now), current.ID)
	if err != nil {
		return Plan{}, false, fmt.Errorf("complete plan step: %w", err)
	}
	planStatus := StatusInProgress
	if currentIndex+1 < len(plan.Steps) {
		_, err = tx.ExecContext(ctx, `UPDATE task_plan_steps SET status = ?, updated_at = ? WHERE id = ?`,
			StatusInProgress, dbutil.FormatTime(now), plan.Steps[currentIndex+1].ID)
		if err != nil {
			return Plan{}, false, fmt.Errorf("activate next plan step: %w", err)
		}
	} else {
		planStatus = StatusCompleted
	}
	_, err = tx.ExecContext(ctx, `UPDATE task_plans SET status = ?, updated_at = ? WHERE id = ?`, planStatus, dbutil.FormatTime(now), plan.ID)
	if err != nil {
		return Plan{}, false, fmt.Errorf("update task plan: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Plan{}, false, fmt.Errorf("commit plan progress: %w", err)
	}
	updated, err := s.Get(ctx, taskID, goal)
	if err != nil || updated.Status == StatusCompleted {
		return updated, true, err
	}
	// A single acceptance command can prove several adjacent gates. Reuse the
	// same evidence until it no longer satisfies the next ordered step.
	fullyAdvanced, advancedAgain, err := s.Assess(ctx, taskID, goal, observation)
	if err != nil {
		return Plan{}, false, err
	}
	if advancedAgain {
		return fullyAdvanced, true, nil
	}
	return updated, true, nil
}

func (s *Store) gateSatisfied(ctx context.Context, step Step, observation ToolObservation) (bool, error) {
	if !observation.Succeeded {
		return false, nil
	}
	tool := strings.ToLower(observation.ToolName)
	text := strings.ToLower(observationSearchText(observation))
	switch step.GateType {
	case GateEvidence:
		if tool != "read_file" && tool != "list_dir" {
			return false, nil
		}
		var count int
		if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(DISTINCT e.resource) FROM evidence e
JOIN task_plans p ON p.task_id = e.task_id
WHERE p.id = ? AND e.created_at >= p.created_at`, step.PlanID).Scan(&count); err != nil {
			return false, fmt.Errorf("count plan evidence resources: %w", err)
		}
		return count >= 1, nil
	case GateImplementation:
		return tool == "write_file", nil
	case GateRegister:
		return tool == "run_command" &&
			containsAny(text, []string{"verify:register", "register_ok", "registration_ok", "/register"}) &&
			hasHTTP2xx(text) && containsAny(text, []string{"\"id\"", "'id'"}), nil
	case GateLogin:
		return tool == "run_command" &&
			containsAny(text, []string{"verify:login", "login_ok", "jwt_ok", "/login"}) &&
			hasHTTP2xx(text) && containsAny(text, []string{"\"token\"", "'token'"}), nil
	case GateUserCRUD:
		return tool == "run_command" && (containsAny(text, []string{"verify:user_crud", "user_crud_ok", "crud_ok"}) || isUserManagementIntegrationTest(text)), nil
	case GateFrontendBuild:
		return tool == "run_command" && containsAny(text, []string{"npm run build", "pnpm run build", "yarn build", "vue-tsc"}), nil
	case GateVerification:
		return tool == "run_command" && containsAny(text, []string{"go test", "go build", "npm run build", "npm test", "mvn test", "mvn verify", "mvn package", "gradle test", "gradle build", "pytest", "cargo test", "dotnet test", "make test"}), nil
	case GateEndToEnd:
		if tool != "run_command" {
			return false, nil
		}
		output := strings.ToLower(observation.OutputText)
		if containsAny(output, []string{"water_e2e_ok", "acceptance passed", "acceptance_ok"}) {
			return true, nil
		}
		return isUserManagementIntegrationTest(text) || containsAny(text, []string{"verify:e2e", "verify-e2e"}), nil
	default:
		return false, nil
	}
}

func isUserManagementIntegrationTest(value string) bool {
	value = strings.ToLower(value)
	if containsAny(value, []string{"skiptests", "skip_tests"}) {
		return false
	}
	if !containsAny(value, []string{"usermanagementintegrationtest", "user_management_integration_test"}) {
		return false
	}
	isMavenTest := strings.Contains(value, "mvn") && containsAny(value, []string{" test", "test "})
	isGradleTest := containsAny(value, []string{"gradle", "gradlew"}) && containsAny(value, []string{" test", "test "})
	return isMavenTest || isGradleTest
}

func hasHTTP2xx(value string) bool {
	normalized := strings.ReplaceAll(strings.ToLower(value), "\r\n", "\n")
	return httpSuccessPattern.MatchString(normalized)
}

func observationSearchText(observation ToolObservation) string {
	parts := []string{observation.Arguments, observation.Purpose, observation.OutputText}
	var output map[string]interface{}
	if json.Unmarshal([]byte(observation.OutputText), &output) == nil {
		parts = append(parts, stringValue(output["command"]), stringValue(output["output"]), stringValue(output["error"]))
	}
	return strings.Join(parts, "\n")
}

func stringValue(value interface{}) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(raw)
}

func (s *Store) listSteps(ctx context.Context, planID string) ([]Step, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, plan_id, position, title, description, status, gate_type, acceptance_json,
       COALESCE(completed_evidence_id, ''), created_at, updated_at
FROM task_plan_steps WHERE plan_id = ? ORDER BY position`, planID)
	if err != nil {
		return nil, fmt.Errorf("query task plan steps: %w", err)
	}
	defer rows.Close()
	var items []Step
	for rows.Next() {
		var item Step
		var acceptanceJSON, createdAt, updatedAt string
		if err := rows.Scan(&item.ID, &item.PlanID, &item.Position, &item.Title, &item.Description, &item.Status,
			&item.GateType, &acceptanceJSON, &item.CompletedEvidenceID, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan task plan step: %w", err)
		}
		if err := json.Unmarshal([]byte(acceptanceJSON), &item.Acceptance); err != nil {
			return nil, fmt.Errorf("decode task plan acceptance: %w", err)
		}
		item.CreatedAt = dbutil.ParseTime(createdAt)
		item.UpdatedAt = dbutil.ParseTime(updatedAt)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate task plan steps: %w", err)
	}
	return items, nil
}

func containsAny(value string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
