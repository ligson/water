package task

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
	TaskStatusCreated  = "created"
	TaskStatusActive   = "active"
	TaskStatusArchived = "archived"

	TurnStatusCreated         = "created"
	TurnStatusRunning         = "running"
	TurnStatusWaitingApproval = "waiting_approval"
	TurnStatusCompleted       = "completed"
	TurnStatusFailed          = "failed"
	TurnStatusInterrupted     = "interrupted"
)

var ErrNotFound = errors.New("task not found")

type Task struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspaceId"`
	Title       string     `json:"title"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	ArchivedAt  *time.Time `json:"archivedAt,omitempty"`
}

type Turn struct {
	ID          string     `json:"id"`
	TaskID      string     `json:"taskId"`
	Sequence    int        `json:"sequence"`
	Status      string     `json:"status"`
	UserInput   string     `json:"userInput"`
	CreatedAt   time.Time  `json:"createdAt"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

type CreateInput struct {
	WorkspaceID string
	Title       string
}

type UpdateInput struct {
	Title string
}

type CreateTurnInput struct {
	TaskID    string
	UserInput string
}

func (s *Store) ListByWorkspace(ctx context.Context, workspaceID string) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, workspace_id, title, status, created_at, updated_at, COALESCE(archived_at, '')
FROM tasks
WHERE workspace_id = ?
ORDER BY updated_at DESC, created_at DESC`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("query tasks: %w", err)
	}
	defer rows.Close()

	tasks := make([]Task, 0)
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tasks: %w", err)
	}
	return tasks, nil
}

func (s *Store) Get(ctx context.Context, id string) (Task, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, workspace_id, title, status, created_at, updated_at, COALESCE(archived_at, '')
FROM tasks
WHERE id = ?`, id)

	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	return t, err
}

func (s *Store) Create(ctx context.Context, input CreateInput) (Task, error) {
	now := time.Now()
	t := Task{
		ID:          uid.New("task"),
		WorkspaceID: input.WorkspaceID,
		Title:       input.Title,
		Status:      TaskStatusCreated,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	_, err := s.db.ExecContext(ctx, `
INSERT INTO tasks (id, workspace_id, title, status, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		t.ID,
		t.WorkspaceID,
		t.Title,
		t.Status,
		dbutil.FormatTime(t.CreatedAt),
		dbutil.FormatTime(t.UpdatedAt),
	)
	if err != nil {
		return Task{}, fmt.Errorf("insert task: %w", err)
	}

	return s.Get(ctx, t.ID)
}

func (s *Store) Delete(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read affected rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) Update(ctx context.Context, id string, input UpdateInput) (Task, error) {
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `
UPDATE tasks
SET title = ?, updated_at = ?
WHERE id = ?`,
		input.Title,
		dbutil.FormatTime(now),
		id,
	)
	if err != nil {
		return Task{}, fmt.Errorf("update task: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Task{}, fmt.Errorf("read affected rows: %w", err)
	}
	if affected == 0 {
		return Task{}, ErrNotFound
	}
	return s.Get(ctx, id)
}

func (s *Store) CreateTurn(ctx context.Context, input CreateTurnInput) (Turn, error) {
	if _, err := s.Get(ctx, input.TaskID); err != nil {
		return Turn{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Turn{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	nextSequence, err := nextTurnSequence(ctx, tx, input.TaskID)
	if err != nil {
		return Turn{}, err
	}

	now := time.Now()
	turn := Turn{
		ID:        uid.New("turn"),
		TaskID:    input.TaskID,
		Sequence:  nextSequence,
		Status:    TurnStatusCreated,
		UserInput: input.UserInput,
		CreatedAt: now,
	}

	_, err = tx.ExecContext(ctx, `
INSERT INTO turns (id, task_id, sequence, status, user_input, created_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		turn.ID,
		turn.TaskID,
		turn.Sequence,
		turn.Status,
		turn.UserInput,
		dbutil.FormatTime(turn.CreatedAt),
	)
	if err != nil {
		return Turn{}, fmt.Errorf("insert turn: %w", err)
	}

	_, err = tx.ExecContext(ctx, `UPDATE tasks SET status = ?, updated_at = ? WHERE id = ?`, TaskStatusActive, dbutil.FormatTime(now), input.TaskID)
	if err != nil {
		return Turn{}, fmt.Errorf("update task after turn: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Turn{}, fmt.Errorf("commit transaction: %w", err)
	}

	return s.GetTurn(ctx, turn.ID)
}

func (s *Store) GetTurn(ctx context.Context, id string) (Turn, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, task_id, sequence, status, user_input, created_at, COALESCE(completed_at, '')
FROM turns
WHERE id = ?`, id)

	turn, err := scanTurn(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Turn{}, ErrNotFound
	}
	return turn, err
}

func (s *Store) UpdateTurnStatus(ctx context.Context, id string, status string) (Turn, error) {
	now := time.Now()
	completedAt := ""
	if isTerminalTurnStatus(status) {
		completedAt = dbutil.FormatTime(now)
	}

	result, err := s.db.ExecContext(ctx, `
UPDATE turns
SET status = ?, completed_at = NULLIF(?, '')
WHERE id = ?`,
		status,
		completedAt,
		id,
	)
	if err != nil {
		return Turn{}, fmt.Errorf("update turn status: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Turn{}, fmt.Errorf("read affected rows: %w", err)
	}
	if affected == 0 {
		return Turn{}, ErrNotFound
	}
	return s.GetTurn(ctx, id)
}

func isTerminalTurnStatus(status string) bool {
	return status == TurnStatusCompleted || status == TurnStatusFailed || status == TurnStatusInterrupted
}

type scanner interface {
	Scan(dest ...interface{}) error
}

func scanTask(row scanner) (Task, error) {
	var t Task
	var archivedAt string
	var createdAt string
	var updatedAt string
	if err := row.Scan(&t.ID, &t.WorkspaceID, &t.Title, &t.Status, &createdAt, &updatedAt, &archivedAt); err != nil {
		return Task{}, err
	}
	t.CreatedAt = dbutil.ParseTime(createdAt)
	t.UpdatedAt = dbutil.ParseTime(updatedAt)
	if archivedAt != "" {
		parsed := dbutil.ParseTime(archivedAt)
		t.ArchivedAt = &parsed
	}
	return t, nil
}

func scanTurn(row scanner) (Turn, error) {
	var turn Turn
	var createdAt string
	var completedAt string
	if err := row.Scan(&turn.ID, &turn.TaskID, &turn.Sequence, &turn.Status, &turn.UserInput, &createdAt, &completedAt); err != nil {
		return Turn{}, err
	}
	turn.CreatedAt = dbutil.ParseTime(createdAt)
	if completedAt != "" {
		parsed := dbutil.ParseTime(completedAt)
		turn.CompletedAt = &parsed
	}
	return turn, nil
}

func nextTurnSequence(ctx context.Context, tx *sql.Tx, taskID string) (int, error) {
	var current sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(sequence) FROM turns WHERE task_id = ?`, taskID).Scan(&current); err != nil {
		return 0, fmt.Errorf("query next turn sequence: %w", err)
	}
	if !current.Valid {
		return 1, nil
	}
	return int(current.Int64) + 1, nil
}
