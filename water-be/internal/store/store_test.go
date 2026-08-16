package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenConfiguresEverySQLiteConnection(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "water-test.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if got := db.Stats().MaxOpenConnections; got != maxOpenConnections {
		t.Fatalf("expected %d max open connections, got %d", maxOpenConnections, got)
	}

	ctx := context.Background()
	connections := make([]*sql.Conn, 0, maxOpenConnections)
	for range maxOpenConnections {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("open pooled connection: %v", err)
		}
		connections = append(connections, conn)
	}
	t.Cleanup(func() {
		for _, conn := range connections {
			_ = conn.Close()
		}
	})

	for index, conn := range connections {
		assertPragmaInt(t, ctx, conn, index, "busy_timeout", busyTimeoutMillis)
		assertPragmaInt(t, ctx, conn, index, "foreign_keys", 1)
		assertPragmaInt(t, ctx, conn, index, "synchronous", 1)

		var journalMode string
		if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
			t.Fatalf("read journal_mode on connection %d: %v", index, err)
		}
		if journalMode != "wal" {
			t.Fatalf("expected WAL on connection %d, got %q", index, journalMode)
		}
	}
}

func TestOpenAllowsWriteWhileRowsAreBeingRead(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "water-test.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(`CREATE TABLE lock_test (id INTEGER PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO lock_test (value) VALUES ('first')`); err != nil {
		t.Fatalf("seed table: %v", err)
	}

	rows, err := db.Query(`SELECT id, value FROM lock_test`)
	if err != nil {
		t.Fatalf("start read: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("expected a row")
	}
	var id int
	var value string
	if err := rows.Scan(&id, &value); err != nil {
		t.Fatalf("scan row: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, `INSERT INTO lock_test (value) VALUES ('second')`); err != nil {
		t.Fatalf("write during active read: %v", err)
	}
}

func assertPragmaInt(t *testing.T, ctx context.Context, conn *sql.Conn, connectionIndex int, name string, want int) {
	t.Helper()
	var got int
	if err := conn.QueryRowContext(ctx, "PRAGMA "+name).Scan(&got); err != nil {
		t.Fatalf("read %s on connection %d: %v", name, connectionIndex, err)
	}
	if got != want {
		t.Fatalf("expected %s=%d on connection %d, got %d", name, want, connectionIndex, got)
	}
}
