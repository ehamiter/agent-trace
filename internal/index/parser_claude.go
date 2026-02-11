package index

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

var claudeSessionFileRe = regexp.MustCompile(`([0-9a-fA-F-]{36})\.jsonl$`)

func parseClaudeJSONLLine(line []byte, sourcePath string) ([]parsedEvent, error) {
	var obj map[string]any
	if err := json.Unmarshal(line, &obj); err != nil {
		return nil, err
	}

	typ := asString(firstByPath(obj, []string{"type"}))

	// Skip non-conversational types.
	switch typ {
	case "progress", "file-history-snapshot":
		return nil, nil
	}

	sessionID := asString(firstByPath(obj, []string{"sessionId"}))
	if sessionID == "" {
		sessionID = claudeSessionIDFromPath(sourcePath)
	}

	timestamp := parseClaudeTimestamp(obj)
	workdir := asString(firstByPath(obj, []string{"cwd"}))

	switch typ {
	case "user":
		return parseClaudeUserMessage(obj, sessionID, timestamp, workdir)
	case "assistant":
		return parseClaudeAssistantMessage(obj, sessionID, timestamp, workdir)
	case "system":
		return parseClaudeSystemMessage(obj, sessionID, timestamp, workdir)
	}

	// Unknown type — skip.
	return nil, nil
}

func parseClaudeUserMessage(obj map[string]any, sessionID string, ts *int64, workdir string) ([]parsedEvent, error) {
	msg, _ := obj["message"].(map[string]any)
	if msg == nil {
		return nil, nil
	}

	content := msg["content"]

	// Simple string content.
	if s, ok := content.(string); ok {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, nil
		}
		return []parsedEvent{{
			SessionID: sessionID,
			TS:        ts,
			Role:      "user",
			Content:   s,
			Type:      "message",
			Workdir:   workdir,
		}}, nil
	}

	// Array content — may be tool_result blocks or text blocks.
	arr, ok := content.([]any)
	if !ok || len(arr) == 0 {
		return nil, nil
	}

	var events []parsedEvent
	for _, item := range arr {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		blockType := asString(firstByPath(block, []string{"type"}))
		switch blockType {
		case "tool_result":
			text := extractToolResultContent(block)
			if text == "" {
				continue
			}
			events = append(events, parsedEvent{
				SessionID: sessionID,
				TS:        ts,
				Role:      "tool",
				Content:   text,
				Type:      "tool_result",
				Workdir:   workdir,
			})
		case "text":
			text := strings.TrimSpace(asString(firstByPath(block, []string{"text"})))
			if text == "" {
				continue
			}
			events = append(events, parsedEvent{
				SessionID: sessionID,
				TS:        ts,
				Role:      "user",
				Content:   text,
				Type:      "message",
				Workdir:   workdir,
			})
		}
	}
	return events, nil
}

func parseClaudeAssistantMessage(obj map[string]any, sessionID string, ts *int64, workdir string) ([]parsedEvent, error) {
	msg, _ := obj["message"].(map[string]any)
	if msg == nil {
		return nil, nil
	}

	arr, ok := msg["content"].([]any)
	if !ok || len(arr) == 0 {
		return nil, nil
	}

	var events []parsedEvent
	var textParts []string

	for _, item := range arr {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		blockType := asString(firstByPath(block, []string{"type"}))
		switch blockType {
		case "text":
			text := strings.TrimSpace(asString(firstByPath(block, []string{"text"})))
			if text != "" {
				textParts = append(textParts, text)
			}
		case "tool_use":
			name := asString(firstByPath(block, []string{"name"}))
			input := firstByPath(block, []string{"input"})
			content := formatToolUse(name, input)
			if content != "" {
				events = append(events, parsedEvent{
					SessionID: sessionID,
					TS:        ts,
					Role:      "tool",
					Content:   content,
					Type:      "tool_use",
					Workdir:   workdir,
				})
			}
		}
	}

	// Combine all text blocks into a single assistant message.
	if combined := strings.TrimSpace(strings.Join(textParts, "\n\n")); combined != "" {
		events = append([]parsedEvent{{
			SessionID: sessionID,
			TS:        ts,
			Role:      "assistant",
			Content:   combined,
			Type:      "message",
			Workdir:   workdir,
		}}, events...)
	}

	return events, nil
}

func parseClaudeSystemMessage(obj map[string]any, sessionID string, ts *int64, workdir string) ([]parsedEvent, error) {
	content := asString(firstByPath(obj, []string{"content"}))
	if content == "" {
		return nil, nil
	}
	return []parsedEvent{{
		SessionID: sessionID,
		TS:        ts,
		Role:      "system",
		Content:   content,
		Type:      "system",
		Workdir:   workdir,
	}}, nil
}

func extractToolResultContent(block map[string]any) string {
	content := block["content"]

	// String content.
	if s, ok := content.(string); ok {
		return strings.TrimSpace(s)
	}

	// Array of content blocks.
	if arr, ok := content.([]any); ok {
		var parts []string
		for _, item := range arr {
			if m, ok := item.(map[string]any); ok {
				if text := asString(firstByPath(m, []string{"text"})); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}

	return ""
}

func formatToolUse(name string, input any) string {
	if name == "" {
		return ""
	}
	inputMap, ok := input.(map[string]any)
	if !ok || len(inputMap) == 0 {
		return name + "()"
	}
	b, err := json.Marshal(inputMap)
	if err != nil {
		return name + "()"
	}
	s := string(b)
	if len(s) > 500 {
		s = s[:497] + "..."
	}
	return name + ": " + s
}

func parseClaudeTimestamp(obj map[string]any) *int64 {
	raw := firstByPath(obj, []string{"timestamp"})
	if raw == nil {
		return nil
	}
	return parseUnix(raw)
}

func claudeSessionIDFromPath(path string) string {
	base := filepath.Base(path)
	if m := claudeSessionFileRe.FindStringSubmatch(base); len(m) == 2 {
		return m[1]
	}
	return "unknown-session"
}

func workdirFromClaudePath(path string) string {
	// Claude stores sessions at ~/.claude/projects/<encoded-path>/<uuid>.jsonl
	// The encoded-path uses dashes instead of path separators:
	// e.g., -Users-eric-projects-foo → /Users/eric/projects/foo
	dir := filepath.Base(filepath.Dir(path))
	if dir == "" || dir == "." || dir == "/" || dir == "projects" {
		return ""
	}
	if !strings.HasPrefix(dir, "-") {
		return ""
	}
	// Replace leading dash and internal dashes with path separators.
	decoded := strings.ReplaceAll(dir, "-", "/")
	if decoded == "" || decoded == "/" {
		return ""
	}
	return filepath.Clean(decoded)
}
