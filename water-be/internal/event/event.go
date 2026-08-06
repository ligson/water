package event

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ligson/water/water-be/internal/dbutil"
	"github.com/ligson/water/water-be/internal/uid"
)

var ErrNotFound = errors.New("event not found")

type Event struct {
	EventID     string    `json:"eventId"`
	RequestID   string    `json:"requestId"`
	WorkspaceID string    `json:"workspaceId,omitempty"`
	TaskID      string    `json:"taskId,omitempty"`
	TurnID      string    `json:"turnId,omitempty"`
	Sequence    int       `json:"sequence"`
	Type        string    `json:"type"`
	PayloadJSON string    `json:"payloadJson"`
	CreatedAt   time.Time `json:"createdAt"`
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

type AppendInput struct {
	RequestID   string
	WorkspaceID string
	TaskID      string
	TurnID      string
	Type        string
	PayloadJSON string
}

func (s *Store) Append(ctx context.Context, input AppendInput) (Event, error) {
	now := time.Now()
	e := Event{
		EventID:     uid.New("evt"),
		RequestID:   input.RequestID,
		WorkspaceID: input.WorkspaceID,
		TaskID:      input.TaskID,
		TurnID:      input.TurnID,
		Type:        input.Type,
		PayloadJSON: dbutil.WithDefault(input.PayloadJSON, "{}"),
		CreatedAt:   now,
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Event{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	nextSequence, err := nextTaskSequence(ctx, tx, e.TaskID)
	if err != nil {
		return Event{}, err
	}
	e.Sequence = nextSequence

	_, err = tx.ExecContext(ctx, `
INSERT INTO events (id, request_id, workspace_id, task_id, turn_id, sequence, type, payload_json, created_at)
VALUES (?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?)`,
		e.EventID,
		e.RequestID,
		e.WorkspaceID,
		e.TaskID,
		e.TurnID,
		e.Sequence,
		e.Type,
		e.PayloadJSON,
		dbutil.FormatTime(e.CreatedAt),
	)
	if err != nil {
		return Event{}, fmt.Errorf("insert event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Event{}, fmt.Errorf("commit transaction: %w", err)
	}

	return e, nil
}

func (s *Store) ListByTask(ctx context.Context, taskID string) ([]Event, error) {
	return s.ListByTaskAfterSequence(ctx, taskID, 0)
}

func (s *Store) ListByTaskAfterSequence(ctx context.Context, taskID string, afterSequence int) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, request_id, COALESCE(workspace_id, ''), COALESCE(task_id, ''), COALESCE(turn_id, ''), sequence, type, payload_json, created_at
FROM events
WHERE task_id = ? AND sequence > ?
ORDER BY sequence ASC, created_at ASC`, taskID, afterSequence)
	if err != nil {
		return nil, fmt.Errorf("query task events: %w", err)
	}
	defer rows.Close()

	events := make([]Event, 0)
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate task events: %w", err)
	}
	return events, nil
}

type scanner interface {
	Scan(dest ...interface{}) error
}

func scanEvent(row scanner) (Event, error) {
	var e Event
	var createdAt string
	if err := row.Scan(&e.EventID, &e.RequestID, &e.WorkspaceID, &e.TaskID, &e.TurnID, &e.Sequence, &e.Type, &e.PayloadJSON, &createdAt); err != nil {
		return Event{}, err
	}
	e.CreatedAt = dbutil.ParseTime(createdAt)
	return e, nil
}

func nextTaskSequence(ctx context.Context, tx *sql.Tx, taskID string) (int, error) {
	var current sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(sequence) FROM events WHERE task_id = NULLIF(?, '')`, taskID).Scan(&current); err != nil {
		return 0, fmt.Errorf("query next event sequence: %w", err)
	}
	if !current.Valid {
		return 1, nil
	}
	return int(current.Int64) + 1, nil
}
