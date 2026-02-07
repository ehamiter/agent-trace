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

func discoverSources(codexHome string) ([]sourceFile, error) {
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
