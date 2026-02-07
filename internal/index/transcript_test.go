package index

import "testing"

func TestFilterMessages_SkipsPreambleUserMessages(t *testing.T) {
	msgs := []Message{
		{Role: "user", Type: "message", Content: "<environment_context> /Users/eric/dev/app zsh </environment_context>"},
		{Role: "user", Type: "message", Content: "real user question"},
		{Role: "assistant", Type: "message", Content: "real answer"},
	}

	out := FilterMessages(msgs, TranscriptToggles{})
	if len(out) != 2 {
		t.Fatalf("expected 2 messages after preamble filtering, got %d", len(out))
	}
	if out[0].Content != "real user question" {
		t.Fatalf("unexpected first message: %q", out[0].Content)
	}
}

func TestIsPreambleUserContent(t *testing.T) {
	cases := []struct {
		content string
		want    bool
	}{
		{"# AGENTS.md instructions for /path", false},
		{"<turn_aborted>... </turn_aborted>", true},
		{"<environment_context> /Users/x zsh </environment_context>", true},
		{"normal message", false},
	}
	for _, tc := range cases {
		got := isBoilerplateUserContent(tc.content)
		if got != tc.want {
			t.Fatalf("content=%q got=%v want=%v", tc.content, got, tc.want)
		}
	}
}

func TestIsNonConversationalPreviewContent(t *testing.T) {
	cases := []struct {
		content string
		want    bool
	}{
		{"# AGENTS.md instructions for /path", true},
		{"<environment_context> /Users/x zsh </environment_context>", true},
		{"normal message", false},
	}
	for _, tc := range cases {
		got := isNonConversationalPreviewContent(tc.content)
		if got != tc.want {
			t.Fatalf("content=%q got=%v want=%v", tc.content, got, tc.want)
		}
	}
}
