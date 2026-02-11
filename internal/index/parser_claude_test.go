package index

import (
	"strings"
	"testing"
)

func TestParseClaudeUserStringContent(t *testing.T) {
	line := `{"type":"user","sessionId":"abc-123","timestamp":"2026-01-15T10:30:00Z","cwd":"/tmp/proj","message":{"role":"user","content":"hello world"}}`
	events, err := parseClaudeJSONLLine([]byte(line), "/fake/path.jsonl")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	e := events[0]
	if e.SessionID != "abc-123" {
		t.Errorf("sessionID=%q, want abc-123", e.SessionID)
	}
	if e.Role != "user" {
		t.Errorf("role=%q, want user", e.Role)
	}
	if e.Content != "hello world" {
		t.Errorf("content=%q, want 'hello world'", e.Content)
	}
	if e.Type != "message" {
		t.Errorf("type=%q, want message", e.Type)
	}
	if e.Workdir != "/tmp/proj" {
		t.Errorf("workdir=%q, want /tmp/proj", e.Workdir)
	}
	if e.TS == nil {
		t.Fatal("expected non-nil timestamp")
	}
}

func TestParseClaudeAssistantWithToolUse(t *testing.T) {
	line := `{"type":"assistant","sessionId":"s1","timestamp":"2026-01-15T10:31:00Z","cwd":"/tmp","message":{"role":"assistant","content":[{"type":"text","text":"Let me check."},{"type":"tool_use","name":"Read","id":"t1","input":{"file_path":"/tmp/foo.go"}}]}}`
	events, err := parseClaudeJSONLLine([]byte(line), "/fake.jsonl")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	// First event should be assistant text.
	if events[0].Role != "assistant" || events[0].Type != "message" {
		t.Errorf("event[0] role=%q type=%q, want assistant/message", events[0].Role, events[0].Type)
	}
	if events[0].Content != "Let me check." {
		t.Errorf("event[0] content=%q", events[0].Content)
	}
	// Second event should be tool_use.
	if events[1].Role != "tool" || events[1].Type != "tool_use" {
		t.Errorf("event[1] role=%q type=%q, want tool/tool_use", events[1].Role, events[1].Type)
	}
	if !strings.HasPrefix(events[1].Content, "Read:") {
		t.Errorf("event[1] content=%q, should start with 'Read:'", events[1].Content)
	}
}

func TestParseClaudeToolResult(t *testing.T) {
	line := `{"type":"user","sessionId":"s1","timestamp":"2026-01-15T10:32:00Z","cwd":"/tmp","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"file contents here"}]}}`
	events, err := parseClaudeJSONLLine([]byte(line), "/fake.jsonl")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Role != "tool" || events[0].Type != "tool_result" {
		t.Errorf("role=%q type=%q, want tool/tool_result", events[0].Role, events[0].Type)
	}
	if events[0].Content != "file contents here" {
		t.Errorf("content=%q", events[0].Content)
	}
}

func TestParseClaudeSkipsProgress(t *testing.T) {
	line := `{"type":"progress","sessionId":"s1","timestamp":"2026-01-15T10:33:00Z"}`
	events, err := parseClaudeJSONLLine([]byte(line), "/fake.jsonl")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events for progress, got %d", len(events))
	}
}

func TestParseClaudeSkipsFileHistorySnapshot(t *testing.T) {
	line := `{"type":"file-history-snapshot","sessionId":"s1","timestamp":"2026-01-15T10:33:00Z"}`
	events, err := parseClaudeJSONLLine([]byte(line), "/fake.jsonl")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events for file-history-snapshot, got %d", len(events))
	}
}

func TestParseClaudeSystemMessage(t *testing.T) {
	line := `{"type":"system","sessionId":"s1","timestamp":"2026-01-15T10:30:00Z","cwd":"/tmp","content":"system init"}`
	events, err := parseClaudeJSONLLine([]byte(line), "/fake.jsonl")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Role != "system" || events[0].Type != "system" {
		t.Errorf("role=%q type=%q, want system/system", events[0].Role, events[0].Type)
	}
	if events[0].Content != "system init" {
		t.Errorf("content=%q", events[0].Content)
	}
}

func TestClaudeSessionIDFromPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/home/user/.claude/projects/-Users-eric/4256a303-4485-4516-8565-464a3379e0fa.jsonl", "4256a303-4485-4516-8565-464a3379e0fa"},
		{"/some/other/path.jsonl", "unknown-session"},
	}
	for _, tt := range tests {
		got := claudeSessionIDFromPath(tt.path)
		if got != tt.want {
			t.Errorf("claudeSessionIDFromPath(%q)=%q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestWorkdirFromClaudePath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/home/user/.claude/projects/-Users-eric-projects-foo/abc.jsonl", "/Users/eric/projects/foo"},
		{"/home/user/.claude/projects/noprefix/abc.jsonl", ""},
		{"/home/user/.claude/projects/abc.jsonl", ""},
	}
	for _, tt := range tests {
		got := workdirFromClaudePath(tt.path)
		if got != tt.want {
			t.Errorf("workdirFromClaudePath(%q)=%q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestParseClaudeAssistantTextOnly(t *testing.T) {
	line := `{"type":"assistant","sessionId":"s1","timestamp":"2026-01-15T10:31:00Z","cwd":"/tmp","message":{"role":"assistant","content":[{"type":"text","text":"Hello!"},{"type":"text","text":"More text."}]}}`
	events, err := parseClaudeJSONLLine([]byte(line), "/fake.jsonl")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event (combined text), got %d", len(events))
	}
	if events[0].Role != "assistant" {
		t.Errorf("role=%q, want assistant", events[0].Role)
	}
	if !strings.Contains(events[0].Content, "Hello!") || !strings.Contains(events[0].Content, "More text.") {
		t.Errorf("content=%q, expected both text blocks combined", events[0].Content)
	}
}
