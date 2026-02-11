package ui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"agent-trace/internal/index"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

func TestOrderedSessionsModes(t *testing.T) {
	in := []index.Session{
		{ID: "s1", Workdir: "/tmp/beta", LastActivityTS: 20},
		{ID: "s2", Workdir: "/tmp/alpha", LastActivityTS: 10},
		{ID: "s3", Workdir: "/tmp/alpha", LastActivityTS: 30},
		{ID: "s4", Workdir: "", LastActivityTS: 40},
	}

	m := Model{sortOldestFirst: false, groupByWorktree: false}
	got := ids(m.orderedSessions(in))
	want := []string{"s4", "s3", "s1", "s2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("default newest order mismatch: got=%v want=%v", got, want)
	}

	m.sortOldestFirst = true
	got = ids(m.orderedSessions(in))
	want = []string{"s2", "s1", "s3", "s4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("oldest order mismatch: got=%v want=%v", got, want)
	}

	m.groupByWorktree = true
	got = ids(m.orderedSessions(in))
	want = []string{"s2", "s3", "s1", "s4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("grouped+oldest order mismatch: got=%v want=%v", got, want)
	}
}

func TestGroupedModeOrdersGroupsByRecency(t *testing.T) {
	in := []index.Session{
		{ID: "a-old", Workdir: "/tmp/alpha", LastActivityTS: 100},
		{ID: "a-new", Workdir: "/tmp/alpha", LastActivityTS: 200},
		{ID: "z-new", Workdir: "/tmp/zulu", LastActivityTS: 300},
		{ID: "z-old", Workdir: "/tmp/zulu", LastActivityTS: 250},
	}

	m := Model{
		groupByWorktree: true,
		sortOldestFirst: false,
	}
	got := ids(m.orderedSessions(in))
	want := []string{"z-new", "z-old", "a-new", "a-old"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("grouped newest should prioritize most recently active group: got=%v want=%v", got, want)
	}
}

func TestOrderedSessionsPreservesSearchRanking(t *testing.T) {
	in := []index.Session{
		{ID: "a", LastActivityTS: 1},
		{ID: "b", LastActivityTS: 999},
		{ID: "c", LastActivityTS: 5},
	}
	m := Model{
		sortOldestFirst: true,
		groupByWorktree: true,
		searchQuery:     "needle",
	}
	got := ids(m.orderedSessions(in))
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("search ordering should preserve backend ranking: got=%v want=%v", got, want)
	}
}

func TestApplySessions_GroupDividerMarkers(t *testing.T) {
	in := []index.Session{
		{ID: "s1", Workdir: "/tmp/alpha", LastActivityTS: 30},
		{ID: "s2", Workdir: "/tmp/alpha", LastActivityTS: 20},
		{ID: "s3", Workdir: "/tmp/beta", LastActivityTS: 10},
	}

	m := Model{
		groupByWorktree: true,
		list:            list.New([]list.Item{}, list.NewDefaultDelegate(), 40, 20),
	}
	m.applySessions(in)

	items := m.list.Items()
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	i0 := items[0].(sessionItem)
	i1 := items[1].(sessionItem)
	i2 := items[2].(sessionItem)

	if i0.groupDivider {
		t.Fatalf("first grouped item should not have divider")
	}
	if i1.groupDivider {
		t.Fatalf("second item in same group should not have divider")
	}
	if !i2.groupDivider {
		t.Fatalf("first item of next group should have divider")
	}
}

func TestEnterToggleSortResetsSelectionToFirst(t *testing.T) {
	in := []index.Session{
		{ID: "old", Workdir: "/tmp/alpha", LastActivityTS: 10},
		{ID: "mid", Workdir: "/tmp/alpha", LastActivityTS: 20},
		{ID: "new", Workdir: "/tmp/alpha", LastActivityTS: 30},
	}
	m := Model{
		list: list.New([]list.Item{}, list.NewDefaultDelegate(), 40, 20),
		keys: defaultKeys(),
	}
	m.applySessions(in)

	// Move away from the top, then toggle sort.
	m.list.Select(1)
	m.selectedID = m.currentSelectedID()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)

	if !got.sortOldestFirst {
		t.Fatalf("expected oldest-first sort after enter toggle")
	}
	if got.selectedID != "old" {
		t.Fatalf("expected selection reset to first item 'old', got %q", got.selectedID)
	}
	if got.list.Index() != 0 {
		t.Fatalf("expected list index 0, got %d", got.list.Index())
	}
}

