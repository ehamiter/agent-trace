package index

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type sourceFile struct {
	Path   string
	Source string
}

func discoverAllSources(codexHome, claudeHome string) ([]sourceFile, error) {
	codex, err := discoverCodexSources(codexHome)
	if err != nil {
		return nil, err
	}
	claude, err := discoverClaudeSources(claudeHome)
	if err != nil {
		return nil, err
	}
	return append(codex, claude...), nil
}

func discoverCodexSources(codexHome string) ([]sourceFile, error) {
	sessionsRoot := filepath.Join(codexHome, "sessions")
	rollouts := make([]sourceFile, 0, 64)

	_ = filepath.WalkDir(sessionsRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		name := strings.ToLower(d.Name())
		if strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, ".jsonl") {
			rollouts = append(rollouts, sourceFile{Path: path, Source: "rollout"})
		}
		return nil
	})

	sort.Slice(rollouts, func(i, j int) bool {
		return rollouts[i].Path < rollouts[j].Path
	})

	if len(rollouts) > 0 {
		return rollouts, nil
	}

	historyPath := filepath.Join(codexHome, "history.jsonl")
	if stat, err := os.Stat(historyPath); err == nil && !stat.IsDir() {
		return []sourceFile{{Path: historyPath, Source: "history"}}, nil
	}
	return nil, nil
}

func discoverClaudeSources(claudeHome string) ([]sourceFile, error) {
	projectsRoot := filepath.Join(claudeHome, "projects")
	var sources []sourceFile

	_ = filepath.WalkDir(projectsRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == "subagents" || name == "memory" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), ".jsonl") {
			sources = append(sources, sourceFile{Path: path, Source: "claude"})
		}
		return nil
	})

	sort.Slice(sources, func(i, j int) bool {
		return sources[i].Path < sources[j].Path
	})
	return sources, nil
}
