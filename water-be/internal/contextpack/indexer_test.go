package contextpack

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ligson/water/water-be/internal/store"
	"github.com/ligson/water/water-be/internal/workspace"
)

func TestIndexerBuildsIncrementalCodeSummariesAndSkipsUnsafeTrees(t *testing.T) {
	root := t.TempDir()
	writeIndexFixture(t, filepath.Join(root, "main.go"), "package main\n\nimport \"fmt\"\n\nfunc main() {}\n")
	writeIndexFixture(t, filepath.Join(root, "internal", "auth.ts"), "import { token } from './token'\nexport function unlock() {}\n")
	writeIndexFixture(t, filepath.Join(root, "README.md"), "# Water\n\nPrivate coding agent.\n")
	writeIndexFixture(t, filepath.Join(root, ".env"), "API_KEY=should-not-be-indexed\n")
	writeIndexFixture(t, filepath.Join(root, "node_modules", "ignored.js"), "export function ignored() {}\n")

	db, err := store.Open(filepath.Join(t.TempDir(), "context-index.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := store.Migrate(db); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	ws, err := workspace.NewStore(db).Create(context.Background(), workspace.CreateInput{Name: "index", RootPath: root})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	indexer := NewIndexer(NewStore(db))
	first, err := indexer.Sync(context.Background(), ws.ID, root)
	if err != nil {
		t.Fatalf("first index: %v", err)
	}
	if first.FilesChanged != 3 || first.FilesIndexed != 3 {
		t.Fatalf("expected three safe files to be indexed, got %#v", first)
	}

	items, err := NewStore(db).ListFileSummaries(context.Background(), ws.ID)
	if err != nil {
		t.Fatalf("list summaries: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected three summaries, got %d", len(items))
	}
	mainSummary, err := NewStore(db).GetFileSummary(context.Background(), ws.ID, filepath.Join(root, "main.go"))
	if err != nil {
		t.Fatalf("get Go summary: %v", err)
	}
	if mainSummary.Language != "go" || mainSummary.ContentHash == "" || mainSummary.SymbolsJSON == "[]" || mainSummary.ImportsJSON == "[]" {
		t.Fatalf("expected structured Go summary, got %#v", mainSummary)
	}

	second, err := indexer.Sync(context.Background(), ws.ID, root)
	if err != nil {
		t.Fatalf("second index: %v", err)
	}
	if second.FilesChanged != 0 || second.FilesIndexed != 3 {
		t.Fatalf("expected unchanged files to be reused, got %#v", second)
	}

	writeIndexFixture(t, filepath.Join(root, "main.go"), "package main\n\nimport \"log\"\n\nfunc main() { log.Print(\"changed\") }\n")
	third, err := indexer.Sync(context.Background(), ws.ID, root)
	if err != nil {
		t.Fatalf("third index: %v", err)
	}
	if third.FilesChanged != 1 {
		t.Fatalf("expected one changed file to be refreshed, got %#v", third)
	}
}

func TestSummarizeSourceOmitsSensitiveConfigurationLines(t *testing.T) {
	summary := summarizeSource([]byte("server:\n  port: 8080\npassword: super-secret\nlogging:\n  level: info\n"))
	if summary == "" || containsSensitiveKey(summary) {
		t.Fatalf("expected summary to omit sensitive configuration values, got %q", summary)
	}
	if strings.Contains(summary, "super-secret") {
		t.Fatalf("expected secret value not to enter file summary, got %q", summary)
	}
}

func writeIndexFixture(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", path, err)
	}
}
