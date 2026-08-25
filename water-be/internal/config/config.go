package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultHTTPAddr = ":8080"
	defaultDataDir  = "data"
	defaultDBName   = "water.db"
)

type Config struct {
	HTTPAddr       string
	DataDir        string
	DatabasePath   string
	AccessPIN      string
	DocumentEngine string
	DocumentPython string
	AuthEnabled    bool
}

type fileConfig struct {
	Server struct {
		HTTPAddr string `yaml:"http_addr"`
	} `yaml:"server"`
	Storage struct {
		DataDir      string `yaml:"data_dir"`
		DatabasePath string `yaml:"database_path"`
	} `yaml:"storage"`
	Document struct {
		Engine string `yaml:"engine"`
		Python string `yaml:"python"`
	} `yaml:"document"`
}

// Load reads non-sensitive runtime settings from YAML and keeps environment
// variables as a backwards-compatible fallback for older installations.
func Load(configPath string) (Config, error) {
	configPath = resolveConfigPath(configPath)
	fileValues := fileConfig{}
	configDir := ""
	configLoaded := false
	if configPath != "" {
		configDir = filepath.Dir(configPath)
		contents, err := os.ReadFile(configPath)
		if err == nil {
			if err := yaml.Unmarshal(contents, &fileValues); err != nil {
				return Config{}, fmt.Errorf("parse config file %s: %w", configPath, err)
			}
			configLoaded = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("read config file %s: %w", configPath, err)
		}
	}

	addr := firstNonEmpty(fileValues.Server.HTTPAddr, os.Getenv("WATER_HTTP_ADDR"), defaultHTTPAddr)
	dataDir := firstNonEmpty(fileValues.Storage.DataDir, os.Getenv("WATER_DATA_DIR"), defaultDataDir)
	dbPath := firstNonEmpty(fileValues.Storage.DatabasePath, os.Getenv("WATER_DATABASE_PATH"), "")
	engine := firstNonEmpty(fileValues.Document.Engine, os.Getenv("WATER_DOCUMENT_ENGINE"), "native")
	pythonPath := firstNonEmpty(fileValues.Document.Python, os.Getenv("WATER_DOCUMENT_PYTHON"), "")
	if configLoaded {
		dataDir = resolvePath(configDir, dataDir)
		if dbPath != "" {
			dbPath = resolvePath(configDir, dbPath)
		}
		pythonPath = resolvePath(configDir, pythonPath)
	}
	if dbPath == "" {
		dbPath = filepath.Join(dataDir, defaultDBName)
	}

	return Config{
		HTTPAddr:       addr,
		DataDir:        dataDir,
		DatabasePath:   dbPath,
		AccessPIN:      strings.TrimSpace(os.Getenv("WATER_ACCESS_PIN")),
		DocumentEngine: engine,
		DocumentPython: pythonPath,
	}, nil
}

func resolveConfigPath(configPath string) string {
	if configured := strings.TrimSpace(configPath); configured != "" {
		return configured
	}
	if configured := strings.TrimSpace(os.Getenv("WATER_CONFIG_FILE")); configured != "" {
		return configured
	}
	executable, err := os.Executable()
	if err == nil {
		return filepath.Join(filepath.Dir(executable), "config.yaml")
	}
	return "config.yaml"
}

func resolvePath(baseDir string, value string) string {
	if value == "" || filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(baseDir, value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
