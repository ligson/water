package schedule

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/ligson/water/water-be/internal/dbutil"
	"github.com/ligson/water/water-be/internal/uid"
)

const (
	TypeDaily    = "daily"
	TypeInterval = "interval"

	ConcurrencySkip = "skip"
	ApprovalPause   = "pause"

	TriggerScheduled = "scheduled"
	TriggerManual    = "manual"
	TriggerRetry     = "retry"

	RunQueued          = "queued"
	RunRunning         = "running"
	RunWaitingApproval = "waiting_approval"
	RunSucceeded       = "succeeded"
	RunFailed          = "failed"
	RunCancelled       = "cancelled"
	RunSkipped         = "skipped"
	RunInterrupted     = "interrupted"
)

var (
	ErrNotFound        = errors.New("scheduled task not found")
	ErrActiveRun       = errors.New("scheduled task already has an active run")
	ErrRunNotActive    = errors.New("scheduled task run is not active")
	ErrInvalidSchedule = errors.New("invalid schedule")
)

type ScheduledTask struct {
	ID                   string     `json:"id"`
	WorkspaceID          string     `json:"workspaceId"`
	Name                 string     `json:"name"`
	Prompt               string     `json:"prompt"`
	ScheduleType         string     `json:"scheduleType"`
	ScheduleExpression   string     `json:"scheduleExpression"`
	Timezone             string     `json:"timezone"`
	Enabled              bool       `json:"enabled"`
	ConcurrencyPolicy    string     `json:"concurrencyPolicy"`
	ApprovalPolicy       string     `json:"approvalPolicy"`
	MaxRetries           int        `json:"maxRetries"`
	RetryIntervalSeconds int        `json:"retryIntervalSeconds"`
	NextRunAt            *time.Time `json:"nextRunAt,omitempty"`
	LastRunAt            *time.Time `json:"lastRunAt,omitempty"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
}

type Run struct {
	ID              string     `json:"id"`
	ScheduledTaskID string     `json:"scheduledTaskId"`
	TaskID          string     `json:"taskId,omitempty"`
	TurnID          string     `json:"turnId,omitempty"`
	TriggerType     string     `json:"triggerType"`
	Status          string     `json:"status"`
	ScheduledAt     time.Time  `json:"scheduledAt"`
	StartedAt       *time.Time `json:"startedAt,omitempty"`
	FinishedAt      *time.Time `json:"finishedAt,omitempty"`
	Attempt         int        `json:"attempt"`
	PromptSnapshot  string     `json:"promptSnapshot"`
	ResultSummary   string     `json:"resultSummary"`
	ErrorMessage    string     `json:"errorMessage"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

type CreateInput struct {
	WorkspaceID          string
	Name                 string
	Prompt               string
	ScheduleType         string
	ScheduleExpression   string
	Timezone             string
	Enabled              bool
	MaxRetries           int
	RetryIntervalSeconds int
}

type UpdateInput = CreateInput

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func Validate(input CreateInput) error {
	if strings.TrimSpace(input.WorkspaceID) == "" || strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Prompt) == "" {
		return fmt.Errorf("%w: workspace, name and prompt are required", ErrInvalidSchedule)
	}
	if input.MaxRetries < 0 || input.MaxRetries > 5 {
		return fmt.Errorf("%w: max retries must be between 0 and 5", ErrInvalidSchedule)
	}
	if input.RetryIntervalSeconds < 60 || input.RetryIntervalSeconds > 86400 {
		return fmt.Errorf("%w: retry interval must be between 60 and 86400 seconds", ErrInvalidSchedule)
	}
	_, err := NextRun(input.ScheduleType, input.ScheduleExpression, input.Timezone, time.Now())
	return err
}

