package skill_test

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ligson/water/water-be/internal/skill"
	"github.com/ligson/water/water-be/internal/store"
)

func TestInstallEnableAndDeleteSkill(t *testing.T) {
	db, dataDir := openSkillDB(t)
	skills := skill.NewStore(db, dataDir)
	archive := skillArchive(t, map[string]string{
		"water-doc/skill.json": `{"id":"water-document","name":"文档增强","version":"1.0.0","description":"增强文档工作流","keywords":["PDF","DOCX"],"entry":"SKILL.md"}`,
		"water-doc/SKILL.md":   "# 文档增强\n\n读取长文档时必须按段验证。",
	})

	installed, err := skills.InstallArchive(context.Background(), archive, "upload", "water-document.zip")
	if err != nil {
		t.Fatalf("install skill: %v", err)
	}
	if installed.Enabled || installed.ID != "water-document" || installed.SHA256 == "" {
		t.Fatalf("unexpected installed skill: %#v", installed)
	}
	if _, err := os.Stat(installed.PackagePath); err != nil {
		t.Fatalf("stored package is missing: %v", err)
	}

	enabled, err := skills.SetEnabled(context.Background(), installed.ID, true)
	if err != nil || !enabled.Enabled {
		t.Fatalf("enable skill: %#v, %v", enabled, err)
	}
	active, err := skills.GetEnabled(context.Background(), installed.ID)
	if err != nil || active.Instructions == "" {
		t.Fatalf("unexpected active skill: %#v, %v", active, err)
	}

	packagePath := installed.PackagePath
	if err := skills.Delete(context.Background(), installed.ID); err != nil {
		t.Fatalf("delete skill: %v", err)
	}
	if _, err := os.Stat(packagePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected package removal, got %v", err)
	}
}

func TestInstallRejectsUnsafeOrIncompleteArchive(t *testing.T) {
	db, dataDir := openSkillDB(t)
	skills := skill.NewStore(db, dataDir)
	tests := map[string]map[string]string{
		"path traversal": {
			"../skill.json": `{"id":"unsafe-skill","name":"Unsafe","version":"1"}`,
			"SKILL.md":      "unsafe",
		},
		"missing manifest": {
			"SKILL.md": "missing manifest",
		},
		"invalid id": {
			"skill.json": `{"id":"Bad Skill","name":"Bad","version":"1"}`,
			"SKILL.md":   "invalid id",
		},
		"unsafe version": {
			"skill.json": `{"id":"unsafe-version","name":"Bad","version":"../../escape"}`,
			"SKILL.md":   "invalid version",
		},
	}
	for name, entries := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := skills.InstallArchive(context.Background(), skillArchive(t, entries), "upload", "test.zip")
			if !errors.Is(err, skill.ErrInvalidPackage) {
				t.Fatalf("expected invalid package error, got %v", err)
			}
		})
	}
}

func openSkillDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dataDir := t.TempDir()
	db, err := store.Open(filepath.Join(dataDir, "water.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(db); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	return db, dataDir
}

func skillArchive(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	for name, content := range entries {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatalf("create archive entry: %v", err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("write archive entry: %v", err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}
	return output.Bytes()
}
