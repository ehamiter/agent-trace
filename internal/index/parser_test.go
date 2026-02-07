package index

import "testing"

func TestParseJSONLLine_ResponseItemMessage(t *testing.T) {
	line := []byte(`{"timestamp":"2025-11-27T15:23:34.609Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello world"}]}}`)
	path := "/Users/eric/.codex/sessions/2025/11/27/rollout-2025-11-27T09-23-19-019ac5e9-684f-7741-9974-4246554edb05.jsonl"

	events, err := parseJSONLLine(line, path)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	e := events[0]
	if e.Type != "message" {
		t.Fatalf("expected type=message, got %q", e.Type)
	}
	if e.Role != "assistant" {
		t.Fatalf("expected role=assistant, got %q", e.Role)
	}
	if e.Content != "hello world" {
		t.Fatalf("expected content hello world, got %q", e.Content)
	}
	if e.SessionID != "019ac5e9-684f-7741-9974-4246554edb05" {
		t.Fatalf("expected parsed session id, got %q", e.SessionID)
	}
	if e.TS == nil || *e.TS == 0 {
		t.Fatalf("expected parsed timestamp, got %#v", e.TS)
	}
}

func TestParseJSONLLine_EventMsgUserMessage(t *testing.T) {
	line := []byte(`{"timestamp":"2025-11-27T15:23:34.610Z","type":"event_msg","payload":{"type":"user_message","message":"begin phase 4","images":[]}}`)
	path := "/Users/eric/.codex/sessions/2025/11/27/rollout-2025-11-27T09-23-19-019ac5e9-684f-7741-9974-4246554edb05.jsonl"

	events, err := parseJSONLLine(line, path)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	e := events[0]
	if e.Type != "user_message" {
		t.Fatalf("expected type=user_message, got %q", e.Type)
	}
	if e.Role != "user" {
		t.Fatalf("expected role=user, got %q", e.Role)
	}
	if e.Content != "begin phase 4" {
		t.Fatalf("expected content begin phase 4, got %q", e.Content)
	}
}
