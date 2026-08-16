package taskcontract

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ligson/water/water-be/internal/dbutil"
)

const (
	TypeConversation = "conversation"
	TypeAnalysis     = "analysis"
	TypeDiagnostic   = "diagnostic"
	TypeCodeChange   = "code_change"
	TypeDocument     = "document"

	StageUnderstanding      = "understanding"
	StageCollectingEvidence = "collecting_evidence"
	StageImplementing       = "implementing"
	StageVerifying          = "verifying"
	StageFinalizing         = "finalizing"
	StageBlocked            = "blocked"
	StageCompleted          = "completed"
)

var ErrNotFound = errors.New("task contract not found")

type Contract struct {
	TaskID        string    `json:"taskId"`
	Goal          string    `json:"goal"`
	TaskType      string    `json:"taskType"`
	Stage         string    `json:"stage"`
	DoneWhen      []string  `json:"doneWhen"`
	MissingInputs []string  `json:"missingInputs"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func (s *Store) Get(ctx context.Context, taskID string) (Contract, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT task_id, goal, task_type, stage, done_when_json, missing_inputs_json, created_at, updated_at
FROM task_contracts WHERE task_id = ?`, taskID)
	var item Contract
	var doneWhenJSON, missingInputsJSON, createdAt, updatedAt string
	if err := row.Scan(&item.TaskID, &item.Goal, &item.TaskType, &item.Stage, &doneWhenJSON, &missingInputsJSON, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Contract{}, ErrNotFound
		}
		return Contract{}, fmt.Errorf("scan task contract: %w", err)
	}
	if err := json.Unmarshal([]byte(doneWhenJSON), &item.DoneWhen); err != nil {
		return Contract{}, fmt.Errorf("decode task completion criteria: %w", err)
	}
	if err := json.Unmarshal([]byte(missingInputsJSON), &item.MissingInputs); err != nil {
		return Contract{}, fmt.Errorf("decode task missing inputs: %w", err)
	}
	item.CreatedAt = dbutil.ParseTime(createdAt)
	item.UpdatedAt = dbutil.ParseTime(updatedAt)
	return item, nil
}

func (s *Store) Upsert(ctx context.Context, item Contract) (Contract, error) {
	now := time.Now()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	doneWhenJSON, err := json.Marshal(item.DoneWhen)
	if err != nil {
		return Contract{}, fmt.Errorf("encode task completion criteria: %w", err)
	}
	missingInputsJSON, err := json.Marshal(item.MissingInputs)
	if err != nil {
		return Contract{}, fmt.Errorf("encode task missing inputs: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO task_contracts (task_id, goal, task_type, stage, done_when_json, missing_inputs_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(task_id) DO UPDATE SET
  goal = excluded.goal,
  task_type = excluded.task_type,
  stage = excluded.stage,
  done_when_json = excluded.done_when_json,
  missing_inputs_json = excluded.missing_inputs_json,
  updated_at = excluded.updated_at`,
		item.TaskID, item.Goal, item.TaskType, item.Stage, string(doneWhenJSON), string(missingInputsJSON),
		dbutil.FormatTime(item.CreatedAt), dbutil.FormatTime(item.UpdatedAt))
	if err != nil {
		return Contract{}, fmt.Errorf("upsert task contract: %w", err)
	}
	return s.Get(ctx, item.TaskID)
}

func Build(taskID string, goal string) Contract {
	goal = strings.TrimSpace(goal)
	taskType := classify(goal)
	doneWhen := []string{"已向用户给出直接、基于证据的答复"}
	switch taskType {
	case TypeCodeChange:
		doneWhen = []string{"已完成必要的文件修改", "已运行与改动对应的测试或构建并通过", "已说明修改结果和验证结果"}
	case TypeDocument:
		doneWhen = []string{"目标文档已经写入工作区", "已向用户说明文档位置和主要内容"}
	case TypeDiagnostic:
		doneWhen = []string{"已收集能够定位问题的直接证据", "已给出明确诊断或准确列出缺失输入", "修复发生时已完成回归验证"}
	case TypeAnalysis:
		doneWhen = []string{"结论有工作区或工具证据支持", "已区分已确认事实与未验证判断"}
	}
	return Contract{
		TaskID:   taskID,
		Goal:     goal,
		TaskType: taskType,
		Stage:    StageUnderstanding,
		DoneWhen: doneWhen,
	}
}

func classify(goal string) string {
	lower := strings.ToLower(goal)
	if containsAny(lower, []string{"文档", "报告", "readme", "说明书"}) && containsAny(lower, []string{"生成", "创建", "写", "保存", "整理", "修改", "更新", "完善", "改"}) {
		return TypeDocument
	}
	codeIntent := containsAny(lower, []string{"修复", "改造", "修改", "调整", "实现", "新增", "增加", "添加", "开发", "重构", "编写", "写代码", "搭建", "fix", "implement", "refactor"})
	writesTechnicalProject := strings.Contains(lower, "写") && containsAny(lower, []string{
		"spring", "vue", "react", "golang", "go ", "java", "python", "前端", "后端", "接口", "api", "项目", "功能",
	})
	if codeIntent || writesTechnicalProject {
		return TypeCodeChange
	}
	if containsAny(lower, []string{"报错", "错误", "失败", "卡住", "死循环", "401", "403", "500", "timeout", "排查", "诊断"}) {
		return TypeDiagnostic
	}
	if containsAny(lower, []string{"分析", "检查", "审查", "看下", "看看", "review", "调研"}) {
		return TypeAnalysis
	}
	return TypeConversation
}

func containsAny(value string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
