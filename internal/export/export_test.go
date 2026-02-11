package export

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agent-trace/internal/index"
)

func TestBuildTranscriptMarkdown_StripsUnstructuredAgentsHeading(t *testing.T) {
	msgs := []index.Message{
		{
			Role:    "user",
			Type:    "message",
			Content: "# AGENTS.md instructions for /tmp/repo\n\nexecute SPECS.md",
		},
		{Role: "assistant", Type: "message", Content: "ok"},
	}

	out := BuildTranscriptMarkdown(msgs, index.TranscriptToggles{}, "")
	if strings.Contains(strings.ToLower(out), "# agents.md instructions for ") {
		t.Fatalf("expected unstructured AGENTS heading to be removed, got:\n%s", out)
	}
	if !strings.Contains(out, "execute SPECS.md") {
		t.Fatalf("expected conversational content to remain, got:\n%s", out)
	}
}

func TestBuildTranscriptMarkdown_StripsUnstructuredAgentsHeadingWithoutHash(t *testing.T) {
	msgs := []index.Message{
		{
			Role:    "user",
			Type:    "message",
			Content: "AGENTS.md instructions for /tmp/repo\n\nexecute SPECS.md",
		},
		{Role: "assistant", Type: "message", Content: "ok"},
	}

	out := BuildTranscriptMarkdown(msgs, index.TranscriptToggles{}, "")
	if strings.Contains(strings.ToLower(out), "agents.md instructions for /tmp/repo") {
		t.Fatalf("expected AGENTS heading line to be removed, got:\n%s", out)
	}
	if !strings.Contains(out, "execute SPECS.md") {
		t.Fatalf("expected conversational content to remain, got:\n%s", out)
	}
}

func TestBuildTranscriptMarkdown_PreservesStructuredAgentsBlock(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	msgs := []index.Message{
		{
			Role: "user",
			Type: "message",
			Content: "# AGENTS.md instructions for " + dir + "\n" +
				"<INSTRUCTIONS>\nkeep me\n</INSTRUCTIONS>",
		},
	}

	out := BuildTranscriptMarkdown(msgs, index.TranscriptToggles{}, "")
	if !strings.Contains(out, "<INSTRUCTIONS>") {
		t.Fatalf("expected structured AGENTS block to be preserved, got:\n%s", out)
	}
}

func TestBuildTranscriptMarkdown_StripsStaleStructuredAgentsBlock(t *testing.T) {
	dir := t.TempDir() // no AGENTS.md file written
	msgs := []index.Message{
		{
			Role: "user",
			Type: "message",
			Content: "# AGENTS.md instructions for " + dir + "\n" +
				"<INSTRUCTIONS>\nkeep me\n</INSTRUCTIONS>\n" +
				"execute SPECS.md",
		},
	}

	out := BuildTranscriptMarkdown(msgs, index.TranscriptToggles{}, "")
	if strings.Contains(strings.ToLower(out), "agents.md instructions for") {
		t.Fatalf("expected stale AGENTS heading to be removed, got:\n%s", out)
	}
	if strings.Contains(out, "<INSTRUCTIONS>") {
		t.Fatalf("expected stale instructions block to be removed, got:\n%s", out)
	}
	if !strings.Contains(out, "execute SPECS.md") {
		t.Fatalf("expected conversational content to remain, got:\n%s", out)
	}
}
