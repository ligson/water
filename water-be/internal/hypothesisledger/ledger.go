package hypothesisledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ligson/water/water-be/internal/dbutil"
	"github.com/ligson/water/water-be/internal/uid"
)

const (
	StatusOpen         = "open"
	StatusSupported    = "supported"
	StatusContradicted = "contradicted"
	StatusBlocked      = "blocked"
	StatusResolved     = "resolved"

	OutcomeSupports    = "supports"
	OutcomeContradicts = "contradicts"
	OutcomeNeutral     = "neutral"
)

var ErrNotFound = errors.New("hypothesis not found")

type Hypothesis struct {
	ID              string    `json:"id"`
	TaskID          string    `json:"taskId"`
	ContractGoal    string    `json:"contractGoal"`
	Claim           string    `json:"claim"`
	Status          string    `json:"status"`
	MissingEvidence []string  `json:"missingEvidence"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type Evidence struct {
	ID            string    `json:"id"`
	TaskID        string    `json:"taskId"`
	TurnID        string    `json:"turnId,omitempty"`
	HypothesisID  string    `json:"hypothesisId"`
	Kind          string    `json:"kind"`
	Operation     string    `json:"operation"`
	Source        string    `json:"source"`
	Resource      string    `json:"resource"`
	ContentHash   string    `json:"contentHash"`
	Summary       string    `json:"summary"`
	Outcome       string    `json:"outcome"`
	EventSequence int       `json:"eventSequence"`
	CreatedAt     time.Time `json:"createdAt"`
}

type Snapshot struct {
	Hypotheses []Hypothesis `json:"hypotheses"`
	Evidence   []Evidence   `json:"evidence"`
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func (s *Store) Ensure(ctx context.Context, taskID string, contractGoal string, claim string, missingEvidence []string) (Hypothesis, bool, error) {
	claim = strings.TrimSpace(claim)
	if claim == "" {
		claim = "完成当前任务目标，并用直接证据验证结论"
	}
	item, err := s.findByClaim(ctx, taskID, contractGoal, claim)
	if err == nil {
		return item, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Hypothesis{}, false, err
	}
	now := time.Now()
	missingJSON, err := json.Marshal(uniqueNonEmpty(missingEvidence))
	if err != nil {
		return Hypothesis{}, false, fmt.Errorf("encode missing evidence: %w", err)
	}
	item = Hypothesis{
		ID:              uid.New("hyp"),
		TaskID:          taskID,
		ContractGoal:    contractGoal,
		Claim:           claim,
		Status:          StatusOpen,
		MissingEvidence: uniqueNonEmpty(missingEvidence),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO hypotheses (id, task_id, contract_goal, claim, status, missing_evidence_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, item.ID, item.TaskID, item.ContractGoal, item.Claim, item.Status,
		string(missingJSON), dbutil.FormatTime(now), dbutil.FormatTime(now))
	if err != nil {
		return Hypothesis{}, false, fmt.Errorf("insert hypothesis: %w", err)
	}
	return item, true, nil
}

func (s *Store) Get(ctx context.Context, id string) (Hypothesis, error) {
	return scanHypothesis(s.db.QueryRowContext(ctx, `
SELECT id, task_id, contract_goal, claim, status, missing_evidence_json, created_at, updated_at
FROM hypotheses WHERE id = ?`, id))
}

func (s *Store) LatestOpen(ctx context.Context, taskID string, contractGoal string) (Hypothesis, error) {
	return scanHypothesis(s.db.QueryRowContext(ctx, `
SELECT id, task_id, contract_goal, claim, status, missing_evidence_json, created_at, updated_at
FROM hypotheses
WHERE task_id = ? AND contract_goal = ? AND status IN (?, ?)
ORDER BY CASE WHEN claim = contract_goal THEN 0 ELSE 1 END, updated_at DESC LIMIT 1`, taskID, contractGoal, StatusOpen, StatusContradicted))
}

func (s *Store) AddEvidence(ctx context.Context, item Evidence) (Evidence, error) {
	if strings.TrimSpace(item.ID) == "" {
		item.ID = uid.New("evd")
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO evidence (id, task_id, turn_id, hypothesis_id, kind, operation, source, resource, content_hash, summary, outcome, event_sequence, created_at)
VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.TaskID, item.TurnID, item.HypothesisID, item.Kind, item.Operation, item.Source,
		item.Resource, item.ContentHash, item.Summary, item.Outcome, item.EventSequence, dbutil.FormatTime(item.CreatedAt))
	if err != nil {
		return Evidence{}, fmt.Errorf("insert evidence: %w", err)
	}
	return item, nil
}

func (s *Store) CountRecentMatchingEvidence(ctx context.Context, hypothesisID string, turnID string, resource string, contentHash string) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM evidence
WHERE hypothesis_id = ? AND turn_id = ? AND resource = ? AND content_hash = ?`,
		hypothesisID, turnID, resource, contentHash).Scan(&count); err != nil {
		return 0, fmt.Errorf("count matching evidence: %w", err)
	}
	return count, nil
}

func (s *Store) ReopenBlocked(ctx context.Context, taskID string, contractGoal string) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE hypotheses SET status = ?, updated_at = ?
WHERE task_id = ? AND contract_goal = ? AND status = ?`,
		StatusOpen, dbutil.FormatTime(time.Now()), taskID, contractGoal, StatusBlocked)
	if err != nil {
		return fmt.Errorf("reopen blocked hypotheses: %w", err)
	}
	return nil
}

func (s *Store) UpdateStatus(ctx context.Context, id string, status string, missingEvidence []string) (Hypothesis, error) {
	missingJSON, err := json.Marshal(uniqueNonEmpty(missingEvidence))
	if err != nil {
		return Hypothesis{}, fmt.Errorf("encode missing evidence: %w", err)
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE hypotheses SET status = ?, missing_evidence_json = ?, updated_at = ? WHERE id = ?`,
		status, string(missingJSON), dbutil.FormatTime(time.Now()), id)
	if err != nil {
		return Hypothesis{}, fmt.Errorf("update hypothesis: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return Hypothesis{}, ErrNotFound
	}
	return s.Get(ctx, id)
}

