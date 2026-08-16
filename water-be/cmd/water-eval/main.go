package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/ligson/water/water-be/internal/eval"
	"github.com/ligson/water/water-be/internal/event"
	"github.com/ligson/water/water-be/internal/store"
)

func main() {
	dbPath := flag.String("db", defaultDatabasePath(), "Water SQLite database path")
	taskIDs := flag.String("task-id", "", "comma-separated task IDs; empty evaluates all tasks")
	flag.Parse()

	db, err := store.Open(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(db); err != nil {
		log.Fatal(err)
	}

	ids, err := selectTaskIDs(context.Background(), db, *taskIDs)
	if err != nil {
		log.Fatal(err)
	}
	eventStore := event.NewStore(db)
	taskReports := make([]eval.TaskReport, 0, len(ids))
	for _, taskID := range ids {
		items, listErr := eventStore.ListByTask(context.Background(), taskID)
		if listErr != nil {
			log.Fatal(listErr)
		}
		taskReports = append(taskReports, eval.AssessTask(taskID, items))
	}
	raw, err := eval.MarshalReport(eval.Aggregate(taskReports))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(raw))
}

func defaultDatabasePath() string {
	if value := strings.TrimSpace(os.Getenv("WATER_DATABASE_PATH")); value != "" {
		return value
	}
	return filepath.Join("data", "water.db")
}

func selectTaskIDs(ctx context.Context, db *sql.DB, requested string) ([]string, error) {
	if strings.TrimSpace(requested) != "" {
		values := make([]string, 0)
		for _, raw := range strings.Split(requested, ",") {
			if value := strings.TrimSpace(raw); value != "" {
				values = append(values, value)
			}
		}
		return values, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT id FROM tasks WHERE archived_at IS NULL ORDER BY updated_at DESC, created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list evaluation tasks: %w", err)
	}
	defer rows.Close()
	values := make([]string, 0)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("scan evaluation task: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate evaluation tasks: %w", err)
	}
	return values, nil
}
