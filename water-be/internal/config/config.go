package config

import (
	"os"
	"path/filepath"
)

const (
	defaultHTTPAddr = ":8080"
	defaultDataDir  = "data"
	defaultDBName   = "water.db"
)

type Config struct {
	HTTPAddr     string
	DataDir      string
	DatabasePath string
}

func Load() Config {
	addr := getEnv("WATER_HTTP_ADDR", defaultHTTPAddr)
	dataDir := getEnv("WATER_DATA_DIR", defaultDataDir)
	dbPath := os.Getenv("WATER_DATABASE_PATH")
	if dbPath == "" {
		dbPath = filepath.Join(dataDir, defaultDBName)
	}

	return Config{
		HTTPAddr:     addr,
		DataDir:      dataDir,
		DatabasePath: dbPath,
	}
}

func getEnv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
