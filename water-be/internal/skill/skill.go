package skill

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	maxArchiveBytes      = 20 * 1024 * 1024
	maxArchiveFiles      = 256
	maxSkillInstructions = 256 * 1024
	maxManifestBytes     = 64 * 1024
	maxSkillIDLength     = 64
	maxSkillNameLength   = 128
	maxSkillDescription  = 1000
)

var skillIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,63}$`)
var skillVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)

var (
	ErrInvalidPackage = errors.New("invalid skill package")
	ErrNotFound       = errors.New("skill not found")
	ErrDisabled       = errors.New("skill disabled")
)

// Manifest describes the stable metadata that every installable Skill must expose.
type Manifest struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Keywords    []string `json:"keywords"`
	Entry       string   `json:"entry"`
}

// Skill is the persisted, user-visible representation of an installed Skill.
type Skill struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Description  string   `json:"description"`
	Keywords     []string `json:"keywords"`
	Source       string   `json:"source"`
	SourceURL    string   `json:"sourceUrl,omitempty"`
	PackagePath  string   `json:"-"`
	SHA256       string   `json:"sha256"`
	Enabled      bool     `json:"enabled"`
	InstalledAt  string   `json:"installedAt"`
	UpdatedAt    string   `json:"updatedAt"`
	Instructions string   `json:"-"`
}

type Store struct {
	db      *sql.DB
	dataDir string
	mu      sync.Mutex
}

func NewStore(db *sql.DB, dataDir string) *Store {
	return &Store{db: db, dataDir: dataDir}
}

// InstallArchive validates an archive, stores it outside the source workspace,
// and atomically upserts its metadata. The archive contents are never executed.
func (s *Store) InstallArchive(ctx context.Context, archiveBytes []byte, source, sourceURL string) (Skill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(archiveBytes) == 0 || len(archiveBytes) > maxArchiveBytes {
		return Skill{}, fmt.Errorf("%w: archive must be between 1 byte and %d MiB", ErrInvalidPackage, maxArchiveBytes/(1024*1024))
	}
	parsed, err := parseArchive(archiveBytes)
	if err != nil {
		return Skill{}, err
	}
	previous, previousErr := s.Get(ctx, parsed.Manifest.ID)
	if previousErr != nil && !errors.Is(previousErr, ErrNotFound) {
		return Skill{}, fmt.Errorf("inspect existing skill: %w", previousErr)
	}

	digest := sha256.Sum256(archiveBytes)
	hash := hex.EncodeToString(digest[:])
	root := filepath.Join(s.dataDir, "skills")
	if strings.TrimSpace(s.dataDir) == "" {
		root = filepath.Join("data", "skills")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return Skill{}, fmt.Errorf("create skill directory: %w", err)
	}
	packagePath := filepath.Join(root, parsed.Manifest.ID+"-"+parsed.Manifest.Version+"-"+hash[:12]+".zip")
	if err := os.WriteFile(packagePath, archiveBytes, 0o600); err != nil {
		return Skill{}, fmt.Errorf("store skill package: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	keywords, _ := json.Marshal(parsed.Manifest.Keywords)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO skills (id, name, version, description, keywords_json, instructions, source, source_url, package_path, sha256, enabled, installed_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			version = excluded.version,
			description = excluded.description,
			keywords_json = excluded.keywords_json,
			instructions = excluded.instructions,
			source = excluded.source,
			source_url = excluded.source_url,
			package_path = excluded.package_path,
			sha256 = excluded.sha256,
			updated_at = excluded.updated_at`,
		parsed.Manifest.ID,
		parsed.Manifest.Name,
		parsed.Manifest.Version,
		parsed.Manifest.Description,
		string(keywords),
		parsed.Instructions,
		normalizeSource(source),
		strings.TrimSpace(sourceURL),
		packagePath,
		hash,
		now,
		now,
	)
	if err != nil {
		if previousErr != nil || previous.PackagePath != packagePath {
			_ = os.Remove(packagePath)
		}
		return Skill{}, fmt.Errorf("persist skill: %w", err)
	}
	if previousErr == nil && previous.PackagePath != "" && previous.PackagePath != packagePath {
		_ = os.Remove(previous.PackagePath)
	}
	return s.Get(ctx, parsed.Manifest.ID)
}

