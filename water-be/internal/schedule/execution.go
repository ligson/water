package schedule

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ligson/water/water-be/internal/dbutil"
	"github.com/ligson/water/water-be/internal/uid"
)

type RunTransition struct {
	Run  Run
	Task ScheduledTask
}

func (s *Store) QueueDue(ctx context.Context, now time.Time, limit int) error {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin due schedule transaction: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, scheduledTaskSelect+`
WHERE enabled = 1 AND next_run_at IS NOT NULL AND next_run_at <= ?
ORDER BY next_run_at ASC
LIMIT ?`, dbutil.FormatTime(now), limit)
	if err != nil {
		return fmt.Errorf("query due schedules: %w", err)
	}
	due := make([]ScheduledTask, 0)
	for rows.Next() {
		item, scanErr := scanScheduledTask(rows)
		if scanErr != nil {
			rows.Close()
			return scanErr
		}
		due = append(due, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, item := range due {
		if item.NextRunAt == nil {
			continue
		}
		var active int
		if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM scheduled_task_runs
WHERE scheduled_task_id = ? AND status IN (?, ?, ?)`,
			item.ID, RunQueued, RunRunning, RunWaitingApproval).Scan(&active); err != nil {
			return fmt.Errorf("check active scheduled run: %w", err)
		}
		status := RunQueued
		errorMessage := ""
		finishedAt := ""
		if active > 0 {
			status = RunSkipped
			errorMessage = "上一次执行尚未结束，本次已按并发策略跳过。"
			finishedAt = dbutil.FormatTime(now)
		}
		runID := uid.New("scheduled-run")
		_, err := tx.ExecContext(ctx, `
INSERT INTO scheduled_task_runs (
  id, scheduled_task_id, trigger_type, status, scheduled_at, finished_at,
  attempt, prompt_snapshot, error_message, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), 1, ?, ?, ?, ?)`,
			runID, item.ID, TriggerScheduled, status, dbutil.FormatTime(*item.NextRunAt),
			finishedAt, item.Prompt, errorMessage, dbutil.FormatTime(now), dbutil.FormatTime(now))
		if err != nil {
			return fmt.Errorf("queue due scheduled run: %w", err)
		}
		next, err := NextRun(item.ScheduleType, item.ScheduleExpression, item.Timezone, now)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE scheduled_tasks SET next_run_at = ?, updated_at = ? WHERE id = ?`,
			dbutil.FormatTime(next), dbutil.FormatTime(now), item.ID); err != nil {
			return fmt.Errorf("advance scheduled task: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit due schedules: %w", err)
	}
	return nil
}

func (s *Store) QueueManual(ctx context.Context, item ScheduledTask) (Run, error) {
	return s.queueRun(ctx, item, TriggerManual, 1, time.Now())
}

func (s *Store) QueueRetry(ctx context.Context, item ScheduledTask, attempt int) (Run, error) {
	when := time.Now().Add(time.Duration(item.RetryIntervalSeconds) * time.Second)
	return s.queueRun(ctx, item, TriggerRetry, attempt, when)
}