func TestToggleGroupingPreservesSelectedSession(t *testing.T) {
	in := []index.Session{
		{ID: "a", Workdir: "/tmp/alpha", LastActivityTS: 30},
		{ID: "b", Workdir: "/tmp/beta", LastActivityTS: 20},
		{ID: "c", Workdir: "/tmp/charlie", LastActivityTS: 10},
	}
	m := Model{
		groupByWorktree: true,
		list:            list.New([]list.Item{}, list.NewDefaultDelegate(), 40, 20),
		keys:            defaultKeys(),
	}
	m.applySessions(in)

	// Select "b" in grouped mode.
	m.list.Select(1)
	m.selectedID = m.currentSelectedID()
	if m.selectedID != "b" {
		t.Fatalf("expected precondition selectedID=b, got %q", m.selectedID)
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	got := updated.(Model)

	if got.groupByWorktree {
		t.Fatalf("expected grouping to toggle off")
	}
	if got.selectedID != "b" {
		t.Fatalf("expected selected session to be preserved, got %q", got.selectedID)
	}
	if got.currentSelectedID() != "b" {
		t.Fatalf("expected list selected item to remain b, got %q", got.currentSelectedID())
	}
}

func ids(in []index.Session) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, s.ID)
	}
	return out
}

func TestCollapseInitialAgentsBlock(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	in := "## You\n# AGENTS.md instructions for " + dir + "\n<INSTRUCTIONS>\nhello\n</INSTRUCTIONS>\n## Codex\nok\n"
	out := collapseInitialAgentsBlock(in)
	if !strings.Contains(out, "AGENTS.md instructions collapsed") {
		t.Fatalf("expected collapsed marker, got: %q", out)
	}
	if strings.Contains(out, "<INSTRUCTIONS>") {
		t.Fatalf("expected instructions body to be removed")
	}
}

func TestCollapseInitialAgentsBlock_NoStructuredBlock(t *testing.T) {
	in := "## You\nI mentioned # AGENTS.md instructions for /tmp/repo in text\n## Codex\nok\n"
	out := collapseInitialAgentsBlock(in)
	if out != in {
		t.Fatalf("expected unchanged markdown when no structured AGENTS block")
	}
}

func TestCollapseInitialAgentsBlock_SkipsWhenAgentsFileMissing(t *testing.T) {
	dir := t.TempDir()
	in := "## You\n# AGENTS.md instructions for " + dir + "\n<INSTRUCTIONS>\nhello\n</INSTRUCTIONS>\n## Codex\nok\n"
	out := collapseInitialAgentsBlock(in)
	if out != in {
		t.Fatalf("expected unchanged markdown when AGENTS.md file does not exist")
	}
}

func TestPrependCollapsedEventsHint(t *testing.T) {
	msgs := []index.Message{
		{Role: "assistant", Type: "message", Content: "hello"},
		{Role: "system", Type: "event_msg", Content: "internal event"},
	}
	md := "## Codex\n\nhello\n"
	out := prependCollapsedEventsHint(md, msgs, index.TranscriptToggles{})
	if !strings.Contains(out, "Events hidden (1)") {
		t.Fatalf("expected events hint, got: %q", out)
	}
}

func TestPrependCollapsedEventsHint_NotShownWhenEnabled(t *testing.T) {
	msgs := []index.Message{
		{Role: "system", Type: "event_msg", Content: "internal event"},
	}
	md := "## Event\n\nx\n"
	out := prependCollapsedEventsHint(md, msgs, index.TranscriptToggles{IncludeEvents: true})
	if out != md {
		t.Fatalf("expected unchanged markdown when events toggle enabled")
	}
}
