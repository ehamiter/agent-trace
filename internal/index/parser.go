package index

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var rolloutPathRe = regexp.MustCompile(`sessions[/\\]([^/\\]+)[/\\]rollout-.*\.jsonl$`)
var rolloutFilenameSessionIDRe = regexp.MustCompile(`rollout-.*-([0-9a-fA-F-]{36})\.jsonl$`)

type parsedEvent struct {
	SessionID string
	TS        *int64
	Role      string
	Content   string
	Type      string
	Workdir   string
}

func parseJSONLLine(line []byte, sourcePath string) ([]parsedEvent, error) {
	var obj map[string]any
	if err := json.Unmarshal(line, &obj); err != nil {
		return nil, err
	}

	rootType := asString(firstByPath(obj, []string{"type"}))
	payloadType := asString(firstByPath(obj, []string{"payload", "type"}))
	typ := rootType
	if rootType == "response_item" || rootType == "event_msg" {
		if payloadType != "" {
			typ = payloadType
		}
	} else if typ == "" && payloadType != "" {
		typ = payloadType
	}
	if typ == "event_msg" && payloadType != "" {
		typ = payloadType
	}
	if typ == "" {
		typ = "unknown"
	}

	sessionID := extractSessionID(obj, sourcePath)
	timestamp := extractTimestamp(obj)
	workdir := extractWorkdir(obj)

	role := normalizeRole(asString(firstByPath(obj,
		[]string{"role"},
		[]string{"payload", "role"},
		[]string{"message", "role"},
		[]string{"payload", "message", "role"},
		[]string{"data", "role"},
	)))
	content := extractContent(obj)

	if typ == "message" {
		if role == "" {
			role = "event"
		}
		if content == "" {
			return nil, nil
		}
		return []parsedEvent{{
			SessionID: sessionID,
			TS:        timestamp,
			Role:      role,
			Content:   content,
			Type:      "message",
			Workdir:   workdir,
		}}, nil
	}

	if typ == "user_message" {
		if content == "" {
			return nil, nil
		}
		return []parsedEvent{{
			SessionID: sessionID,
			TS:        timestamp,
			Role:      "user",
			Content:   content,
			Type:      "user_message",
			Workdir:   workdir,
		}}, nil
	}

	if content == "" {
		return nil, nil
	}
	if role == "" {
		if strings.Contains(strings.ToLower(typ), "tool") {
			role = "tool"
		} else {
			role = "event"
		}
	}
	return []parsedEvent{{
		SessionID: sessionID,
		TS:        timestamp,
		Role:      role,
		Content:   content,
		Type:      typ,
		Workdir:   workdir,
	}}, nil
}

func extractSessionID(obj map[string]any, sourcePath string) string {
	for _, path := range [][]string{
		{"session_id"},
		{"sessionId"},
		{"conversation_id"},
		{"conversationId"},
		{"session", "id"},
		{"data", "session_id"},
		{"payload", "session_id"},
		{"payload", "sessionId"},
		{"payload", "conversation_id"},
		{"payload", "conversationId"},
		{"payload", "id"},
	} {
		if s := asString(firstByPath(obj, path)); s != "" {
			return s
		}
	}

	return sessionIDFromPath(sourcePath)
}

func extractWorkdir(obj map[string]any) string {
	for _, path := range [][]string{
		{"workdir"},
		{"cwd"},
		{"payload", "cwd"},
		{"payload", "workdir"},
		{"workspace"},
		{"workspace_path"},
		{"project", "path"},
		{"data", "workdir"},
	} {
		if s := asString(firstByPath(obj, path)); s != "" {
			return s
		}
	}
	return ""
}

func extractTimestamp(obj map[string]any) *int64 {
	for _, path := range [][]string{
		{"timestamp"},
		{"ts"},
		{"time"},
		{"payload", "timestamp"},
		{"payload", "time"},
		{"created_at"},
		{"createdAt"},
		{"message", "timestamp"},
		{"data", "timestamp"},
	} {
		if ts := parseUnix(firstByPath(obj, path)); ts != nil {
			return ts
		}
	}
	return nil
}

func extractContent(obj map[string]any) string {
	for _, path := range [][]string{
		{"content"},
		{"payload", "content"},
		{"message", "content"},
		{"payload", "message", "content"},
		{"data", "content"},
		{"text"},
		{"payload", "text"},
		{"output"},
		{"payload", "output"},
		{"input"},
		{"payload", "input"},
		{"message", "text"},
		{"payload", "message", "text"},
		{"delta", "content"},
		{"payload", "delta", "content"},
		{"payload", "message"},
		{"payload", "arguments"},
		{"payload", "reason"},
	} {
		if text := coerceText(firstByPath(obj, path)); text != "" {
			return text
		}
	}

	for _, path := range [][]string{
		{"tool"},
		{"data"},
		{"message"},
	} {
		if text := coerceText(firstByPath(obj, path)); text != "" {
			return text
		}
	}
	return ""
}

func sessionIDFromPath(sourcePath string) string {
	norm := filepath.ToSlash(sourcePath)
	if matches := rolloutPathRe.FindStringSubmatch(norm); len(matches) == 2 {
		return matches[1]
	}
	if matches := rolloutFilenameSessionIDRe.FindStringSubmatch(filepath.Base(norm)); len(matches) == 2 {
		return matches[1]
	}
	if strings.HasSuffix(norm, "/history.jsonl") {
		return "history"
	}
	if base := filepath.Base(filepath.Dir(sourcePath)); base != "." && base != string(filepath.Separator) {
		return base
	}
	return "unknown-session"
}

func firstByPath(obj map[string]any, path ...[]string) any {
	for _, p := range path {
		var cur any = obj
		ok := true
		for _, seg := range p {
			m, isMap := cur.(map[string]any)
			if !isMap {
				ok = false
				break
			}
			var exists bool
			cur, exists = m[seg]
			if !exists {
				ok = false
				break
			}
		}
		if ok {
			return cur
		}
	}
	return nil
}

func parseUnix(v any) *int64 {
	switch t := v.(type) {
	case nil:
		return nil
	case int64:
		x := t
		if x > 1_000_000_000_000 {
			x /= 1000
		}
		return &x
	case int:
		x := int64(t)
		if x > 1_000_000_000_000 {
			x /= 1000
		}
		return &x
	case float64:
		x := int64(t)
		if x > 1_000_000_000_000 {
			x /= 1000
		}
		return &x
	case json.Number:
		if i, err := t.Int64(); err == nil {
			x := i
			if x > 1_000_000_000_000 {
				x /= 1000
			}
			return &x
		}
	case string:
		t = strings.TrimSpace(t)
		if t == "" {
			return nil
		}
		if i, err := strconv.ParseInt(t, 10, 64); err == nil {
			x := i
			if x > 1_000_000_000_000 {
				x /= 1000
			}
			return &x
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
			if ts, err := time.Parse(layout, t); err == nil {
				x := ts.Unix()
				return &x
			}
		}
	}
	return nil
}

func coerceText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case json.Number:
		return t.String()
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case []any:
		parts := make([]string, 0, len(t))
		for _, item := range t {
			if s := coerceText(item); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	case map[string]any:
		for _, key := range []string{"text", "content", "input", "output", "result", "message", "arguments"} {
			if s := coerceText(t[key]); s != "" {
				return s
			}
		}
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func normalizeRole(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	switch role {
	case "user", "assistant", "tool", "system", "event":
		return role
	default:
		if role == "" {
			return ""
		}
		if strings.Contains(role, "tool") {
			return "tool"
		}
		return role
	}
}
