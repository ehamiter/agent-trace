package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const DefaultGlamourStyle = "dark"

type AppConfig struct {
	CodexHome   string
	ClaudeHomes []string
	DBPath      string
	ExportDir   string
	Reindex     bool
}

// stringSliceFlag is a flag.Value that collects comma-separated or
// repeatedly-set string values into a slice.
type stringSliceFlag []string

func (f *stringSliceFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *stringSliceFlag) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			*f = append(*f, part)
		}
	}
	return nil
}

func Parse() (AppConfig, error) {
	var cfg AppConfig

	defaultCodexHome, err := DetectCodexHome("")
	if err != nil {
		return cfg, err
	}

	var claudeHomeFlag stringSliceFlag
	flag.StringVar(&cfg.CodexHome, "codex-home", defaultCodexHome, "path to CODEX_HOME")
	flag.Var(&claudeHomeFlag, "claude-home", "path(s) to Claude home director(ies); comma-separated or repeated (default: all ~/.claude* dirs with a projects/ subdir)")
	flag.StringVar(&cfg.DBPath, "db-path", "", "path to SQLite index file")
	flag.StringVar(&cfg.ExportDir, "export-dir", "", "override export output directory")
	flag.BoolVar(&cfg.Reindex, "reindex", false, "force full DB rebuild")
	flag.Parse()

	cfg.CodexHome, err = DetectCodexHome(cfg.CodexHome)
	if err != nil {
		return cfg, err
	}

	cfg.ClaudeHomes, err = DetectClaudeHomes([]string(claudeHomeFlag))
	if err != nil {
		return cfg, err
	}

	if cfg.DBPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return cfg, fmt.Errorf("resolve home directory: %w", err)
		}
		cfg.DBPath = filepath.Join(home, ".local", "share", "agent-trace", "index.sqlite")
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

// DetectClaudeHomes returns the list of Claude home directories to scan.
// When explicit paths are provided (via flag or env), those are used directly.
// Otherwise, all ~/.claude* directories that contain a projects/ subdirectory
// are discovered automatically, so ~/.claude-container and similar conventions
// are picked up without any configuration.
func DetectClaudeHomes(explicit []string) ([]string, error) {
	if len(explicit) > 0 {
		cleaned := make([]string, 0, len(explicit))
		for _, p := range explicit {
			cleaned = append(cleaned, filepath.Clean(p))
		}
		return cleaned, nil
	}
	if fromEnv := os.Getenv("CLAUDE_HOME"); fromEnv != "" {
		return []string{filepath.Clean(fromEnv)}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	return discoverClaudeHomes(home), nil
}

// discoverClaudeHomes globs ~/.claude* and returns directories that have a
// projects/ subdirectory, which is the signature of a Claude session store.
// Falls back to ~/.claude if none are found.
func discoverClaudeHomes(home string) []string {
	matches, _ := filepath.Glob(filepath.Join(home, ".claude*"))
	var homes []string
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil || !info.IsDir() {
			continue
		}
		projectsInfo, err := os.Stat(filepath.Join(m, "projects"))
		if err != nil || !projectsInfo.IsDir() {
			continue
		}
		homes = append(homes, m)
	}
	if len(homes) == 0 {
		homes = []string{filepath.Join(home, ".claude")}
	}
	return homes
}