func NextRun(scheduleType, expression, timezone string, after time.Time) (time.Time, error) {
	location, err := time.LoadLocation(strings.TrimSpace(timezone))
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: invalid timezone", ErrInvalidSchedule)
	}
	switch strings.TrimSpace(scheduleType) {
	case TypeDaily:
		parts := strings.Split(strings.TrimSpace(expression), ":")
		if len(parts) != 2 {
			return time.Time{}, fmt.Errorf("%w: daily expression must be HH:MM", ErrInvalidSchedule)
		}
		hour, hourErr := strconv.Atoi(parts[0])
		minute, minuteErr := strconv.Atoi(parts[1])
		if hourErr != nil || minuteErr != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
			return time.Time{}, fmt.Errorf("%w: invalid daily time", ErrInvalidSchedule)
		}
		localAfter := after.In(location)
		candidate := time.Date(localAfter.Year(), localAfter.Month(), localAfter.Day(), hour, minute, 0, 0, location)
		if !candidate.After(localAfter) {
			candidate = candidate.AddDate(0, 0, 1)
		}
		return candidate, nil
	case TypeInterval:
		seconds, parseErr := strconv.Atoi(strings.TrimSpace(expression))
		if parseErr != nil || seconds < 300 || seconds > 30*24*60*60 {
			return time.Time{}, fmt.Errorf("%w: interval must be between 300 and 2592000 seconds", ErrInvalidSchedule)
		}
		return after.Add(time.Duration(seconds) * time.Second), nil
	default:
		return time.Time{}, fmt.Errorf("%w: unsupported schedule type", ErrInvalidSchedule)
	}
}