func (s *Store) queueRun(ctx context.Context, item ScheduledTask, trigger string, attempt int, when time.Time) (Run, error) {
	now := time.Now()
	id := uid.New("scheduled-run")
	_, err := s.db.ExecContext(ctx, `
INSERT INTO scheduled_task_runs (
  id, scheduled_task_id, trigger_type, status, scheduled_at, attempt,
  prompt_snapshot, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, item.ID, trigger, RunQueued, dbutil.FormatTime(when), attempt,
		item.Prompt, dbutil.FormatTime(now), dbutil.FormatTime(now))
	if err != nil {
		if isUniqueConstraint(err) {
			return Run{}, ErrActiveRun
		}
		return Run{}, fmt.Errorf("queue scheduled task run: %w", err)
	}
	return s.GetRun(ctx, id)
}

func (s *Store) ClaimQueued(ctx context.Context, now time.Time) (Run, ScheduledTask, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, ScheduledTask{}, fmt.Errorf("begin claim scheduled run: %w", err)
	}
	defer tx.Rollback()

	run, err := scanRun(tx.QueryRowContext(ctx, runSelect+`
WHERE status = ? AND scheduled_at <= ?
ORDER BY scheduled_at ASC, created_at ASC
LIMIT 1`, RunQueued, dbutil.FormatTime(now)))
	if err != nil {
		return Run{}, ScheduledTask{}, err
	}
	result, err := tx.ExecContext(ctx, `
UPDATE scheduled_task_runs SET status = ?, started_at = ?, updated_at = ?
WHERE id = ? AND status = ?`, RunRunning, dbutil.FormatTime(now), dbutil.FormatTime(now), run.ID, RunQueued)
	if err != nil {
		return Run{}, ScheduledTask{}, fmt.Errorf("claim scheduled run: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		if err != nil {
			return Run{}, ScheduledTask{}, err
		}
		return Run{}, ScheduledTask{}, sql.ErrNoRows
	}
	item, err := scanScheduledTask(tx.QueryRowContext(ctx, scheduledTaskSelect+` WHERE id = ?`, run.ScheduledTaskID))
	if err != nil {
		return Run{}, ScheduledTask{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE scheduled_tasks SET last_run_at = ?, updated_at = ? WHERE id = ?`,
		dbutil.FormatTime(now), dbutil.FormatTime(now), item.ID); err != nil {
		return Run{}, ScheduledTask{}, fmt.Errorf("update scheduled task last run: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Run{}, ScheduledTask{}, err
	}
	claimed, err := s.GetRun(ctx, run.ID)
	if err != nil {
		return Run{}, ScheduledTask{}, err
	}
	return claimed, item, nil
}

func (s *Store) BindRun(ctx context.Context, runID, taskID, turnID string) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE scheduled_task_runs SET task_id = NULLIF(?, ''), turn_id = NULLIF(?, ''), updated_at = ?
WHERE id = ? AND status = ?`, taskID, turnID, dbutil.FormatTime(time.Now()), runID, RunRunning)
	if err != nil {
		return fmt.Errorf("bind scheduled task run: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrRunNotActive
	}
	return nil
}

func (s *Store) CompleteRun(ctx context.Context, runID, status, summary, errorMessage string) error {
	now := time.Now()
	finishedAt := ""
	if isTerminalRunStatus(status) {
		finishedAt = dbutil.FormatTime(now)
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE scheduled_task_runs
SET status = ?, result_summary = ?, error_message = ?, finished_at = NULLIF(?, ''), updated_at = ?
WHERE id = ? AND status IN (?, ?, ?)`, status, summary, errorMessage, finishedAt,
		dbutil.FormatTime(now), runID, RunQueued, RunRunning, RunWaitingApproval)
	if err != nil {
		return fmt.Errorf("complete scheduled task run: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrRunNotActive
	}
	return nil
}

func (s *Store) CancelRun(ctx context.Context, runID string) (Run, error) {
	if err := s.CompleteRun(ctx, runID, RunCancelled, "", "执行已取消"); err != nil {
		return Run{}, err
	}
	return s.GetRun(ctx, runID)
}

