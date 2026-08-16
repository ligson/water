package contextpack

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ligson/water/water-be/internal/dbutil"
	"github.com/ligson/water/water-be/internal/uid"
)

var ErrNotFound = errors.New("context summary not found")

type FileSummary struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspaceId"`
	Path        string    `json:"path"`
	ContentHash string    `json:"contentHash"`
	Language    string    `json:"language"`
	Summary     string    `json:"summary"`
	SymbolsJSON string    `json:"symbolsJson"`
	ImportsJSON string    `json:"importsJson"`
	MatchReason string    `json:"matchReason,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type TaskSummary struct {
	ID          string    `json:"id"`
	TaskID      string    `json:"taskId"`
	ContentHash string    `json:"contentHash"`
	Summary     string    `json:"summary"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type UpsertFileSummaryInput struct {
	WorkspaceID string
	Path        string
	ContentHash string
	Language    string
	Summary     string
	SymbolsJSON string
	ImportsJSON string
}

type UpsertTaskSummaryInput struct {
	TaskID      string
	ContentHash string
	Summary     string
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) UpsertFileSummary(ctx context.Context, input UpsertFileSummaryInput) (FileSummary, error) {
	now := time.Now()
	id := uid.New("fsum")
	_, err := s.db.ExecContext(ctx, `
INSERT INTO file_summaries (id, workspace_id, path, content_hash, language, summary, symbols_json, imports_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(workspace_id, path) DO UPDATE SET
  content_hash = excluded.content_hash,
  language = excluded.language,
  summary = excluded.summary,
  symbols_json = excluded.symbols_json,
  imports_json = excluded.imports_json,
  updated_at = excluded.updated_at`,
		id,
		input.WorkspaceID,
		input.Path,
		input.ContentHash,
		input.Language,
		input.Summary,
		dbutil.WithDefault(input.SymbolsJSON, "[]"),
		dbutil.WithDefault(input.ImportsJSON, "[]"),
		dbutil.FormatTime(now),
		dbutil.FormatTime(now),
	)
	if err != nil {
		return FileSummary{}, fmt.Errorf("upsert file summary: %w", err)
	}
	return s.GetFileSummary(ctx, input.WorkspaceID, input.Path)
}

func (s *Store) GetFileSummary(ctx context.Context, workspaceID string, path string) (FileSummary, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, workspace_id, path, content_hash, language, summary, symbols_json, imports_json, created_at, updated_at
FROM file_summaries
WHERE workspace_id = ? AND path = ?`, workspaceID, path)
	item, err := scanFileSummary(row)
	if errors.Is(err, sql.ErrNoRows) {
		return FileSummary{}, ErrNotFound
	}
	return item, err
}

func (s *Store) ListFileSummaries(ctx context.Context, workspaceID string) ([]FileSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, workspace_id, path, content_hash, language, summary, symbols_json, imports_json, created_at, updated_at
FROM file_summaries
WHERE workspace_id = ?
ORDER BY updated_at DESC, path ASC`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("query file summaries: %w", err)
	}
	defer rows.Close()

	items := make([]FileSummary, 0)
	for rows.Next() {
		item, err := scanFileSummary(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate file summaries: %w", err)
	}
	return items, nil
}

func (s *Store) UpsertTaskSummary(ctx context.Context, input UpsertTaskSummaryInput) (TaskSummary, error) {
	now := time.Now()
	id := uid.New("tsum")
	_, err := s.db.ExecContext(ctx, `
INSERT INTO task_summaries (id, task_id, content_hash, summary, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(task_id) DO UPDATE SET
  content_hash = excluded.content_hash,
  summary = excluded.summary,
  updated_at = excluded.updated_at`,
		id,
		input.TaskID,
		input.ContentHash,
		input.Summary,
		dbutil.FormatTime(now),
		dbutil.FormatTime(now),
	)
	if err != nil {
		return TaskSummary{}, fmt.Errorf("upsert task summary: %w", err)
	}
	return s.GetTaskSummary(ctx, input.TaskID)
}

func (s *Store) GetTaskSummary(ctx context.Context, taskID string) (TaskSummary, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, task_id, content_hash, summary, created_at, updated_at
FROM task_summaries
WHERE task_id = ?`, taskID)
	item, err := scanTaskSummary(row)
	if errors.Is(err, sql.ErrNoRows) {
		return TaskSummary{}, ErrNotFound
	}
	return item, err
}

type scanner interface {
	Scan(dest ...interface{}) error
}

func scanFileSummary(row scanner) (FileSummary, error) {
	var item FileSummary
	var createdAt string
	var updatedAt string
	if err := row.Scan(&item.ID, &item.WorkspaceID, &item.Path, &item.ContentHash, &item.Language, &item.Summary, &item.SymbolsJSON, &item.ImportsJSON, &createdAt, &updatedAt); err != nil {
		return FileSummary{}, err
	}
	item.CreatedAt = dbutil.ParseTime(createdAt)
	item.UpdatedAt = dbutil.ParseTime(updatedAt)
	return item, nil
}

func scanTaskSummary(row scanner) (TaskSummary, error) {
	var item TaskSummary
	var createdAt string
	var updatedAt string
	if err := row.Scan(&item.ID, &item.TaskID, &item.ContentHash, &item.Summary, &createdAt, &updatedAt); err != nil {
		return TaskSummary{}, err
	}
	item.CreatedAt = dbutil.ParseTime(createdAt)
	item.UpdatedAt = dbutil.ParseTime(updatedAt)
	return item, nil
}
