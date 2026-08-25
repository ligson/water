package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadYAMLOverridesLegacyEnvironment(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	content := []byte(`server:
  http_addr: ":13013"
storage:
  data_dir: "./data"
  database_path: "./data/custom.db"
document:
  engine: markitdown
  python: "/opt/water/python"
`)
	if err := os.WriteFile(configPath, content, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("WATER_HTTP_ADDR", ":8080")
	t.Setenv("WATER_DATA_DIR", "/legacy/data")
	t.Setenv("WATER_DATABASE_PATH", "/legacy/water.db")
	t.Setenv("WATER_DOCUMENT_ENGINE", "native")
	t.Setenv("WATER_DOCUMENT_PYTHON", "/legacy/python")
	t.Setenv("WATER_ACCESS_PIN", "123456")

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.HTTPAddr != ":13013" || cfg.DataDir != filepath.Join(root, "data") || cfg.DatabasePath != filepath.Join(root, "data", "custom.db") {
		t.Fatalf("unexpected runtime config: %#v", cfg)
	}
	if cfg.DocumentEngine != "markitdown" || cfg.DocumentPython != "/opt/water/python" {
		t.Fatalf("unexpected document config: %#v", cfg)
	}
	if cfg.AccessPIN != "123456" {
		t.Fatalf("expected PIN to remain environment-only, got %q", cfg.AccessPIN)
	}
}

func TestLoadFallsBackToEnvironmentWhenYAMLIsMissing(t *testing.T) {
	t.Setenv("WATER_HTTP_ADDR", ":18080")
	t.Setenv("WATER_DATA_DIR", "/tmp/water-data")
	t.Setenv("WATER_DOCUMENT_ENGINE", "native")

	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("load missing config: %v", err)
	}
	if cfg.HTTPAddr != ":18080" || cfg.DataDir != "/tmp/water-data" || cfg.DatabasePath != "/tmp/water-data/water.db" {
		t.Fatalf("unexpected environment fallback: %#v", cfg)
	}
}
