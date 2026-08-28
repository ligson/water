package schedule

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/ligson/water/water-be/internal/dbutil"
	"github.com/ligson/water/water-be/internal/store"
	taskstore "github.com/ligson/water/water-be/internal/task"
	"github.com/ligson/water/water-be/internal/uid"
)

func TestNextRunDailyUsesConfiguredTimezone(t *testing.T) {
	after := time.Date(2026, 8, 28, 0, 30, 0, 0, time.UTC)
	next, err := NextRun(TypeDaily, "09:30", "Asia/Shanghai", after)
	if err != nil {
		t.Fatalf("next run: %v", err)
	}
	if got := next.In(time.FixedZone("CST", 8*60*60)).Format("2006-01-02 15:04"); got != "2026-08-28 09:30" {
		t.Fatalf("unexpected daily next run %s", got)
	}
}

func TestNextRunIntervalRejectsUnsafeFrequency(t *testing.T) {
	if _, err := NextRun(TypeInterval, "30", "Asia/Shanghai", time.Now()); err == nil {
		t.Fatal("expected short interval to be rejected")
	}
}

func TestStoreQueuesAndClaimsDueRunWithoutOverlap(t *testing.T) {
	db := openScheduleTestDB(t)
	ctx := context.Background()
	workspaceID := insertScheduleTestWorkspace(t, db)
	s := NewStore(db)
	item, err := s.Create(ctx, CreateInput{
		WorkspaceID: workspaceID, Name: "每日检查", Prompt: "检查项目测试",
		ScheduleType: TypeInterval, ScheduleExpression: "300", Timezone: "Asia/Shanghai",
		Enabled: true, RetryIntervalSeconds: 60,
	})
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	if _, err := db.Exec(`UPDATE scheduled_tasks SET next_run_at = ? WHERE id = ?`, "2026-08-28T00:00:00Z", item.ID); err != nil {
		t.Fatalf("make schedule due: %v", err)
	}
	if err := s.QueueDue(ctx, time.Now(), 10); err != nil {
		t.Fatalf("queue due: %v", err)
	}
	run, claimedTask, err := s.ClaimQueued(ctx, time.Now())
	if err != nil {
		t.Fatalf("claim run: %v", err)
	}
	if run.Status != RunRunning || claimedTask.ID != item.ID {
		t.Fatalf("unexpected claim: run=%#v task=%#v", run, claimedTask)
	}
	if _, err := s.QueueManual(ctx, item); err != ErrActiveRun {
		t.Fatalf("expected active run guard, got %v", err)
	}
}

func TestRecoverStaleRunsMarksRunningInterrupted(t *testing.T) {
	db := openScheduleTestDB(t)
	ctx := context.Background()
	workspaceID := insertScheduleTestWorkspace(t, db)
	s := NewStore(db)
	item, err := s.Create(ctx, CreateInput{
		WorkspaceID: workspaceID, Name: "恢复检查", Prompt: "检查",
		ScheduleType: TypeDaily, ScheduleExpression: "09:30", Timezone: "Asia/Shanghai",
		Enabled: false, RetryIntervalSeconds: 60,
	})
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	run, err := s.QueueManual(ctx, item)
	if err != nil {
		t.Fatalf("queue manual: %v", err)
	}
	if _, _, err := s.ClaimQueued(ctx, time.Now()); err != nil {
		t.Fatalf("claim run: %v", err)
	}
	if err := s.RecoverStaleRuns(ctx); err != nil {
		t.Fatalf("recover stale runs: %v", err)
	}
	recovered, err := s.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get recovered run: %v", err)
	}
	if recovered.Status != RunInterrupted || recovered.FinishedAt == nil {
		t.Fatalf("unexpected recovered run %#v", recovered)
	}
}

func TestSchedulerReconcileRefreshesResultSummary(t *testing.T) {
	db := openScheduleTestDB(t)
	ctx := context.Background()
	workspaceID := insertScheduleTestWorkspace(t, db)
	s := NewStore(db)
	item, err := s.Create(ctx, CreateInput{
		WorkspaceID: workspaceID, Name: "审批后结果", Prompt: "检查",
		ScheduleType: TypeDaily, ScheduleExpression: "09:30", Timezone: "Asia/Shanghai",
		Enabled: false, RetryIntervalSeconds: 60,
	})
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	run, err := s.QueueManual(ctx, item)
	if err != nil {
		t.Fatalf("queue run: %v", err)
	}
	if _, _, err := s.ClaimQueued(ctx, time.Now()); err != nil {
		t.Fatalf("claim run: %v", err)
	}
	linkedTask, err := taskstore.NewStore(db).Create(ctx, taskstore.CreateInput{
		WorkspaceID: workspaceID,
		Title:       "Scheduled",
	})
	if err != nil {
		t.Fatalf("create linked task: %v", err)
	}
	turn, err := taskstore.NewStore(db).CreateTurn(ctx, taskstore.CreateTurnInput{
		TaskID: linkedTask.ID, UserInput: "检查",
	})
	if err != nil {
		t.Fatalf("create linked turn: %v", err)
	}
	if err := s.BindRun(ctx, run.ID, linkedTask.ID, turn.ID); err != nil {
		t.Fatalf("bind run: %v", err)
	}
	if _, err := taskstore.NewStore(db).UpdateTurnStatus(ctx, turn.ID, taskstore.TurnStatusCompleted); err != nil {
		t.Fatalf("complete turn: %v", err)
	}

	scheduler := NewScheduler(s, nil, nil, WithResultResolver(func(_ context.Context, taskID string) string {
		if taskID != linkedTask.ID {
			t.Fatalf("unexpected task id %s", taskID)
		}
		return "审批后生成的最终结果"
	}))
	if err := scheduler.reconcile(ctx); err != nil {
		t.Fatalf("reconcile run: %v", err)
	}

	updated, err := s.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get reconciled run: %v", err)
	}
	if updated.Status != RunSucceeded || updated.ResultSummary != "审批后生成的最终结果" {
		t.Fatalf("unexpected reconciled run %#v", updated)
	}
}

func openScheduleTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "water.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := store.Migrate(db); err != nil {
		db.Close()
		t.Fatalf("migrate db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func insertScheduleTestWorkspace(t *testing.T, db *sql.DB) string {
	t.Helper()
	id := uid.New("workspace")
	now := dbutil.FormatTime(time.Now())
	if _, err := db.Exec(`
INSERT INTO workspaces (id, name, root_path, permission_mode, trusted, created_at, updated_at)
VALUES (?, ?, ?, ?, 0, ?, ?)`, id, "Test", filepath.Dir(t.TempDir()), "request_approval", now, now); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	return id
}