func (s *Store) List(ctx context.Context) ([]Skill, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, version, description, keywords_json, source, source_url, package_path, sha256, enabled, installed_at, updated_at FROM skills ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Skill, 0)
	for rows.Next() {
		item, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) Get(ctx context.Context, id string) (Skill, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, version, description, keywords_json, source, source_url, package_path, sha256, enabled, installed_at, updated_at, instructions FROM skills WHERE id = ?`, id)
	var item Skill
	var keywords string
	var enabled int
	if err := row.Scan(&item.ID, &item.Name, &item.Version, &item.Description, &keywords, &item.Source, &item.SourceURL, &item.PackagePath, &item.SHA256, &enabled, &item.InstalledAt, &item.UpdatedAt, &item.Instructions); errors.Is(err, sql.ErrNoRows) {
		return Skill{}, ErrNotFound
	} else if err != nil {
		return Skill{}, err
	}
	item.Enabled = enabled == 1
	if err := json.Unmarshal([]byte(keywords), &item.Keywords); err != nil {
		item.Keywords = []string{}
	}
	return item, nil
}

func (s *Store) GetEnabled(ctx context.Context, id string) (Skill, error) {
	item, err := s.Get(ctx, id)
	if err != nil {
		return Skill{}, err
	}
	if !item.Enabled {
		return Skill{}, ErrDisabled
	}
	return item, nil
}

func (s *Store) SetEnabled(ctx context.Context, id string, enabled bool) (Skill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.ExecContext(ctx, `UPDATE skills SET enabled = ?, updated_at = ? WHERE id = ?`, boolInt(enabled), time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return Skill{}, err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return Skill{}, ErrNotFound
	}
	return s.Get(ctx, id)
}

func (s *Store) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	item, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM skills WHERE id = ?`, id); err != nil {
		return err
	}
	if item.PackagePath != "" {
		_ = os.Remove(item.PackagePath)
	}
	return nil
}