func (s *Store) UpdateGoalStatus(ctx context.Context, taskID string, contractGoal string, fromStatuses []string, status string, missingEvidence []string) ([]Hypothesis, error) {
	if len(fromStatuses) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(fromStatuses))
	args := make([]interface{}, 0, 5+len(fromStatuses))
	missingJSON, err := json.Marshal(uniqueNonEmpty(missingEvidence))
	if err != nil {
		return nil, fmt.Errorf("encode missing evidence: %w", err)
	}
	args = append(args, status, string(missingJSON), dbutil.FormatTime(time.Now()), taskID, contractGoal)
	for index, value := range fromStatuses {
		placeholders[index] = "?"
		args = append(args, value)
	}
	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`
UPDATE hypotheses SET status = ?, missing_evidence_json = ?, updated_at = ?
WHERE task_id = ? AND contract_goal = ? AND status IN (%s)`, strings.Join(placeholders, ",")), args...)
	if err != nil {
		return nil, fmt.Errorf("update hypotheses for goal: %w", err)
	}
	snapshot, err := s.Snapshot(ctx, taskID, contractGoal, 0)
	if err != nil {
		return nil, err
	}
	return snapshot.Hypotheses, nil
}

func (s *Store) UpdateEvidenceSequence(ctx context.Context, id string, sequence int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE evidence SET event_sequence = ? WHERE id = ?`, sequence, id)
	if err != nil {
		return fmt.Errorf("update evidence event sequence: %w", err)
	}
	return nil
}

func (s *Store) Snapshot(ctx context.Context, taskID string, contractGoal string, evidenceLimit int) (Snapshot, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, task_id, contract_goal, claim, status, missing_evidence_json, created_at, updated_at
FROM hypotheses WHERE task_id = ? AND contract_goal = ? ORDER BY updated_at DESC`, taskID, contractGoal)
	if err != nil {
		return Snapshot{}, fmt.Errorf("query hypotheses: %w", err)
	}
	defer rows.Close()
	var snapshot Snapshot
	for rows.Next() {
		item, err := scanHypothesis(rows)
		if err != nil {
			return Snapshot{}, err
		}
		snapshot.Hypotheses = append(snapshot.Hypotheses, item)
	}
	if err := rows.Err(); err != nil {
		return Snapshot{}, fmt.Errorf("iterate hypotheses: %w", err)
	}
	if evidenceLimit <= 0 || len(snapshot.Hypotheses) == 0 {
		return snapshot, nil
	}
	evidenceRows, err := s.db.QueryContext(ctx, `
SELECT e.id, e.task_id, COALESCE(e.turn_id, ''), e.hypothesis_id, e.kind, e.operation, e.source,
       e.resource, e.content_hash, e.summary, e.outcome, e.event_sequence, e.created_at
FROM evidence e
JOIN hypotheses h ON h.id = e.hypothesis_id
WHERE e.task_id = ? AND h.contract_goal = ?
ORDER BY e.created_at DESC LIMIT ?`, taskID, contractGoal, evidenceLimit)
	if err != nil {
		return Snapshot{}, fmt.Errorf("query evidence: %w", err)
	}
	defer evidenceRows.Close()
	for evidenceRows.Next() {
		var item Evidence
		var createdAt string
		if err := evidenceRows.Scan(&item.ID, &item.TaskID, &item.TurnID, &item.HypothesisID, &item.Kind,
			&item.Operation, &item.Source, &item.Resource, &item.ContentHash, &item.Summary, &item.Outcome,
			&item.EventSequence, &createdAt); err != nil {
			return Snapshot{}, fmt.Errorf("scan evidence: %w", err)
		}
		item.CreatedAt = dbutil.ParseTime(createdAt)
		snapshot.Evidence = append(snapshot.Evidence, item)
	}
	if err := evidenceRows.Err(); err != nil {
		return Snapshot{}, fmt.Errorf("iterate evidence: %w", err)
	}
	return snapshot, nil
}

func (s *Store) findByClaim(ctx context.Context, taskID string, contractGoal string, claim string) (Hypothesis, error) {
	return scanHypothesis(s.db.QueryRowContext(ctx, `
SELECT id, task_id, contract_goal, claim, status, missing_evidence_json, created_at, updated_at
FROM hypotheses WHERE task_id = ? AND contract_goal = ? AND claim = ? ORDER BY updated_at DESC LIMIT 1`, taskID, contractGoal, claim))
}

type scanner interface{ Scan(...interface{}) error }

func scanHypothesis(row scanner) (Hypothesis, error) {
	var item Hypothesis
	var missingJSON, createdAt, updatedAt string
	if err := row.Scan(&item.ID, &item.TaskID, &item.ContractGoal, &item.Claim, &item.Status, &missingJSON, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Hypothesis{}, ErrNotFound
		}
		return Hypothesis{}, fmt.Errorf("scan hypothesis: %w", err)
	}
	if err := json.Unmarshal([]byte(missingJSON), &item.MissingEvidence); err != nil {
		return Hypothesis{}, fmt.Errorf("decode missing evidence: %w", err)
	}
	item.CreatedAt = dbutil.ParseTime(createdAt)
	item.UpdatedAt = dbutil.ParseTime(updatedAt)
	return item, nil
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
