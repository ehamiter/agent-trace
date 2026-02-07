package highlight

import (
	"strings"
	"testing"
)

func TestApplyANSI_CaseInsensitive(t *testing.T) {
	in := "Hello there\nsecond hello\n"
	res := ApplyANSI(in, "hello", func(s string) string { return "[[" + s + "]]" })

	if res.Count != 2 {
		t.Fatalf("expected 2 matches, got %d", res.Count)
	}
	if len(res.LineIndex) != 2 || res.LineIndex[0] != 0 || res.LineIndex[1] != 1 {
		t.Fatalf("unexpected line indexes: %#v", res.LineIndex)
	}
	if !strings.Contains(res.Text, "[[Hello]]") || !strings.Contains(res.Text, "[[hello]]") {
		t.Fatalf("highlight wrapper not applied: %q", res.Text)
	}
}

func TestApplyANSI_PreservesEscapeSequences(t *testing.T) {
	in := "a \x1b[31mhello\x1b[0m b"
	res := ApplyANSI(in, "hello", func(s string) string { return "<" + s + ">" })

	if res.Count != 1 {
		t.Fatalf("expected 1 match, got %d", res.Count)
	}
	if !strings.Contains(res.Text, "\x1b[31m<hello>\x1b[0m") {
		t.Fatalf("expected escaped segment to stay intact, got %q", res.Text)
	}
}

func TestApplyANSI_DoesNotMatchAcrossANSIBoundaries(t *testing.T) {
	in := "he\x1b[31mll\x1b[0mo"
	res := ApplyANSI(in, "hello", func(s string) string { return "<" + s + ">" })
	if res.Count != 0 {
		t.Fatalf("expected 0 matches across ansi boundaries, got %d", res.Count)
	}
}
