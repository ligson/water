package contextpack

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ligson/water/water-be/internal/store"
)

func TestStoreUpsertAndBuildPack(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "water-test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := store.Migrate(db); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	ctx := t.Context()
	_, err = db.ExecContext(ctx, `
INSERT INTO workspaces (id, name, root_path, permission_mode, trusted, created_at, updated_at)
VALUES ('ws_test', 'Water', '/tmp/water', 'request_approval', 1, '2026-08-05T00:00:00Z', '2026-08-05T00:00:00Z');
INSERT INTO tasks (id, workspace_id, title, status, created_at, updated_at)
VALUES ('task_test', 'ws_test', 'Task', 'created', '2026-08-05T00:00:00Z', '2026-08-05T00:00:00Z');`)
	if err != nil {
		t.Fatalf("seed db: %v", err)
	}

	s := NewStore(db)
	if _, err := s.UpsertTaskSummary(ctx, UpsertTaskSummaryInput{
		TaskID:      "task_test",
		ContentHash: "hash-task",
		Summary:     "用户正在实现 Water MVP。",
	}); err != nil {
		t.Fatalf("upsert task summary: %v", err)
	}
	if _, err := s.UpsertFileSummary(ctx, UpsertFileSummaryInput{
		WorkspaceID: "ws_test",
		Path:        "/tmp/water/main.go",
		ContentHash: "hash-file",
		Language:    "go",
		Summary:     "Go 服务入口。",
	}); err != nil {
		t.Fatalf("upsert file summary: %v", err)
	}

	pack, err := NewBuilder(s).Build(ctx, BuildInput{
		WorkspaceID:   "ws_test",
		TaskID:        "task_test",
		UserInput:     "继续实现",
		ContextTokens: 1000,
	})
	if err != nil {
		t.Fatalf("build pack: %v", err)
	}
	if pack.TokenBudget != 800 {
		t.Fatalf("expected 80 percent token budget, got %d", pack.TokenBudget)
	}
	if pack.TaskSummary == "" || len(pack.FileSummaries) != 1 {
		t.Fatalf("expected task and file summaries in pack: %#v", pack)
	}
}

func TestBuilderTruncatesByBudget(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "water-test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := store.Migrate(db); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	ctx := t.Context()
	_, err = db.ExecContext(ctx, `
INSERT INTO workspaces (id, name, root_path, permission_mode, trusted, created_at, updated_at)
VALUES ('ws_test', 'Water', '/tmp/water', 'request_approval', 1, '2026-08-05T00:00:00Z', '2026-08-05T00:00:00Z')`)
	if err != nil {
		t.Fatalf("seed db: %v", err)
	}

	s := NewStore(db)
	for _, item := range []string{"a.go", "b.go", "c.go"} {
		if _, err := s.UpsertFileSummary(ctx, UpsertFileSummaryInput{
			WorkspaceID: "ws_test",
			Path:        "/tmp/water/" + item,
			ContentHash: item,
			Summary:     strings.Repeat("很长的摘要内容", 40),
		}); err != nil {
			t.Fatalf("upsert file summary: %v", err)
		}
	}

	pack, err := NewBuilder(s).Build(ctx, BuildInput{
		WorkspaceID:   "ws_test",
		ContextTokens: 120,
	})
	if err != nil {
		t.Fatalf("build pack: %v", err)
	}
	if !pack.Truncated {
		t.Fatalf("expected pack to be truncated")
	}
}

func TestBuilderPrioritizesRelevantFileSummaries(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "water-test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := store.Migrate(db); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	ctx := t.Context()
	_, err = db.ExecContext(ctx, `
INSERT INTO workspaces (id, name, root_path, permission_mode, trusted, created_at, updated_at)
VALUES ('ws_test', 'Water', '/tmp/water', 'request_approval', 1, '2026-08-05T00:00:00Z', '2026-08-05T00:00:00Z');
INSERT INTO tasks (id, workspace_id, title, status, created_at, updated_at)
VALUES ('task_test', 'ws_test', 'Task', 'created', '2026-08-05T00:00:00Z', '2026-08-05T00:00:00Z');`)
	if err != nil {
		t.Fatalf("seed db: %v", err)
	}

	s := NewStore(db)
	for _, item := range []UpsertFileSummaryInput{
		{
			WorkspaceID: "ws_test",
			Path:        "/tmp/water/water-be/internal/tools/tools.go",
			ContentHash: "tools",
			Language:    "go",
			Summary:     "实现 run_command 工具和命令权限校验。",
			SymbolsJSON: `["runCommand","isSafeReadOnlyCommand"]`,
		},
		{
			WorkspaceID: "ws_test",
			Path:        "/tmp/water/water-fe/src/style.css",
			ContentHash: "style",
			Language:    "css",
			Summary:     "前端主题和按钮样式。",
		},
	} {
		if _, err := s.UpsertFileSummary(ctx, item); err != nil {
			t.Fatalf("upsert file summary: %v", err)
		}
	}

	pack, err := NewBuilder(s).Build(ctx, BuildInput{
		WorkspaceID:   "ws_test",
		TaskID:        "task_test",
		UserInput:     "修复 run_command 的校验逻辑",
		ContextTokens: 1000,
	})
	if err != nil {
		t.Fatalf("build pack: %v", err)
	}
	if len(pack.FileSummaries) < 2 {
		t.Fatalf("expected file summaries, got %#v", pack.FileSummaries)
	}
	if !strings.HasSuffix(pack.FileSummaries[0].Path, "tools.go") {
		t.Fatalf("expected tools.go to be ranked first, got %q", pack.FileSummaries[0].Path)
	}
}