func (s *Store) SetRunResultSummary(ctx context.Context, runID, summary string) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE scheduled_task_runs SET result_summary = ?, updated_at = ? WHERE id = ?`,
		summary, dbutil.FormatTime(time.Now()), runID)
	if err != nil {
		return fmt.Errorf("update scheduled task run result: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RecoverStaleRuns(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin stale run recovery: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
SELECT r.id, r.scheduled_task_id, r.attempt, r.prompt_snapshot,
       st.max_retries, st.retry_interval_seconds
FROM scheduled_task_runs r
JOIN scheduled_tasks st ON st.id = r.scheduled_task_id
WHERE r.status = ?`, RunRunning)
	if err != nil {
		return fmt.Errorf("query stale scheduled runs: %w", err)
	}
	type staleRun struct {
		id, scheduledTaskID, prompt        string
		attempt, maxRetries, retryInterval int
	}
	items := make([]staleRun, 0)
	for rows.Next() {
		var item staleRun
		if err := rows.Scan(&item.id, &item.scheduledTaskID, &item.attempt, &item.prompt, &item.maxRetries, &item.retryInterval); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	now := time.Now()
	nowText := dbutil.FormatTime(now)
	for _, item := range items {
		if _, err := tx.ExecContext(ctx, `
UPDATE scheduled_task_runs
SET status = ?, finished_at = ?, error_message = '若水服务重启，执行已中断。', updated_at = ?
WHERE id = ? AND status = ?`, RunInterrupted, nowText, nowText, item.id, RunRunning); err != nil {
			return fmt.Errorf("interrupt stale scheduled run: %w", err)
		}
		if item.attempt > item.maxRetries {
			continue
		}
		retryAt := now.Add(time.Duration(item.retryInterval) * time.Second)
		if _, err := tx.ExecContext(ctx, `
INSERT INTO scheduled_task_runs (
  id, scheduled_task_id, trigger_type, status, scheduled_at, attempt,
  prompt_snapshot, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			uid.New("scheduled-run"), item.scheduledTaskID, TriggerRetry, RunQueued,
			dbutil.FormatTime(retryAt), item.attempt+1, item.prompt, nowText, nowText); err != nil {
			return fmt.Errorf("queue stale scheduled run retry: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit stale run recovery: %w", err)
	}
	return nil
}

func (s *Store) ReconcileRuns(ctx context.Context) ([]RunTransition, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT r.id, r.scheduled_task_id, COALESCE(r.task_id, ''), COALESCE(r.turn_id, ''),
       r.trigger_type, r.status, r.scheduled_at, COALESCE(r.started_at, ''),
       COALESCE(r.finished_at, ''), r.attempt, r.prompt_snapshot, r.result_summary,
       r.error_message, r.created_at, r.updated_at, t.status
FROM scheduled_task_runs r
JOIN turns t ON t.id = r.turn_id
WHERE r.status IN (?, ?)`, RunRunning, RunWaitingApproval)
	if err != nil {
		return nil, fmt.Errorf("query active scheduled runs: %w", err)
	}
	type candidate struct {
		run        Run
		turnStatus string
	}
	candidates := make([]candidate, 0)
	for rows.Next() {
		var item candidate
		var scheduled, started, finished, created, updated string
		if err := rows.Scan(&item.run.ID, &item.run.ScheduledTaskID, &item.run.TaskID,
			&item.run.TurnID, &item.run.TriggerType, &item.run.Status, &scheduled, &started,
			&finished, &item.run.Attempt, &item.run.PromptSnapshot, &item.run.ResultSummary,
			&item.run.ErrorMessage, &created, &updated, &item.turnStatus); err != nil {
			rows.Close()
			return nil, err
		}
		item.run.ScheduledAt = dbutil.ParseTime(scheduled)
		item.run.StartedAt = parseOptionalTime(started)
		item.run.FinishedAt = parseOptionalTime(finished)
		item.run.CreatedAt = dbutil.ParseTime(created)
		item.run.UpdatedAt = dbutil.ParseTime(updated)
		candidates = append(candidates, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	transitions := make([]RunTransition, 0)
	for _, candidate := range candidates {
		nextStatus := runStatusForTurn(candidate.turnStatus)
		if nextStatus == "" || nextStatus == candidate.run.Status {
			continue
		}
		if err := s.CompleteRun(ctx, candidate.run.ID, nextStatus, candidate.run.ResultSummary, errorForTurnStatus(candidate.turnStatus)); err != nil {
			if errors.Is(err, ErrRunNotActive) {
				continue
			}
			return nil, err
		}
		updated, err := s.GetRun(ctx, candidate.run.ID)
		if err != nil {
			return nil, err
		}
		item, err := s.Get(ctx, updated.ScheduledTaskID)
		if err != nil {
			return nil, err
		}
		if isTerminalRunStatus(updated.Status) {
			transitions = append(transitions, RunTransition{Run: updated, Task: item})
		}
	}
	return transitions, nil
}

func runStatusForTurn(status string) string {
	switch status {
	case "created", "running":
		return RunRunning
	case "waiting_approval":
		return RunWaitingApproval
	case "completed":
		return RunSucceeded
	case "interrupted":
		return RunInterrupted
	case "blocked", "paused", "failed":
		return RunFailed
	default:
		return ""
	}
}

func errorForTurnStatus(status string) string {
	switch status {
	case "blocked":
		return "自动任务缺少继续执行所需的信息。"
	case "paused":
		return "自动任务达到安全执行上限但尚未完成。"
	case "failed":
		return "自动任务执行失败，请查看任务事件。"
	case "interrupted":
		return "自动任务执行被中断。"
	default:
		return ""
	}
}

func isTerminalRunStatus(status string) bool {
	switch status {
	case RunSucceeded, RunFailed, RunCancelled, RunSkipped, RunInterrupted:
		return true
	default:
		return false
	}
}

func isUniqueConstraint(err error) bool {
	return stringsContains(err.Error(), "UNIQUE constraint failed")
}

func stringsContains(value, target string) bool {
	for i := 0; i+len(target) <= len(value); i++ {
		if value[i:i+len(target)] == target {
			return true
		}
	}
	return false
}
