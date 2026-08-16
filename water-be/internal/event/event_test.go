package event

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ligson/water/water-be/internal/store"
)

func TestAppendSerializesConcurrentTaskEvents(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "water-test.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(db); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	const (
		workspaceID = "workspace-concurrent-events"
		taskID      = "task-concurrent-events"
		eventCount  = 24
	)
	if _, err := db.Exec(`
INSERT INTO workspaces (id, name, root_path, permission_mode, trusted, created_at, updated_at)
VALUES (?, 'test', '/tmp', 'request_approval', 0, '2026-08-11T00:00:00Z', '2026-08-11T00:00:00Z')`, workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if _, err := db.Exec(`
INSERT INTO tasks (id, workspace_id, title, status, created_at, updated_at)
VALUES (?, ?, 'test', 'active', '2026-08-11T00:00:00Z', '2026-08-11T00:00:00Z')`, taskID, workspaceID); err != nil {
		t.Fatalf("insert task: %v", err)
	}

	eventStore := NewStore(db)
	errorsByWorker := make(chan error, eventCount)
	var workers sync.WaitGroup
	for index := range eventCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := eventStore.Append(context.Background(), AppendInput{
				RequestID:   fmt.Sprintf("request-%d", index),
				WorkspaceID: workspaceID,
				TaskID:      taskID,
				Type:        "test.concurrent",
			})
			if err != nil {
				errorsByWorker <- err
			}
		}()
	}
	workers.Wait()
	close(errorsByWorker)
	for err := range errorsByWorker {
		t.Errorf("append concurrent event: %v", err)
	}
	if t.Failed() {
		return
	}

	events, err := eventStore.ListByTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != eventCount {
		t.Fatalf("expected %d events, got %d", eventCount, len(events))
	}
	for index, item := range events {
		want := index + 1
		if item.Sequence != want {
			t.Fatalf("expected sequence %d at index %d, got %d", want, index, item.Sequence)
		}
	}
}
