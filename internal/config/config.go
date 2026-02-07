package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

const DefaultGlamourStyle = "dark"

type AppConfig struct {
	CodexHome string
	DBPath    string
	ExportDir string
	Reindex   bool
}

func Parse() (AppConfig, error) {
	var cfg AppConfig

	defaultCodexHome, err := DetectCodexHome("")
	if err != nil {
		return cfg, err
	}

	flag.StringVar(&cfg.CodexHome, "codex-home", defaultCodexHome, "path to CODEX_HOME")
	flag.StringVar(&cfg.DBPath, "db-path", "", "path to SQLite index file")
	flag.StringVar(&cfg.ExportDir, "export-dir", "", "override export output directory")
	flag.BoolVar(&cfg.Reindex, "reindex", false, "force full DB rebuild")
	flag.Parse()

	cfg.CodexHome, err = DetectCodexHome(cfg.CodexHome)
	if err != nil {
		return cfg, err
	}

	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(cfg.CodexHome, "codex-history-index.sqlite")
	}

	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		return cfg, fmt.Errorf("create db dir: %w", err)
	}

	return cfg, nil
}

func DetectCodexHome(explicit string) (string, error) {
	if explicit != "" {
		return filepath.Clean(explicit), nil
	}
	if fromEnv := os.Getenv("CODEX_HOME"); fromEnv != "" {
		return filepath.Clean(fromEnv), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".codex"), nil
}
