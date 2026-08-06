package approval

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ligson/water/water-be/internal/dbutil"
	"github.com/ligson/water/water-be/internal/uid"
)

const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusRejected = "rejected"

	ActionReadFile   = "read_file"
	ActionWriteFile  = "write_file"
	ActionRunCommand = "run_command"
)

var ErrNotFound = errors.New("approval not found")

type Approval struct {
	ID             string     `json:"id"`
	WorkspaceID    string     `json:"workspaceId"`
	TaskID         string     `json:"taskId,omitempty"`
	TurnID         string     `json:"turnId,omitempty"`
	ActionType     string     `json:"actionType"`
	Target         string     `json:"target"`
	RiskSummary    string     `json:"riskSummary"`
	ExpectedImpact string     `json:"expectedImpact"`
	Status         string     `json:"status"`
	RequestedAt    time.Time  `json:"requestedAt"`
	ResolvedAt     *time.Time `json:"resolvedAt,omitempty"`
	DecisionMsg    string     `json:"decisionMessage"`
	RequestJSON    string     `json:"-"`
}

type Store struct {
	db *sql.DB
}

type CreateInput struct {
	WorkspaceID    string
	TaskID         string
	TurnID         string
	ActionType     string
	Target         string
	RiskSummary    string
	ExpectedImpact string
	RequestJSON    string
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) ListByWorkspace(ctx context.Context, workspaceID string, status string) ([]Approval, error) {
	query := `
SELECT id, workspace_id, COALESCE(task_id, ''), COALESCE(turn_id, ''), action_type, target, risk_summary, expected_impact, status, requested_at, COALESCE(resolved_at, ''), decision_message, request_json
FROM approvals
WHERE workspace_id = ?`
	args := []interface{}{workspaceID}
	if status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY requested_at DESC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query approvals: %w", err)
	}
	defer rows.Close()

	items := make([]Approval, 0)
	for rows.Next() {
		item, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate approvals: %w", err)
	}
	return items, nil
}

func (s *Store) Get(ctx context.Context, id string) (Approval, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, workspace_id, COALESCE(task_id, ''), COALESCE(turn_id, ''), action_type, target, risk_summary, expected_impact, status, requested_at, COALESCE(resolved_at, ''), decision_message, request_json
FROM approvals
WHERE id = ?`, id)
	item, err := scanApproval(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Approval{}, ErrNotFound
	}
	return item, err
}

func (s *Store) Create(ctx context.Context, input CreateInput) (Approval, error) {
	now := time.Now()
	item := Approval{
		ID:             uid.New("appr"),
		WorkspaceID:    input.WorkspaceID,
		TaskID:         input.TaskID,
		TurnID:         input.TurnID,
		ActionType:     input.ActionType,
		Target:         input.Target,
		RiskSummary:    input.RiskSummary,
		ExpectedImpact: input.ExpectedImpact,
		Status:         StatusPending,
		RequestedAt:    now,
		RequestJSON:    dbutil.WithDefault(input.RequestJSON, "{}"),
	}

	_, err := s.db.ExecContext(ctx, `
INSERT INTO approvals (id, workspace_id, task_id, turn_id, action_type, target, risk_summary, expected_impact, status, requested_at, request_json)
VALUES (?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?)`,
		item.ID,
		item.WorkspaceID,
		item.TaskID,
		item.TurnID,
		item.ActionType,
		item.Target,
		item.RiskSummary,
		item.ExpectedImpact,
		item.Status,
		dbutil.FormatTime(item.RequestedAt),
		item.RequestJSON,
	)
	if err != nil {
		return Approval{}, fmt.Errorf("insert approval: %w", err)
	}
	return s.Get(ctx, item.ID)
}

func (s *Store) Resolve(ctx context.Context, id string, status string, message string) (Approval, error) {
	if status != StatusApproved && status != StatusRejected {
		return Approval{}, fmt.Errorf("unsupported approval status %q", status)
	}

	result, err := s.db.ExecContext(ctx, `
UPDATE approvals
SET status = ?, resolved_at = ?, decision_message = ?
WHERE id = ? AND status = ?`,
		status,
		dbutil.FormatTime(time.Now()),
		message,
		id,
		StatusPending,
	)
	if err != nil {
		return Approval{}, fmt.Errorf("resolve approval: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Approval{}, fmt.Errorf("read affected rows: %w", err)
	}
	if affected == 0 {
		return Approval{}, ErrNotFound
	}
	return s.Get(ctx, id)
}

type scanner interface {
	Scan(dest ...interface{}) error
}

func scanApproval(row scanner) (Approval, error) {
	var item Approval
	var requestedAt string
	var resolvedAt string
	if err := row.Scan(&item.ID, &item.WorkspaceID, &item.TaskID, &item.TurnID, &item.ActionType, &item.Target, &item.RiskSummary, &item.ExpectedImpact, &item.Status, &requestedAt, &resolvedAt, &item.DecisionMsg, &item.RequestJSON); err != nil {
		return Approval{}, err
	}
	item.RequestedAt = dbutil.ParseTime(requestedAt)
	if resolvedAt != "" {
		parsed := dbutil.ParseTime(resolvedAt)
		item.ResolvedAt = &parsed
	}
	return item, nil
}
