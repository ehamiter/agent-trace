package export

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"codex-trace/internal/index"
)

type Exporter struct {
	overrideDir string
	cwd         string
}

func New(overrideDir string) (*Exporter, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve cwd: %w", err)
	}
	return &Exporter{overrideDir: strings.TrimSpace(overrideDir), cwd: cwd}, nil
}

func (e *Exporter) Export(session index.Session, messages []index.Message, toggles index.TranscriptToggles) (string, error) {
	path, err := e.outputPath(session)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create export directory: %w", err)
	}

	body := BuildTranscriptMarkdown(messages, toggles)
	md := BuildSessionMarkdown(session, body, time.Now().UTC())
	if err := os.WriteFile(path, []byte(md), 0o644); err != nil {
		return "", fmt.Errorf("write export file: %w", err)
	}
	return path, nil
}

func BuildTranscriptMarkdown(messages []index.Message, toggles index.TranscriptToggles) string {
	filtered := index.FilterMessages(messages, toggles)
	var b strings.Builder
	for _, m := range filtered {
		content := strings.TrimSpace(m.Content)
		if m.Role == "user" {
			content = sanitizeUserTranscriptContent(content)
		}
		if content == "" {
			continue
		}

		switch m.Role {
		case "user":
			header := "## You"
			if m.Type == "user_message" {
				header += " (aborted)"
			}
			b.WriteString(header + "\n\n")
			b.WriteString(content + "\n\n")
		case "assistant":
			b.WriteString("## Codex\n\n")
			b.WriteString(content + "\n\n")
		default:
			title := "## Event"
			if indexFilterIsTool(m) {
				title = "## Tool"
			}
			if m.Type != "" {
				title += " (" + m.Type + ")"
			}
			b.WriteString(title + "\n\n")
			b.WriteString("```text\n")
			b.WriteString(content + "\n")
			b.WriteString("```\n\n")
		}
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func sanitizeUserTranscriptContent(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	lower := strings.ToLower(content)
	if strings.Contains(lower, "<instructions>") {
		// Keep structured AGENTS blocks only when the referenced AGENTS.md file
		// exists; otherwise strip stale preamble blocks that render as a dangling
		// heading in Glamour.
		content = stripStaleStructuredAgentsBlock(content)
		if strings.TrimSpace(content) == "" {
			return ""
		}
		return content
	}

	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if isAgentsHeadingLine(line) {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

var agentsHeadingLineRe = regexp.MustCompile(`(?i)^[\s#>*` + "`" + `-]*agents\.md instructions for\b`)
var instructionsBlockRe = regexp.MustCompile(`(?is)<instructions>.*?</instructions>`)

func isAgentsHeadingLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	return agentsHeadingLineRe.MatchString(trimmed)
}

func stripStaleStructuredAgentsBlock(content string) string {
	path, ok := agentsPathFromContent(content)
	if !ok {
		return content
	}
	if agentsFileExists(path) {
		return content
	}

	lines := strings.Split(content, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if isAgentsHeadingLine(line) {
			continue
		}
		filtered = append(filtered, line)
	}
	joined := strings.Join(filtered, "\n")
	joined = instructionsBlockRe.ReplaceAllString(joined, "")
	return strings.TrimSpace(joined)
}

func agentsPathFromContent(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !isAgentsHeadingLine(trimmed) {
			continue
		}
		lower := strings.ToLower(trimmed)
		idx := strings.Index(lower, "agents.md instructions for")
		if idx < 0 {
			continue
		}
		path := strings.TrimSpace(trimmed[idx+len("agents.md instructions for"):])
		path = strings.Trim(path, "`'\"")
		if path == "" {
			return "", false
		}
		return path, true
	}
	return "", false
}

func agentsFileExists(path string) bool {
	st, err := os.Stat(filepath.Join(path, "AGENTS.md"))
	return err == nil && !st.IsDir()
}

func BuildSessionMarkdown(session index.Session, transcript string, now time.Time) string {
	var b strings.Builder
	b.WriteString("# Codex session " + session.ID + "\n\n")
	b.WriteString("Exported: " + now.Format(time.RFC3339) + "\n\n")
	b.WriteString("```text\n")
	b.WriteString("source: " + safeValue(session.Source) + "\n")
	b.WriteString(fmt.Sprintf("message_count: %d\n", session.MessageCount))
	b.WriteString("workdir: " + safeValue(session.Workdir) + "\n")
	b.WriteString("```\n\n")
	b.WriteString(transcript)
	if !strings.HasSuffix(transcript, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

func (e *Exporter) outputPath(session index.Session) (string, error) {
	if e.overrideDir != "" {
		dir := e.overrideDir
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(e.cwd, dir)
		}
		return filepath.Join(dir, safeFileName(session.ID)+".md"), nil
	}

	root := e.cwd
	if session.Workdir != "" {
		if repoRoot := findRepoRoot(session.Workdir); repoRoot != "" {
			root = repoRoot
		}
	}
	return filepath.Join(root, "docs", "codex", safeFileName(session.ID)+".md"), nil
}

func findRepoRoot(start string) string {
	if start == "" {
		return ""
	}
	path := filepath.Clean(start)
	for {
		if st, err := os.Stat(filepath.Join(path, ".git")); err == nil && st != nil {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return ""
		}
		path = parent
	}
}

func safeFileName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "session"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return replacer.Replace(s)
}

func safeValue(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "n/a"
	}
	return s
}

func indexFilterIsTool(m index.Message) bool {
	return strings.Contains(strings.ToLower(m.Role), "tool") || strings.Contains(strings.ToLower(m.Type), "tool")
}