func (s *Store) Create(ctx context.Context, input CreateInput) (ScheduledTask, error) {
	normalizeInput(&input)
	if err := Validate(input); err != nil {
		return ScheduledTask{}, err
	}
	now := time.Now()
	var nextRun string
	if input.Enabled {
		next, err := NextRun(input.ScheduleType, input.ScheduleExpression, input.Timezone, now)
		if err != nil {
			return ScheduledTask{}, err
		}
		nextRun = dbutil.FormatTime(next)
	}
	id := uid.New("schedule")
	_, err := s.db.ExecContext(ctx, `
INSERT INTO scheduled_tasks (
  id, workspace_id, name, prompt, schedule_type, schedule_expression, timezone,
  enabled, concurrency_policy, approval_policy, max_retries, retry_interval_seconds,
  next_run_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?)`,
		id, input.WorkspaceID, input.Name, input.Prompt, input.ScheduleType,
		input.ScheduleExpression, input.Timezone, boolInt(input.Enabled), ConcurrencySkip,
		ApprovalPause, input.MaxRetries, input.RetryIntervalSeconds, nextRun,
		dbutil.FormatTime(now), dbutil.FormatTime(now),
	)
	if err != nil {
		return ScheduledTask{}, fmt.Errorf("insert scheduled task: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *Store) Update(ctx context.Context, id string, input UpdateInput) (ScheduledTask, error) {
	normalizeInput(&input)
	if err := Validate(input); err != nil {
		return ScheduledTask{}, err
	}
	now := time.Now()
	var nextRun string
	if input.Enabled {
		next, err := NextRun(input.ScheduleType, input.ScheduleExpression, input.Timezone, now)
		if err != nil {
			return ScheduledTask{}, err
		}
		nextRun = dbutil.FormatTime(next)
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE scheduled_tasks
SET workspace_id = ?, name = ?, prompt = ?, schedule_type = ?, schedule_expression = ?,
    timezone = ?, enabled = ?, max_retries = ?, retry_interval_seconds = ?,
    next_run_at = NULLIF(?, ''), updated_at = ?
WHERE id = ?`, input.WorkspaceID, input.Name, input.Prompt, input.ScheduleType,
		input.ScheduleExpression, input.Timezone, boolInt(input.Enabled), input.MaxRetries,
		input.RetryIntervalSeconds, nextRun, dbutil.FormatTime(now), id)
	if err != nil {
		return ScheduledTask{}, fmt.Errorf("update scheduled task: %w", err)
	}
	return s.getAfterResult(ctx, id, result)
}

func (s *Store) SetEnabled(ctx context.Context, id string, enabled bool) (ScheduledTask, error) {
	item, err := s.Get(ctx, id)
	if err != nil {
		return ScheduledTask{}, err
	}
	var nextRun string
	if enabled {
		next, nextErr := NextRun(item.ScheduleType, item.ScheduleExpression, item.Timezone, time.Now())
		if nextErr != nil {
			return ScheduledTask{}, nextErr
		}
		nextRun = dbutil.FormatTime(next)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE scheduled_tasks SET enabled = ?, next_run_at = NULLIF(?, ''), updated_at = ? WHERE id = ?`,
		boolInt(enabled), nextRun, dbutil.FormatTime(time.Now()), id)
	if err != nil {
		return ScheduledTask{}, fmt.Errorf("set scheduled task enabled: %w", err)
	}
	return s.getAfterResult(ctx, id, result)
}

func (s *Store) Delete(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM scheduled_tasks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete scheduled task: %w", err)
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

func (s *Store) Get(ctx context.Context, id string) (ScheduledTask, error) {
	item, err := scanScheduledTask(s.db.QueryRowContext(ctx, scheduledTaskSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ScheduledTask{}, ErrNotFound
	}
	return item, err
}

func (s *Store) List(ctx context.Context, workspaceID string) ([]ScheduledTask, error) {
	query := scheduledTaskSelect
	args := make([]interface{}, 0, 1)
	if strings.TrimSpace(workspaceID) != "" {
		query += ` WHERE workspace_id = ?`
		args = append(args, workspaceID)
	}
	query += ` ORDER BY enabled DESC, next_run_at ASC, updated_at DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list scheduled tasks: %w", err)
	}
	defer rows.Close()
	items := make([]ScheduledTask, 0)
	for rows.Next() {
		item, scanErr := scanScheduledTask(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ListRuns(ctx context.Context, scheduledTaskID string, limit int) ([]Run, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, runSelect+` WHERE scheduled_task_id = ? ORDER BY created_at DESC LIMIT ?`, scheduledTaskID, limit)
	if err != nil {
		return nil, fmt.Errorf("list scheduled task runs: %w", err)
	}
	defer rows.Close()
	items := make([]Run, 0)
	for rows.Next() {
		item, scanErr := scanRun(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetRun(ctx context.Context, id string) (Run, error) {
	item, err := scanRun(s.db.QueryRowContext(ctx, runSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	return item, err
}

func normalizeInput(input *CreateInput) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Name = strings.TrimSpace(input.Name)
	input.Prompt = strings.TrimSpace(input.Prompt)
	input.ScheduleType = strings.TrimSpace(input.ScheduleType)
	input.ScheduleExpression = strings.TrimSpace(input.ScheduleExpression)
	input.Timezone = strings.TrimSpace(input.Timezone)
	if input.Timezone == "" {
		input.Timezone = "Asia/Shanghai"
	}
	if input.RetryIntervalSeconds == 0 {
		input.RetryIntervalSeconds = 300
	}
}

func (s *Store) getAfterResult(ctx context.Context, id string, result sql.Result) (ScheduledTask, error) {
	affected, err := result.RowsAffected()
	if err != nil {
		return ScheduledTask{}, err
	}
	if affected == 0 {
		return ScheduledTask{}, ErrNotFound
	}
	return s.Get(ctx, id)
}

const scheduledTaskSelect = `
SELECT id, workspace_id, name, prompt, schedule_type, schedule_expression, timezone,
       enabled, concurrency_policy, approval_policy, max_retries, retry_interval_seconds,
       COALESCE(next_run_at, ''), COALESCE(last_run_at, ''), created_at, updated_at
FROM scheduled_tasks`

const runSelect = `
SELECT id, scheduled_task_id, COALESCE(task_id, ''), COALESCE(turn_id, ''), trigger_type,
       status, scheduled_at, COALESCE(started_at, ''), COALESCE(finished_at, ''), attempt,
       prompt_snapshot, result_summary, error_message, created_at, updated_at
FROM scheduled_task_runs`

type scanner interface {
	Scan(dest ...interface{}) error
}

func scanScheduledTask(row scanner) (ScheduledTask, error) {
	var item ScheduledTask
	var enabled int
	var nextRun, lastRun, created, updated string
	if err := row.Scan(&item.ID, &item.WorkspaceID, &item.Name, &item.Prompt, &item.ScheduleType,
		&item.ScheduleExpression, &item.Timezone, &enabled, &item.ConcurrencyPolicy,
		&item.ApprovalPolicy, &item.MaxRetries, &item.RetryIntervalSeconds,
		&nextRun, &lastRun, &created, &updated); err != nil {
		return ScheduledTask{}, err
	}
	item.Enabled = enabled != 0
	item.CreatedAt = dbutil.ParseTime(created)
	item.UpdatedAt = dbutil.ParseTime(updated)
	item.NextRunAt = parseOptionalTime(nextRun)
	item.LastRunAt = parseOptionalTime(lastRun)
	return item, nil
}

func scanRun(row scanner) (Run, error) {
	var item Run
	var scheduled, started, finished, created, updated string
	if err := row.Scan(&item.ID, &item.ScheduledTaskID, &item.TaskID, &item.TurnID,
		&item.TriggerType, &item.Status, &scheduled, &started, &finished, &item.Attempt,
		&item.PromptSnapshot, &item.ResultSummary, &item.ErrorMessage, &created, &updated); err != nil {
		return Run{}, err
	}
	item.ScheduledAt = dbutil.ParseTime(scheduled)
	item.StartedAt = parseOptionalTime(started)
	item.FinishedAt = parseOptionalTime(finished)
	item.CreatedAt = dbutil.ParseTime(created)
	item.UpdatedAt = dbutil.ParseTime(updated)
	return item, nil
}

func parseOptionalTime(value string) *time.Time {
	if value == "" {
		return nil
	}
	parsed := dbutil.ParseTime(value)
	return &parsed
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

type Scheduler struct {
	store          *Store
	executor       Executor
	resultResolver ResultResolver
	interval       time.Duration
	logger         Logger
	startOnce      sync.Once
	workerSlot     chan struct{}
}

type Logger interface {
	Error(msg string, args ...interface{})
	Info(msg string, args ...interface{})
}

type Executor func(context.Context, ScheduledTask, Run) ExecutionResult

type ResultResolver func(context.Context, string) string

type SchedulerOption func(*Scheduler)

func WithResultResolver(resolver ResultResolver) SchedulerOption {
	return func(scheduler *Scheduler) {
		scheduler.resultResolver = resolver
	}
}

type ExecutionResult struct {
	Status        string
	ResultSummary string
	ErrorMessage  string
}

func NewScheduler(store *Store, executor Executor, logger Logger, options ...SchedulerOption) *Scheduler {
	scheduler := &Scheduler{
		store: store, executor: executor, logger: logger, interval: 5 * time.Second,
		workerSlot: make(chan struct{}, 1),
	}
	for _, option := range options {
		option(scheduler)
	}
	return scheduler
}

func (s *Scheduler) Start(ctx context.Context) {
	s.startOnce.Do(func() {
		go s.loop(ctx)
	})
}

func (s *Scheduler) Wake(ctx context.Context) {
	if err := s.tick(ctx); err != nil && s.logger != nil {
		s.logger.Error("run scheduled task tick", "error", err)
	}
}

func (s *Scheduler) loop(ctx context.Context) {
	s.Wake(ctx)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Wake(ctx)
		}
	}
}

func (s *Scheduler) tick(ctx context.Context) error {
	now := time.Now()
	if err := s.store.QueueDue(ctx, now, 20); err != nil {
		return err
	}
	if err := s.reconcile(ctx); err != nil {
		return err
	}
	select {
	case s.workerSlot <- struct{}{}:
		run, item, err := s.store.ClaimQueued(ctx, now)
		if errors.Is(err, sql.ErrNoRows) {
			<-s.workerSlot
			return nil
		}
		if err != nil {
			<-s.workerSlot
			return err
		}
		go s.execute(ctx, item, run)
	default:
	}
	return nil
}

func (s *Scheduler) execute(parent context.Context, item ScheduledTask, run Run) {
	defer func() { <-s.workerSlot }()
	result := s.executor(parent, item, run)
	if result.Status == "" {
		result.Status = RunFailed
	}
	if err := s.store.CompleteRun(context.Background(), run.ID, result.Status, result.ResultSummary, result.ErrorMessage); err != nil {
		if s.logger != nil {
			s.logger.Error("complete scheduled task run", "runId", run.ID, "error", err)
		}
		return
	}
	if shouldRetry(result.Status) && run.Attempt <= item.MaxRetries {
		if _, err := s.store.QueueRetry(context.Background(), item, run.Attempt+1); err != nil && !errors.Is(err, ErrActiveRun) && s.logger != nil {
			s.logger.Error("queue scheduled task retry", "runId", run.ID, "error", err)
		}
	}
}

func (s *Scheduler) reconcile(ctx context.Context) error {
	transitions, err := s.store.ReconcileRuns(ctx)
	if err != nil {
		return err
	}
	for _, transition := range transitions {
		if s.resultResolver != nil && transition.Run.TaskID != "" {
			summary := strings.TrimSpace(s.resultResolver(ctx, transition.Run.TaskID))
			if summary != "" && summary != transition.Run.ResultSummary {
				if updateErr := s.store.SetRunResultSummary(ctx, transition.Run.ID, summary); updateErr != nil {
					return updateErr
				}
				transition.Run.ResultSummary = summary
			}
		}
		if shouldRetry(transition.Run.Status) && transition.Run.Attempt <= transition.Task.MaxRetries {
			if _, queueErr := s.store.QueueRetry(ctx, transition.Task, transition.Run.Attempt+1); queueErr != nil && !errors.Is(queueErr, ErrActiveRun) {
				return queueErr
			}
		}
	}
	return nil
}

func shouldRetry(status string) bool {
	return status == RunFailed || status == RunInterrupted
}
