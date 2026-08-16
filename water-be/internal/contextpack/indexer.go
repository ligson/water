package contextpack

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxIndexedFiles = 1200
	maxIndexedBytes = 128 * 1024
)

var indexableExtensions = map[string]string{
	".c": "c", ".cc": "cpp", ".cpp": "cpp", ".cs": "csharp", ".css": "css",
	".go": "go", ".h": "c", ".hpp": "cpp", ".html": "html", ".java": "java",
	".js": "javascript", ".json": "json", ".jsx": "javascript", ".kt": "kotlin",
	".md": "markdown", ".php": "php", ".properties": "properties", ".py": "python",
	".rb": "ruby", ".rs": "rust", ".scss": "scss", ".sh": "shell", ".sql": "sql",
	".ts": "typescript", ".tsx": "typescript", ".vue": "vue", ".xml": "xml",
	".yaml": "yaml", ".yml": "yaml",
}

var ignoredIndexDirectories = map[string]struct{}{
	".git": {}, ".idea": {}, ".next": {}, ".nuxt": {}, ".pytest_cache": {},
	"build": {}, "coverage": {}, "dist": {}, "node_modules": {}, "target": {},
	"vendor": {},
}

type IndexStats struct {
	FilesSeen    int  `json:"filesSeen"`
	FilesIndexed int  `json:"filesIndexed"`
	FilesChanged int  `json:"filesChanged"`
	FilesSkipped int  `json:"filesSkipped"`
	Truncated    bool `json:"truncated"`
}

type Indexer struct {
	store *Store
}

func NewIndexer(store *Store) *Indexer {
	return &Indexer{store: store}
}

func (i *Indexer) Sync(ctx context.Context, workspaceID string, root string) (IndexStats, error) {
	if i == nil || i.store == nil {
		return IndexStats{}, fmt.Errorf("context indexer store is required")
	}
	root, err := filepath.Abs(filepath.Clean(strings.TrimSpace(root)))
	if err != nil {
		return IndexStats{}, fmt.Errorf("resolve workspace root: %w", err)
	}
	if workspaceID == "" || root == "." {
		return IndexStats{}, nil
	}

	stats := IndexStats{}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			stats.FilesSkipped++
			return nil
		}
		if entry.IsDir() {
			if path != root {
				if _, ignored := ignoredIndexDirectories[entry.Name()]; ignored {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			stats.FilesSkipped++
			return nil
		}
		if stats.FilesSeen >= maxIndexedFiles {
			stats.Truncated = true
			return filepath.SkipAll
		}
		stats.FilesSeen++
		if !isIndexableFile(path) {
			stats.FilesSkipped++
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil || info.Size() > maxIndexedBytes {
			stats.FilesSkipped++
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil || bytes.IndexByte(content, 0) >= 0 {
			stats.FilesSkipped++
			return nil
		}
		hash := sha256.Sum256(content)
		contentHash := hex.EncodeToString(hash[:])
		previous, previousErr := i.store.GetFileSummary(ctx, workspaceID, path)
		if previousErr == nil && previous.ContentHash == contentHash {
			stats.FilesIndexed++
			return nil
		}
		if previousErr != nil && previousErr != ErrNotFound {
			return previousErr
		}
		if _, upsertErr := i.store.UpsertFileSummary(ctx, UpsertFileSummaryInput{
			WorkspaceID: workspaceID,
			Path:        path,
			ContentHash: contentHash,
			Language:    languageForPath(path),
			Summary:     summarizeSource(content),
			SymbolsJSON: jsonList(extractSymbols(content)),
			ImportsJSON: jsonList(extractImports(content)),
		}); upsertErr != nil {
			return upsertErr
		}
		stats.FilesIndexed++
		stats.FilesChanged++
		return nil
	})
	if err != nil {
		return stats, fmt.Errorf("index workspace files: %w", err)
	}
	return stats, nil
}

func isIndexableFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if strings.HasPrefix(base, ".env") || strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key") || strings.Contains(base, "secret") {
		return false
	}
	if base == "dockerfile" || base == "makefile" || base == "readme" || strings.HasPrefix(base, "readme.") {
		return true
	}
	_, ok := indexableExtensions[strings.ToLower(filepath.Ext(base))]
	return ok
}

func languageForPath(path string) string {
	base := strings.ToLower(filepath.Base(path))
	if base == "dockerfile" {
		return "dockerfile"
	}
	if base == "makefile" {
		return "makefile"
	}
	if base == "readme" || strings.HasPrefix(base, "readme.") {
		return "markdown"
	}
	return indexableExtensions[strings.ToLower(filepath.Ext(base))]
}

func summarizeSource(content []byte) string {
	lines := strings.Split(string(content), "\n")
	parts := make([]string, 0, 5)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "/*#; ")
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "--") {
			continue
		}
		if containsSensitiveKey(line) {
			continue
		}
		parts = append(parts, line)
		if len(parts) == 5 {
			break
		}
	}
	text := strings.Join(parts, " | ")
	if len([]rune(text)) > 360 {
		text = string([]rune(text)[:360]) + "..."
	}
	return text
}

func containsSensitiveKey(line string) bool {
	lower := strings.ToLower(line)
	for _, key := range []string{"api_key", "apikey", "access_token", "password", "passwd", "private_key", "client_secret", "secret_key", "authorization:"} {
		if strings.Contains(lower, key) {
			return true
		}
	}
	return false
}

func extractSymbols(content []byte) []string {
	values := make(map[string]struct{})
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		for _, prefix := range []string{"func ", "func(", "type ", "class ", "interface ", "def ", "struct ", "enum ", "export class ", "export function ", "public class ", "public interface "} {
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			name := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			fields := strings.FieldsFunc(name, func(r rune) bool {
				return !(r == '_' || r == '-' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z')
			})
			if len(fields) > 0 && fields[0] != "" {
				values[fields[0]] = struct{}{}
			}
			break
		}
		if len(values) >= 80 {
			break
		}
	}
	return sortedKeys(values)
}

func extractImports(content []byte) []string {
	values := make(map[string]struct{})
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "import ") || strings.HasPrefix(line, "from ") || strings.Contains(line, "require(") || strings.HasPrefix(line, "#include ") || strings.HasPrefix(line, "package ") {
			if len([]rune(line)) > 240 {
				line = string([]rune(line)[:240])
			}
			values[line] = struct{}{}
		}
		if len(values) >= 40 {
			break
		}
	}
	return sortedKeys(values)
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	sort.Strings(keys)
	return keys
}

func jsonList(values []string) string {
	raw, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(raw)
}
