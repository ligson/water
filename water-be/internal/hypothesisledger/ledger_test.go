package hypothesisledger

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/ligson/water/water-be/internal/dbutil"
	"github.com/ligson/water/water-be/internal/store"
)

func TestLedgerPersistsHypothesesAndSemanticEvidence(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "water.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(db); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	seedLedgerTask(t, db)

	ctx := context.Background()
	ledger := NewStore(db)
	hypothesis, created, err := ledger.Ensure(ctx, "task-1", "排查登录 401", "用户不存在", []string{"测试用户名"})
	if err != nil || !created {
		t.Fatalf("ensure hypothesis: created=%t err=%v", created, err)
	}
	if _, created, err := ledger.Ensure(ctx, "task-1", "排查登录 401", "用户不存在", nil); err != nil || created {
		t.Fatalf("expected existing hypothesis: created=%t err=%v", created, err)
	}

	for _, item := range []struct {
		operation string
		resource  string
		hash      string
	}{
		{operation: "read", resource: "/workspace/WebSecurityConfig.java", hash: "same-hash"},
		{operation: "read", resource: "/workspace/UserController.java", hash: "other-hash"},
		{operation: "search", resource: "/workspace/WebSecurityConfig.java", hash: "same-hash"},
	} {
		if _, err := ledger.AddEvidence(ctx, Evidence{
			TaskID:       "task-1",
			TurnID:       "turn-1",
			HypothesisID: hypothesis.ID,
			Kind:         "file",
			Operation:    item.operation,
			Source:       item.operation,
			Resource:     item.resource,
			ContentHash:  item.hash,
			Summary:      "unchanged",
			Outcome:      OutcomeNeutral,
		}); err != nil {
			t.Fatalf("add evidence: %v", err)
		}
	}
	count, err := ledger.CountRecentMatchingEvidence(ctx, hypothesis.ID, "turn-1", "/workspace/WebSecurityConfig.java", "same-hash")
	if err != nil || count != 2 {
		t.Fatalf("expected semantic count 2, got %d err=%v", count, err)
	}

	updated, err := ledger.UpdateStatus(ctx, hypothesis.ID, StatusBlocked, []string{"测试用户名"})
	if err != nil || updated.Status != StatusBlocked {
		t.Fatalf("block hypothesis: %#v err=%v", updated, err)
	}
	if err := ledger.ReopenBlocked(ctx, "task-1", "排查登录 401"); err != nil {
		t.Fatalf("reopen hypothesis: %v", err)
	}
	reopened, err := ledger.Get(ctx, hypothesis.ID)
	if err != nil || reopened.Status != StatusOpen || len(reopened.MissingEvidence) != 1 {
		t.Fatalf("expected reopen to preserve missing evidence, got %#v err=%v", reopened, err)
	}
	snapshot, err := ledger.Snapshot(ctx, "task-1", "排查登录 401", 10)
	if err != nil || len(snapshot.Hypotheses) != 1 || len(snapshot.Evidence) != 3 {
		t.Fatalf("unexpected snapshot: %#v err=%v", snapshot, err)
	}
}

func seedLedgerTask(t *testing.T, db *sql.DB) {
	t.Helper()
	now := dbutil.FormatTime(time.Now())
	if _, err := db.Exec(`INSERT INTO providers (id, name, type, base_url, model, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"provider-1", "test", "openai", "http://localhost", "test", now, now); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces (id, name, root_path, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		"workspace-1", "test", "/workspace", now, now); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO tasks (id, workspace_id, title, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		"task-1", "workspace-1", "login", now, now); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO turns (id, task_id, sequence, status, user_input, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		"turn-1", "task-1", 1, "running", "排查登录 401", now); err != nil {
		t.Fatalf("seed turn: %v", err)
	}
}
