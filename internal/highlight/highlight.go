package highlight

import (
	"regexp"
	"strings"
)

var ansiCSI = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

type Result struct {
	Text      string
	Count     int
	LineIndex []int
}

func ApplyANSI(input, query string, wrap func(string) string) Result {
	query = strings.TrimSpace(query)
	if query == "" {
		return Result{Text: input}
	}
	if wrap == nil {
		wrap = func(s string) string { return s }
	}

	lines := strings.SplitAfter(input, "\n")
	if len(lines) == 0 {
		lines = []string{input}
	}

	var out strings.Builder
	lineMatches := make([]int, 0, 64)
	total := 0

	for lineNo, line := range lines {
		hasNewline := strings.HasSuffix(line, "\n")
		core := line
		if hasNewline {
			core = strings.TrimSuffix(line, "\n")
		}

		rendered, count := applyToANSIText(core, query, wrap)
		out.WriteString(rendered)
		if hasNewline {
			out.WriteByte('\n')
		}
		if count > 0 {
			lineMatches = append(lineMatches, lineNo)
			total += count
		}
	}

	return Result{
		Text:      out.String(),
		Count:     total,
		LineIndex: lineMatches,
	}
}

func applyToANSIText(s, query string, wrap func(string) string) (string, int) {
	indices := ansiCSI.FindAllStringIndex(s, -1)
	if len(indices) == 0 {
		return applyToPlain(s, query, wrap)
	}

	var out strings.Builder
	total := 0
	pos := 0
	for _, idx := range indices {
		if idx[0] > pos {
			plain, count := applyToPlain(s[pos:idx[0]], query, wrap)
			out.WriteString(plain)
			total += count
		}
		out.WriteString(s[idx[0]:idx[1]])
		pos = idx[1]
	}
	if pos < len(s) {
		plain, count := applyToPlain(s[pos:], query, wrap)
		out.WriteString(plain)
		total += count
	}
	return out.String(), total
}

func applyToPlain(s, query string, wrap func(string) string) (string, int) {
	if s == "" || query == "" {
		return s, 0
	}

	lower := strings.ToLower(s)
	q := strings.ToLower(query)
	if !strings.Contains(lower, q) {
		return s, 0
	}

	var out strings.Builder
	count := 0
	start := 0
	for {
		rel := strings.Index(lower[start:], q)
		if rel < 0 {
			out.WriteString(s[start:])
			break
		}
		idx := start + rel
		out.WriteString(s[start:idx])
		end := idx + len(query)
		out.WriteString(wrap(s[idx:end]))
		count++
		start = end
	}
	return out.String(), count
}