func (s *Store) ListEnabled(ctx context.Context) ([]Skill, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, version, description, keywords_json, source, source_url, package_path, sha256, enabled, installed_at, updated_at FROM skills WHERE enabled = 1 ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Skill, 0)
	for rows.Next() {
		item, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type parsedArchive struct {
	Manifest     Manifest
	Instructions string
}

func parseArchive(archiveBytes []byte) (parsedArchive, error) {
	reader, err := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	if err != nil {
		return parsedArchive{}, fmt.Errorf("%w: read ZIP: %v", ErrInvalidPackage, err)
	}
	if len(reader.File) == 0 || len(reader.File) > maxArchiveFiles {
		return parsedArchive{}, fmt.Errorf("%w: archive must contain 1-%d files", ErrInvalidPackage, maxArchiveFiles)
	}
	var manifestRaw, instructionRaw []byte
	var manifestDir, instructionDir string
	for _, file := range reader.File {
		if file.FileInfo().Mode()&os.ModeSymlink != 0 {
			return parsedArchive{}, fmt.Errorf("%w: symlinks are not allowed", ErrInvalidPackage)
		}
		name, ok := packageEntryName(file.Name)
		if !ok {
			return parsedArchive{}, fmt.Errorf("%w: unsafe archive path %q", ErrInvalidPackage, file.Name)
		}
		if file.FileInfo().IsDir() {
			continue
		}
		switch strings.ToLower(path.Base(name)) {
		case "skill.json":
			if manifestRaw != nil {
				return parsedArchive{}, fmt.Errorf("%w: duplicate skill.json", ErrInvalidPackage)
			}
			manifestRaw, err = readZipEntry(file, maxManifestBytes)
			manifestDir = path.Dir(name)
		case "skill.md":
			if instructionRaw != nil {
				return parsedArchive{}, fmt.Errorf("%w: duplicate SKILL.md", ErrInvalidPackage)
			}
			instructionRaw, err = readZipEntry(file, maxSkillInstructions)
			instructionDir = path.Dir(name)
		}
		if err != nil {
			return parsedArchive{}, err
		}
	}
	if manifestRaw == nil || instructionRaw == nil {
		return parsedArchive{}, fmt.Errorf("%w: package must contain skill.json and SKILL.md", ErrInvalidPackage)
	}
	if manifestDir != instructionDir {
		return parsedArchive{}, fmt.Errorf("%w: skill.json and SKILL.md must share the same directory", ErrInvalidPackage)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		return parsedArchive{}, fmt.Errorf("%w: invalid skill.json: %v", ErrInvalidPackage, err)
	}
	manifest.ID = strings.TrimSpace(manifest.ID)
	manifest.Name = strings.TrimSpace(manifest.Name)
	manifest.Version = strings.TrimSpace(manifest.Version)
	manifest.Description = strings.TrimSpace(manifest.Description)
	if !skillIDPattern.MatchString(manifest.ID) || len(manifest.ID) > maxSkillIDLength {
		return parsedArchive{}, fmt.Errorf("%w: id must match %s", ErrInvalidPackage, skillIDPattern.String())
	}
	if manifest.Name == "" || len(manifest.Name) > maxSkillNameLength || !skillVersionPattern.MatchString(manifest.Version) {
		return parsedArchive{}, fmt.Errorf("%w: name and version are required and bounded", ErrInvalidPackage)
	}
	if len(manifest.Description) > maxSkillDescription {
		return parsedArchive{}, fmt.Errorf("%w: description is too long", ErrInvalidPackage)
	}
	if manifest.Entry != "" && manifest.Entry != "SKILL.md" {
		return parsedArchive{}, fmt.Errorf("%w: entry must be SKILL.md", ErrInvalidPackage)
	}
	manifest.Entry = "SKILL.md"
	for index, keyword := range manifest.Keywords {
		manifest.Keywords[index] = strings.TrimSpace(keyword)
	}
	instructions := strings.TrimSpace(string(instructionRaw))
	if instructions == "" {
		return parsedArchive{}, fmt.Errorf("%w: SKILL.md is empty", ErrInvalidPackage)
	}
	if !utf8.Valid(instructionRaw) {
		return parsedArchive{}, fmt.Errorf("%w: SKILL.md must be valid UTF-8", ErrInvalidPackage)
	}
	return parsedArchive{Manifest: manifest, Instructions: instructions}, nil
}

func packageEntryName(name string) (string, bool) {
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(name, "/") || strings.ContainsRune(name, 0) {
		return "", false
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return clean, true
}

func readZipEntry(file *zip.File, limit int64) ([]byte, error) {
	if file.UncompressedSize64 > uint64(limit) {
		return nil, fmt.Errorf("%w: archive entry %q is too large", ErrInvalidPackage, file.Name)
	}
	reader, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: open archive entry: %v", ErrInvalidPackage, err)
	}
	defer reader.Close()
	content, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read archive entry: %v", ErrInvalidPackage, err)
	}
	if int64(len(content)) > limit {
		return nil, fmt.Errorf("%w: archive entry %q is too large", ErrInvalidPackage, file.Name)
	}
	return content, nil
}

func scanSkill(rows *sql.Rows) (Skill, error) {
	var item Skill
	var keywords string
	var enabled int
	if err := rows.Scan(&item.ID, &item.Name, &item.Version, &item.Description, &keywords, &item.Source, &item.SourceURL, &item.PackagePath, &item.SHA256, &enabled, &item.InstalledAt, &item.UpdatedAt); err != nil {
		return Skill{}, err
	}
	item.Enabled = enabled == 1
	_ = json.Unmarshal([]byte(keywords), &item.Keywords)
	return item, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func normalizeSource(source string) string {
	if strings.EqualFold(strings.TrimSpace(source), "url") {
		return "url"
	}
	return "upload"
}
