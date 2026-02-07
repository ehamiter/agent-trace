package ui

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"codex-trace/internal/clipboard"
	"codex-trace/internal/config"
	"codex-trace/internal/export"
	"codex-trace/internal/highlight"
	"codex-trace/internal/index"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/harmonica"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type Model struct {
	cfg      config.AppConfig
	indexer  *index.Indexer
	exporter *export.Exporter

	list     list.Model
	viewport viewport.Model
	help     help.Model
	spinner  spinner.Model
	search   textinput.Model
	keys     keyMap

	width  int
	height int

	indexing        bool
	searchMode      bool
	searchQuery     string
	focusOnList     bool
	includeTools    bool
	includeAborted  bool
	includeEvents   bool
	collapseAgents  bool
	sortOldestFirst bool
	groupByWorktree bool
	showKeyHelp     bool
	helpAnim        float64
	helpAnimVel     float64
	helpTarget      float64
	helpSpring      harmonica.Spring
	rendering       bool
	renderNonce     int

	selectedID  string
	sessions    map[string]index.Session
	messages    map[string][]index.Message
	rendered    map[string]string
	highlighted map[string]highlight.Result
	matchLines  []int
	matchCount  int
	matchIndex  int

	status string
	err    error
}

type indexDoneMsg struct{ err error }
type sessionsMsg struct {
	sessions []index.Session
	err      error
}
type transcriptMsg struct {
	session index.Session
	msgs    []index.Message
	err     error
}
type exportMsg struct {
	path string
	err  error
}
type renderMsg struct {
	sessionID string
	cacheKey  string
	rendered  string
	nonce     int
	err       error
}
type copyMsg struct {
	err error
}
type helpAnimTickMsg struct{}

type sessionItem struct {
	s            index.Session
	groupDivider bool
}

func (i sessionItem) Title() string {
	prefix := ""
	if i.groupDivider {
		prefix = "┈ "
	}
	if i.s.Workdir != "" {
		base := filepath.Base(i.s.Workdir)
		if base != "." && base != "/" {
			return prefix + base
		}
	}
	return prefix + shorten(i.s.ID, 28)
}

func (i sessionItem) Description() string {
	meta := fmt.Sprintf("last %s | %d msgs", index.FormatUnix(i.s.LastActivityTS), i.s.MessageCount)
	if i.s.Preview == "" {
		return meta
	}
	return meta + " | " + i.s.Preview
}

func (i sessionItem) FilterValue() string {
	return strings.ToLower(i.s.ID + " " + i.s.Preview + " " + i.s.Workdir)
}

func NewModel(cfg config.AppConfig, idx *index.Indexer, exp *export.Exporter) Model {
	l := list.New([]list.Item{}, list.NewDefaultDelegate(), 40, 20)
	l.Title = "Sessions"
	l.SetShowFilter(false)
	l.SetFilteringEnabled(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.DisableQuitKeybindings()

	vp := viewport.New(60, 20)
	vp.SetContent("Indexing Codex history...")

	h := help.New()
	h.ShowAll = false

	sp := spinner.New()
	sp.Spinner = spinner.Points

	ti := textinput.New()
	ti.Placeholder = "Search across sessions..."
	ti.Prompt = "/ "
	ti.CharLimit = 256

	m := Model{
		cfg:      cfg,
		indexer:  idx,
		exporter: exp,
		list:     l,
		viewport: vp,
		help:     h,
		spinner:  sp,
		search:   ti,
		keys:     defaultKeys(),

		indexing:        true,
		focusOnList:     true,
		collapseAgents:  true,
		sortOldestFirst: false,
		groupByWorktree: false,
		sessions:        make(map[string]index.Session),
		messages:        make(map[string][]index.Message),
		rendered:        make(map[string]string),
		highlighted:     make(map[string]highlight.Result),
		matchIndex:      -1,
		helpAnim:        0,
		helpTarget:      0,
		helpSpring:      harmonica.NewSpring(harmonica.FPS(60), 12.5, 0.9),
	}
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.indexCmd())
}

func (m Model) indexCmd() tea.Cmd {
	return func() tea.Msg {
		err := m.indexer.BuildIndex(context.Background())
		return indexDoneMsg{err: err}
	}
}

func (m Model) sessionsCmd(query string) tea.Cmd {
	return func() tea.Msg {
		s, err := m.indexer.ListSessions(query, 500)
		return sessionsMsg{sessions: s, err: err}
	}
}

func (m Model) transcriptCmd(sessionID string) tea.Cmd {
	if sessionID == "" {
		return nil
	}
	return func() tea.Msg {
		s, err := m.indexer.GetSession(sessionID)
		if err != nil {
			return transcriptMsg{err: err}
		}
		msgs, err := m.indexer.GetMessages(sessionID)
		if err != nil {
			return transcriptMsg{err: err}
		}
		return transcriptMsg{session: s, msgs: msgs}
	}
}

func (m Model) exportCmd(sessionID string) tea.Cmd {
	if sessionID == "" {
		return nil
	}
	msgs := m.messages[sessionID]
	session := m.sessions[sessionID]
	toggles := index.TranscriptToggles{
		IncludeTools:   m.includeTools,
		IncludeAborted: m.includeAborted,
		IncludeEvents:  m.includeEvents,
	}

	return func() tea.Msg {
		path, err := m.exporter.Export(session, msgs, toggles)
		return exportMsg{path: path, err: err}
	}
}

func (m Model) copyCmd(sessionID string) tea.Cmd {
	if sessionID == "" {
		return nil
	}
	msgs, ok := m.messages[sessionID]
	if !ok {
		return nil
	}
	session, ok := m.sessions[sessionID]
	if !ok {
		return nil
	}
	toggles := index.TranscriptToggles{
		IncludeTools:   m.includeTools,
		IncludeAborted: m.includeAborted,
		IncludeEvents:  m.includeEvents,
	}

	return func() tea.Msg {
		path, err := m.exporter.Export(session, msgs, toggles)
		if err != nil {
			return copyMsg{err: err}
		}
		snippet := buildPRSnippet(session, msgs, path)

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := clipboard.Copy(ctx, snippet); err != nil {
			return copyMsg{err: err}
		}
		return copyMsg{}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		cmds = append(cmds, m.renderSelected(true))

	case indexDoneMsg:
		m.indexing = false
		m.err = msg.err
		if msg.err != nil {
			m.status = "Indexing failed: " + msg.err.Error()
		} else {
			m.status = "Index ready"
			cmds = append(cmds, m.sessionsCmd(m.searchQuery))
		}

	case sessionsMsg:
		if msg.err != nil {
			m.err = msg.err
			m.status = "Session query failed"
			break
		}
		m.applySessions(msg.sessions)
		if m.selectedID != "" {
			cmds = append(cmds, m.transcriptCmd(m.selectedID))
		}

	case transcriptMsg:
		if msg.err != nil {
			m.err = msg.err
			m.status = "Transcript load failed"
			break
		}
		m.sessions[msg.session.ID] = msg.session
		m.messages[msg.session.ID] = msg.msgs
		if m.selectedID == msg.session.ID {
			cmds = append(cmds, m.renderSelected(true))
		}

	case exportMsg:
		if msg.err != nil {
			m.err = msg.err
			m.status = "Export failed: " + msg.err.Error()
		} else {
			m.status = "Exported: " + msg.path
		}

	case copyMsg:
		if msg.err != nil {
			m.err = msg.err
			if errors.Is(msg.err, clipboard.ErrToolNotFound) {
				m.status = "Could not copy: clipboard tool not found"
			} else {
				m.status = "Could not copy: " + msg.err.Error()
			}
		} else {
			m.status = "Copied PR snippet to clipboard"
		}

	case renderMsg:
		if msg.nonce != m.renderNonce {
			break
		}
		m.rendering = false
		if msg.err != nil {
			m.err = msg.err
			m.status = "Render failed: " + msg.err.Error()
			break
		}
		m.rendered[msg.cacheKey] = msg.rendered
		if m.selectedID == msg.sessionID {
			m.setViewportFromRendered(msg.cacheKey, msg.rendered, true)
		}

	case helpAnimTickMsg:
		if m.stepHelpAnimation() {
			cmds = append(cmds, helpAnimTickCmd())
		}

	case tea.KeyMsg:
		if m.helpOverlayActive() && !key.Matches(msg, m.keys.ToggleHelp) && !key.Matches(msg, m.keys.Quit) {
			return m, nil
		}

		if m.searchMode {
			if key.Matches(msg, m.keys.ToggleHelp) {
				m.toggleHelpOverlay()
				cmds = append(cmds, helpAnimTickCmd())
				return m, tea.Batch(cmds...)
			}
			switch msg.String() {
			case "esc":
				m.searchMode = false
				m.searchQuery = ""
				m.search.SetValue("")
				m.search.Blur()
				m.refreshViewportFromCache()
				cmds = append(cmds, m.sessionsCmd(""))
				return m, tea.Batch(cmds...)
			case "enter":
				m.searchMode = false
				m.search.Blur()
				m.searchQuery = strings.TrimSpace(m.search.Value())
				m.refreshViewportFromCache()
				cmds = append(cmds, m.sessionsCmd(m.searchQuery))
				return m, tea.Batch(cmds...)
			}
			before := m.search.Value()
			var cmd tea.Cmd
			m.search, cmd = m.search.Update(msg)
			cmds = append(cmds, cmd)
			after := strings.TrimSpace(m.search.Value())
			if after != strings.TrimSpace(before) {
				m.searchQuery = after
				m.refreshViewportFromCache()
				cmds = append(cmds, m.sessionsCmd(after))
			}
			return m, tea.Batch(cmds...)
		}

		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Search):
			m.searchMode = true
			m.search.SetValue(m.searchQuery)
			m.search.CursorEnd()
			m.search.Focus()
			return m, nil
		case key.Matches(msg, m.keys.Tab):
			m.focusOnList = !m.focusOnList
			return m, nil
		case key.Matches(msg, m.keys.FocusLeft):
			m.focusOnList = true
			return m, nil
		case key.Matches(msg, m.keys.FocusRight):
			m.focusOnList = false
			return m, nil
		case key.Matches(msg, m.keys.ToggleSort):
			m.sortOldestFirst = !m.sortOldestFirst
			if strings.TrimSpace(m.searchQuery) != "" || m.searchMode {
				m.status = "Sort set to " + m.sortLabel() + " (applies when search is cleared)"
			} else {
				m.selectedID = ""
				m.applySessionsFromMap()
				m.status = "Sort: " + m.sortLabel()
			}
			return m, nil
		case key.Matches(msg, m.keys.ToggleGrouping):
			m.groupByWorktree = !m.groupByWorktree
			if strings.TrimSpace(m.searchQuery) != "" || m.searchMode {
				m.status = "Grouping set to " + m.groupingLabel() + " (applies when search is cleared)"
			} else {
				m.applySessionsFromMap()
				m.status = "Grouping: " + m.groupingLabel()
			}
			return m, nil
		case key.Matches(msg, m.keys.ToggleHelp):
			m.toggleHelpOverlay()
			cmds = append(cmds, helpAnimTickCmd())
			return m, tea.Batch(cmds...)
		case key.Matches(msg, m.keys.PageUp):
			if !m.focusOnList {
				m.viewport.HalfViewUp()
			}
			return m, nil
		case key.Matches(msg, m.keys.PageDown):
			if !m.focusOnList {
				m.viewport.HalfViewDown()
			}
			return m, nil
		case key.Matches(msg, m.keys.PrevPage):
			if !m.focusOnList {
				if strings.TrimSpace(m.searchQuery) != "" && len(m.matchLines) > 0 {
					m.jumpToMatch(-1)
				} else {
					m.viewport.HalfViewUp()
				}
			}
			return m, nil
		case key.Matches(msg, m.keys.NextPage):
			if !m.focusOnList {
				if strings.TrimSpace(m.searchQuery) != "" && len(m.matchLines) > 0 {
					m.jumpToMatch(1)
				} else {
					m.viewport.HalfViewDown()
				}
			}
			return m, nil
		case key.Matches(msg, m.keys.ToggleTools):
			m.includeTools = !m.includeTools
			return m, m.renderSelected(true)
		case key.Matches(msg, m.keys.ToggleAborted):
			m.includeAborted = !m.includeAborted
			return m, m.renderSelected(true)
		case key.Matches(msg, m.keys.ToggleAgents):
			m.collapseAgents = !m.collapseAgents
			return m, m.renderSelected(true)
		case key.Matches(msg, m.keys.ToggleEvents):
			m.includeEvents = !m.includeEvents
			return m, m.renderSelected(true)
		case key.Matches(msg, m.keys.Export):
			if m.selectedID != "" {
				cmds = append(cmds, m.exportCmd(m.selectedID))
			}
			return m, tea.Batch(cmds...)
		case key.Matches(msg, m.keys.Copy):
			if m.selectedID != "" {
				cmds = append(cmds, m.copyCmd(m.selectedID))
			}
			return m, tea.Batch(cmds...)
		}

		if m.focusOnList {
			prev := m.selectedID
			var cmd tea.Cmd
			m.list, cmd = m.list.Update(msg)
			cmds = append(cmds, cmd)
			m.selectedID = m.currentSelectedID()
			if m.selectedID != prev {
				cmds = append(cmds, m.transcriptCmd(m.selectedID))
				cmds = append(cmds, m.renderSelected(false))
			}
		} else {
			switch msg.String() {
			case "up", "k":
				m.viewport.LineUp(1)
			case "down", "j":
				m.viewport.LineDown(1)
			}
		}
	}

	if m.indexing {
		var spin tea.Cmd
		m.spinner, spin = m.spinner.Update(msg)
		cmds = append(cmds, spin)
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) applySessions(in []index.Session) {
	ordered := m.orderedSessions(in)

	items := make([]list.Item, 0, len(in))
	m.sessions = make(map[string]index.Session, len(in))
	prevGroup := ""
	groupedMode := m.groupByWorktree && strings.TrimSpace(m.searchQuery) == "" && !m.searchMode
	for idx, s := range ordered {
		m.sessions[s.ID] = s
		groupDivider := false
		if groupedMode {
			curGroup := sessionGroupKey(s)
			groupDivider = idx > 0 && curGroup != prevGroup
			prevGroup = curGroup
		}
		items = append(items, sessionItem{s: s, groupDivider: groupDivider})
	}
	m.list.SetItems(items)

	if len(ordered) == 0 {
		m.selectedID = ""
		if strings.TrimSpace(m.searchQuery) == "" {
			m.viewport.SetContent("No sessions found.\n\nTip: run with --reindex to force rebuilding from rollout logs.")
		} else {
			m.viewport.SetContent("No sessions matched your search.")
		}
		return
	}

	selectIdx := 0
	if m.selectedID != "" {
		for idx, s := range ordered {
			if s.ID == m.selectedID {
				selectIdx = idx
				break
			}
		}
	}
	m.list.Select(selectIdx)
	m.selectedID = ordered[selectIdx].ID
}

func (m *Model) applySessionsFromMap() {
	if len(m.sessions) == 0 {
		return
	}
	all := make([]index.Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		all = append(all, s)
	}
	m.applySessions(all)
}

func (m Model) orderedSessions(in []index.Session) []index.Session {
	out := make([]index.Session, len(in))
	copy(out, in)

	// Preserve backend relevance ranking while search mode/query is active.
	if strings.TrimSpace(m.searchQuery) != "" || m.searchMode {
		return out
	}

	if m.groupByWorktree {
		groupScore := make(map[string]int64, len(out))
		for _, s := range out {
			g := sessionGroupKey(s)
			ts := s.LastActivityTS
			cur, ok := groupScore[g]
			if !ok {
				groupScore[g] = ts
				continue
			}
			if m.sortOldestFirst {
				if ts < cur {
					groupScore[g] = ts
				}
			} else {
				if ts > cur {
					groupScore[g] = ts
				}
			}
		}

		sort.SliceStable(out, func(i, j int) bool {
			gi := sessionGroupKey(out[i])
			gj := sessionGroupKey(out[j])
			if gi != gj {
				if gi == "~" && gj != "~" {
					return false
				}
				if gj == "~" && gi != "~" {
					return true
				}
				if groupScore[gi] != groupScore[gj] {
					if m.sortOldestFirst {
						return groupScore[gi] < groupScore[gj]
					}
					return groupScore[gi] > groupScore[gj]
				}
				return gi < gj
			}
			if out[i].LastActivityTS != out[j].LastActivityTS {
				if m.sortOldestFirst {
					return out[i].LastActivityTS < out[j].LastActivityTS
				}
				return out[i].LastActivityTS > out[j].LastActivityTS
			}
			return out[i].ID < out[j].ID
		})
		return out
	}

	if m.sortOldestFirst {
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].LastActivityTS != out[j].LastActivityTS {
				return out[i].LastActivityTS < out[j].LastActivityTS
			}
			return out[i].ID < out[j].ID
		})
		return out
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].LastActivityTS != out[j].LastActivityTS {
			return out[i].LastActivityTS > out[j].LastActivityTS
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (m *Model) currentSelectedID() string {
	item, ok := m.list.SelectedItem().(sessionItem)
	if !ok {
		return ""
	}
	return item.s.ID
}

func (m *Model) renderSelected(force bool) tea.Cmd {
	if m.selectedID == "" {
		m.viewport.SetContent("No session selected")
		m.clearMatches()
		return nil
	}

	msgs, ok := m.messages[m.selectedID]
	if !ok {
		m.viewport.SetContent("Loading transcript...")
		m.clearMatches()
		return nil
	}

	cacheKey := m.renderCacheKey(m.selectedID)
	if !force {
		if rendered, ok := m.rendered[cacheKey]; ok {
			m.setViewportFromRendered(cacheKey, rendered, false)
			return nil
		}
	}
	m.rendering = true
	m.renderNonce++
	nonce := m.renderNonce
	m.viewport.SetContent("Rendering transcript...")
	toggles := index.TranscriptToggles{
		IncludeTools:   m.includeTools,
		IncludeAborted: m.includeAborted,
		IncludeEvents:  m.includeEvents,
	}
	wrap := m.viewport.Width - 2
	if wrap < 20 {
		wrap = 20
	}
	sessionID := m.selectedID
	return m.renderTranscriptCmd(sessionID, cacheKey, msgs, toggles, m.collapseAgents, wrap, nonce)
}

func (m Model) renderTranscriptCmd(
	sessionID, cacheKey string,
	msgs []index.Message,
	toggles index.TranscriptToggles,
	collapseAgents bool,
	wrap int,
	nonce int,
) tea.Cmd {
	return func() tea.Msg {
		filtered := index.FilterMessages(msgs, toggles)
		md := export.BuildTranscriptMarkdown(msgs, toggles)
		if strings.TrimSpace(md) == "" {
			if hasOnlyBoilerplateConversation(msgs) {
				md = "_Session contains only environment/turn boilerplate and no conversational turns._"
			} else if len(filtered) == 0 {
				md = "_No transcript content with current filters._"
			}
		}
		md = sanitizeMarkdownForDisplay(md, collapseAgents)

		if len(md) > 500_000 {
			return renderMsg{
				sessionID: sessionID,
				cacheKey:  cacheKey,
				rendered:  md,
				nonce:     nonce,
			}
		}

		rendered := md
		r, err := glamour.NewTermRenderer(
			glamour.WithStandardStyle(config.DefaultGlamourStyle),
			glamour.WithWordWrap(wrap),
		)
		if err != nil {
			return renderMsg{
				sessionID: sessionID,
				cacheKey:  cacheKey,
				rendered:  md,
				nonce:     nonce,
			}
		}
		if out, renderErr := r.Render(md); renderErr == nil {
			rendered = out
		}
		return renderMsg{
			sessionID: sessionID,
			cacheKey:  cacheKey,
			rendered:  rendered,
			nonce:     nonce,
		}
	}
}

func (m Model) renderCacheKey(sessionID string) string {
	return fmt.Sprintf(
		"%s|w=%d|t=%t|a=%t|e=%t|ag=%t",
		sessionID,
		m.viewport.Width,
		m.includeTools,
		m.includeAborted,
		m.includeEvents,
		m.collapseAgents,
	)
}

func (m Model) highlightCacheKey(cacheKey, query string) string {
	return cacheKey + "|q=" + strings.ToLower(strings.TrimSpace(query))
}

func (m *Model) refreshViewportFromCache() {
	if m.selectedID == "" {
		m.clearMatches()
		return
	}
	cacheKey := m.renderCacheKey(m.selectedID)
	rendered, ok := m.rendered[cacheKey]
	if !ok {
		return
	}
	oldOffset := m.viewport.YOffset
	m.setViewportFromRendered(cacheKey, rendered, false)
	m.viewport.SetYOffset(m.clampViewportOffset(oldOffset))
}

func (m *Model) setViewportFromRendered(cacheKey, rendered string, gotoTop bool) {
	content := rendered
	query := strings.TrimSpace(m.searchQuery)
	if query != "" {
		hKey := m.highlightCacheKey(cacheKey, query)
		res, ok := m.highlighted[hKey]
		if !ok {
			res = highlight.ApplyANSI(rendered, query, func(s string) string {
				return searchMatchStyle.Render(s)
			})
			m.highlighted[hKey] = res
		}
		content = res.Text
		m.setMatchMeta(res)
	} else {
		m.clearMatches()
	}

	m.viewport.SetContent(content)
	if gotoTop {
		m.viewport.GotoTop()
		if len(m.matchLines) > 0 {
			m.matchIndex = 0
			m.viewport.SetYOffset(m.clampViewportOffset(m.matchLines[0]))
		}
	}
}

func (m *Model) setMatchMeta(res highlight.Result) {
	if res.Count == 0 || len(res.LineIndex) == 0 {
		m.clearMatches()
		return
	}
	m.matchCount = res.Count
	m.matchLines = append(m.matchLines[:0], res.LineIndex...)
	if m.matchIndex < 0 || m.matchIndex >= len(m.matchLines) {
		m.matchIndex = 0
	}
}

func (m *Model) clearMatches() {
	m.matchLines = nil
	m.matchCount = 0
	m.matchIndex = -1
}

func (m *Model) jumpToMatch(delta int) {
	if len(m.matchLines) == 0 {
		m.status = "No search matches in transcript"
		return
	}

	if m.matchIndex < 0 || m.matchIndex >= len(m.matchLines) {
		m.matchIndex = 0
	} else if delta > 0 {
		m.matchIndex = (m.matchIndex + 1) % len(m.matchLines)
	} else if delta < 0 {
		m.matchIndex = (m.matchIndex - 1 + len(m.matchLines)) % len(m.matchLines)
	}

	line := m.matchLines[m.matchIndex]
	m.viewport.SetYOffset(m.clampViewportOffset(line))
	m.status = fmt.Sprintf("Match %d/%d", m.matchIndex+1, m.matchCount)
}

func (m *Model) clampViewportOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	maxOffset := m.viewport.TotalLineCount() - m.viewport.Height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if offset > maxOffset {
		return maxOffset
	}
	return offset
}

func hasOnlyBoilerplateConversation(msgs []index.Message) bool {
	hasCanonical := false
	for _, m := range msgs {
		if m.Type != "message" || (m.Role != "user" && m.Role != "assistant") {
			continue
		}
		hasCanonical = true
		if m.Role == "assistant" {
			return false
		}
		if !isLikelyEnvironmentBoilerplate(m.Content) {
			return false
		}
	}
	return hasCanonical
}

func isLikelyEnvironmentBoilerplate(content string) bool {
	c := strings.ToLower(strings.TrimSpace(content))
	if c == "" {
		return true
	}
	if strings.HasPrefix(c, "<environment_context>") {
		return true
	}
	if strings.HasPrefix(c, "<turn_aborted>") {
		return true
	}
	return strings.Contains(c, "<environment_context>") && strings.Contains(c, "<cwd>")
}

func sanitizeMarkdownForDisplay(md string, collapseAgents bool) string {
	if collapseAgents {
		md = collapseInitialAgentsBlock(md)
	}
	md = stripEmbeddedImageData(md)
	md = clampLongLines(md, 8000)
	const maxDisplayChars = 1_000_000
	if len(md) <= maxDisplayChars {
		return md
	}
	trimmed := md[:maxDisplayChars]
	trimmed = strings.TrimRight(trimmed, "\n")
	return trimmed + "\n\n... [transcript truncated for display; use export for full content] ...\n"
}

func collapseInitialAgentsBlock(md string) string {
	marker := "# AGENTS.md instructions for "
	start := strings.Index(md, marker)
	if start < 0 {
		return md
	}

	end := -1
	if closeIdx := strings.Index(md[start:], "</INSTRUCTIONS>"); closeIdx >= 0 {
		end = start + closeIdx + len("</INSTRUCTIONS>")
	}
	if end < 0 {
		if nextSectionIdx := strings.Index(md[start:], "\n## "); nextSectionIdx >= 0 {
			end = start + nextSectionIdx
		}
	}
	if end < 0 {
		end = len(md)
	}

	replacement := "\n> [AGENTS.md instructions collapsed. Press `a` to expand.]\n"
	return md[:start] + replacement + md[end:]
}

func stripEmbeddedImageData(s string) string {
	var b strings.Builder
	pos := 0
	for {
		i := strings.Index(s[pos:], "data:image/")
		if i < 0 {
			b.WriteString(s[pos:])
			break
		}
		start := pos + i
		b.WriteString(s[pos:start])

		rest := s[start:]
		base64MarkerIdx := strings.Index(rest, ";base64,")
		if base64MarkerIdx < 0 {
			b.WriteString("data:image/")
			pos = start + len("data:image/")
			continue
		}

		payloadStart := start + base64MarkerIdx + len(";base64,")
		j := payloadStart
		for j < len(s) && isBase64Byte(s[j]) {
			j++
		}
		payloadLen := j - payloadStart

		b.WriteString("[embedded image data omitted: ")
		b.WriteString(strconv.Itoa(payloadLen))
		b.WriteString(" base64 chars]")
		pos = j
	}
	return b.String()
}

func isBase64Byte(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z':
		return true
	case c >= 'a' && c <= 'z':
		return true
	case c >= '0' && c <= '9':
		return true
	case c == '+' || c == '/' || c == '=' || c == '\n' || c == '\r':
		return true
	default:
		return false
	}
}

func clampLongLines(s string, max int) string {
	if max <= 0 || len(s) == 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if len(line) <= max {
			continue
		}
		head := line[:max/2]
		tail := line[len(line)-max/2:]
		lines[i] = head + "... [line truncated " + strconv.Itoa(len(line)-max) + " chars] ..." + tail
	}
	return strings.Join(lines, "\n")
}

func (m *Model) resize() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	left, right := m.paneWidths()

	bodyHeight := m.height - 1
	if bodyHeight < 8 {
		bodyHeight = 8
	}

	m.list.SetSize(left-2, bodyHeight-2)
	m.viewport.Width = right - 2
	m.viewport.Height = bodyHeight - 2
}

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Starting..."
	}

	bodyHeight := m.height - 1
	if bodyHeight < 8 {
		bodyHeight = 8
	}

	left, right := m.paneWidths()
	leftPane := panelStyle(m.focusOnList).Width(left).Height(bodyHeight).Render(m.list.View())
	rightContent := m.viewport.View()
	rightPane := panelStyle(!m.focusOnList).Width(right).Height(bodyHeight).Render(rightContent)
	body := lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)
	if m.helpOverlayActive() {
		anim := clamp01(m.helpAnim)
		modal := m.shortcutsView(m.width-8, bodyHeight-4, anim)
		yOffset := int((1 - anim) * 3)
		body = backdropStyle.Render(body)
		body = overlayModalCentered(body, modal, m.width, bodyHeight, yOffset)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		body,
		m.statusLine(),
	)
}

func (m Model) statusLine() string {
	status := ""
	if m.indexing {
		status = m.spinner.View() + " indexing..."
	}
	if m.selectedID != "" {
		s := m.sessions[m.selectedID]
		status = fmt.Sprintf(
			"session=%s  messages=%d  last=%s  source=%s",
			shorten(s.ID, 18),
			s.MessageCount,
			index.FormatUnix(s.LastActivityTS),
			s.Source,
		)
	}
	if m.searchQuery != "" || m.searchMode {
		status += "  [search]"
		queryText := strings.TrimSpace(m.searchQuery)
		if m.searchMode {
			queryText = strings.TrimSpace(m.search.Value())
		}
		if queryText != "" {
			status += "  q=" + shorten(queryText, 40)
		}
		if strings.TrimSpace(m.searchQuery) != "" {
			if m.matchCount > 0 {
				cur := m.matchIndex + 1
				if cur < 1 {
					cur = 1
				}
				status += fmt.Sprintf("  [match %d/%d]", cur, m.matchCount)
			} else {
				status += "  [match 0]"
			}
		}
	}
	if strings.TrimSpace(m.searchQuery) == "" && !m.searchMode {
		status += "  [sort: " + m.sortLabel() + "]"
		status += "  [group: " + m.groupingLabel() + "]"
	} else {
		status += "  [order: relevance]"
	}
	if m.includeTools {
		status += "  [tools]"
	}
	if m.includeAborted {
		status += "  [aborted]"
	}
	if m.collapseAgents {
		status += "  [agents-collapsed]"
	}
	if m.includeEvents {
		status += "  [events]"
	}
	if m.rendering {
		status += "  [rendering]"
	}
	if m.helpOverlayActive() {
		status += "  [? shortcuts]"
	}
	if m.searchMode {
		status += "  " + m.search.View()
	}
	if strings.TrimSpace(m.status) != "" {
		status += "  " + shorten(strings.TrimSpace(m.status), 80)
	}
	if m.err != nil {
		status += "  err=" + m.err.Error()
	}
	return statusStyle.Render(status)
}

func (m Model) shortcutsView(maxWidth, maxHeight int, anim float64) string {
	if maxWidth < 44 {
		maxWidth = 44
	}
	if maxHeight < 10 {
		maxHeight = 10
	}

	targetW := minInt(maxWidth, 84)
	targetH := minInt(maxHeight, 22)
	width := int(float64(targetW) * (0.82 + 0.18*anim))
	height := int(float64(targetH) * (0.74 + 0.26*anim))
	if width < 44 {
		width = 44
	}
	if height < 10 {
		height = 10
	}

	helpModel := m.help
	helpModel.ShowAll = true
	body := helpModel.View(m.keys)
	header := shortcutsTitleStyle.Render("Keyboard Shortcuts  (? to close)")
	content := lipgloss.NewStyle().
		Width(width - 4).
		MaxHeight(height - 4).
		Render(lipgloss.JoinVertical(lipgloss.Left, header, "", body))

	return shortcutsModalStyle(anim).
		Width(width).
		Height(height).
		Render(content)
}

func (m *Model) toggleHelpOverlay() {
	if m.helpTarget > 0.5 {
		m.helpTarget = 0
		return
	}
	m.showKeyHelp = true
	m.helpTarget = 1
}

func (m *Model) stepHelpAnimation() bool {
	if !m.helpOverlayActive() && m.helpTarget == 0 {
		return false
	}

	m.helpAnim, m.helpAnimVel = m.helpSpring.Update(m.helpAnim, m.helpAnimVel, m.helpTarget)
	if m.helpAnim < -0.02 {
		m.helpAnim = -0.02
		m.helpAnimVel = 0
	} else if m.helpAnim > 1.02 {
		m.helpAnim = 1.02
	}

	if math.Abs(m.helpAnim-m.helpTarget) < 0.006 && math.Abs(m.helpAnimVel) < 0.006 {
		m.helpAnim = m.helpTarget
		m.helpAnimVel = 0
	}

	if m.helpAnim <= 0.001 && m.helpTarget == 0 {
		m.showKeyHelp = false
		return false
	}
	m.showKeyHelp = true
	return math.Abs(m.helpAnim-m.helpTarget) >= 0.004 || math.Abs(m.helpAnimVel) >= 0.004
}

func (m Model) helpOverlayActive() bool {
	return m.showKeyHelp || m.helpAnim > 0.001 || m.helpTarget > 0.001
}

func helpAnimTickCmd() tea.Cmd {
	return tea.Tick(16*time.Millisecond, func(time.Time) tea.Msg {
		return helpAnimTickMsg{}
	})
}

func overlayModalCentered(base, modal string, width, height, yOffset int) string {
	baseLines := normalizeCanvasLines(base, width, height)
	if len(baseLines) == 0 {
		baseLines = make([]string, height)
		for i := range baseLines {
			baseLines[i] = strings.Repeat(" ", maxInt(width, 0))
		}
	}

	modalLayer := modal
	if yOffset > 0 {
		modalLayer = lipgloss.NewStyle().MarginTop(yOffset).Render(modalLayer)
	}
	layer := lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modalLayer)
	layerLines := normalizeCanvasLines(layer, width, height)

	out := make([]string, height)
	for i := 0; i < height; i++ {
		if strings.TrimSpace(ansi.Strip(layerLines[i])) == "" {
			out[i] = baseLines[i]
			continue
		}
		out[i] = layerLines[i]
	}
	return strings.Join(out, "\n")
}

func normalizeCanvasLines(s string, width, height int) []string {
	if height <= 0 {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) < height {
		pad := make([]string, height-len(lines))
		lines = append(lines, pad...)
	} else if len(lines) > height {
		lines = lines[:height]
	}
	for i := range lines {
		lines[i] = padToWidth(lines[i], width)
	}
	return lines
}

func padToWidth(s string, width int) string {
	if width <= 0 {
		return ""
	}
	s = ansi.Truncate(s, width, "")
	w := ansi.StringWidth(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (m *Model) paneWidths() (int, int) {
	left := m.width / 3
	if left < 32 {
		left = 32
	}
	if left > m.width-32 {
		left = m.width - 32
	}
	if left < 20 {
		left = 20
	}
	right := m.width - left - 1
	if right < 20 {
		right = 20
	}
	return left, right
}

func shorten(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

func sessionGroupKey(s index.Session) string {
	wd := strings.TrimSpace(s.Workdir)
	if wd == "" {
		return "~"
	}
	base := filepath.Base(filepath.Clean(wd))
	if base == "" || base == "." || base == "/" {
		return "~"
	}
	return strings.ToLower(base)
}

func (m Model) sortLabel() string {
	if m.sortOldestFirst {
		return "oldest first"
	}
	return "newest first"
}

func (m Model) groupingLabel() string {
	if m.groupByWorktree {
		return "worktree"
	}
	return "flat"
}

func buildPRSnippet(session index.Session, msgs []index.Message, exportPath string) string {
	var b strings.Builder
	b.WriteString("### Codex transcript\n\n")
	b.WriteString("- Session: `" + strings.TrimSpace(session.ID) + "`\n")
	b.WriteString("- Export: `" + snippetExportPath(exportPath) + "`\n")
	b.WriteString("- Notes: " + snippetNotes(session, msgs) + "\n")
	return b.String()
}

func snippetExportPath(path string) string {
	clean := filepath.ToSlash(filepath.Clean(path))
	if idx := strings.Index(clean, "/docs/codex/"); idx >= 0 {
		return clean[idx+1:]
	}
	wd, err := os.Getwd()
	if err != nil {
		return clean
	}
	rel, err := filepath.Rel(wd, path)
	if err != nil {
		return clean
	}
	rel = filepath.ToSlash(rel)
	if strings.HasPrefix(rel, "../") {
		return clean
	}
	return rel
}

func snippetNotes(session index.Session, msgs []index.Message) string {
	note := strings.TrimSpace(session.Preview)
	if note == "" {
		for _, msg := range msgs {
			if strings.ToLower(strings.TrimSpace(msg.Role)) != "user" {
				continue
			}
			lower := strings.ToLower(strings.TrimSpace(msg.Content))
			if isLikelyEnvironmentBoilerplate(msg.Content) || strings.HasPrefix(lower, "# agents.md instructions for ") {
				continue
			}
			note = strings.TrimSpace(msg.Content)
			if note != "" {
				break
			}
		}
	}
	if note == "" {
		return "n/a"
	}
	note = strings.Join(strings.Fields(note), " ")
	return shorten(note, 120)
}

var (
	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Background(lipgloss.Color("24")).
			Padding(0, 1)
	backdropStyle = lipgloss.NewStyle().
			Faint(true)
	shortcutsTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("212")).
				Bold(true)
	searchMatchStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("16")).
				Background(lipgloss.Color("220"))
)

func shortcutsModalStyle(anim float64) lipgloss.Style {
	borderColor := lipgloss.Color("99")
	if anim > 0.66 {
		borderColor = lipgloss.Color("212")
	} else if anim > 0.33 {
		borderColor = lipgloss.Color("141")
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Background(lipgloss.Color("235")).
		Foreground(lipgloss.Color("252")).
		Padding(1, 1)
}

func panelStyle(active bool) lipgloss.Style {
	border := lipgloss.NormalBorder()
	if active {
		return lipgloss.NewStyle().
			Border(border, true).
			BorderForeground(lipgloss.Color("39")).
			Padding(0, 1)
	}
	return lipgloss.NewStyle().
		Border(border, true).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1)
}

type keyMap struct {
	Up             key.Binding
	Down           key.Binding
	FocusLeft      key.Binding
	FocusRight     key.Binding
	Tab            key.Binding
	ToggleSort     key.Binding
	ToggleGrouping key.Binding
	PageUp         key.Binding
	PageDown       key.Binding
	PrevPage       key.Binding
	NextPage       key.Binding
	Search         key.Binding
	Esc            key.Binding
	ToggleHelp     key.Binding
	Export         key.Binding
	Copy           key.Binding
	ToggleTools    key.Binding
	ToggleAborted  key.Binding
	ToggleAgents   key.Binding
	ToggleEvents   key.Binding
	Quit           key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓/j", "down"),
		),
		FocusLeft: key.NewBinding(
			key.WithKeys("left"),
			key.WithHelp("←", "focus list"),
		),
		FocusRight: key.NewBinding(
			key.WithKeys("right"),
			key.WithHelp("→", "focus transcript"),
		),
		Tab: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "toggle focus"),
		),
		ToggleSort: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "toggle sort"),
		),
		ToggleGrouping: key.NewBinding(
			key.WithKeys("w"),
			key.WithHelp("w", "toggle grouping"),
		),
		PageUp: key.NewBinding(
			key.WithKeys("pgup", "b"),
			key.WithHelp("pgup", "page up"),
		),
		PageDown: key.NewBinding(
			key.WithKeys("pgdown", "f"),
			key.WithHelp("pgdn", "page down"),
		),
		PrevPage: key.NewBinding(
			key.WithKeys("p"),
			key.WithHelp("p", "prev match/page"),
		),
		NextPage: key.NewBinding(
			key.WithKeys("n"),
			key.WithHelp("n", "next match/page"),
		),
		Search: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "search"),
		),
		Esc: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "clear search"),
		),
		ToggleHelp: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "toggle shortcuts"),
		),
		Export: key.NewBinding(
			key.WithKeys("e"),
			key.WithHelp("e", "export markdown"),
		),
		Copy: key.NewBinding(
			key.WithKeys("c"),
			key.WithHelp("c", "copy PR snippet"),
		),
		ToggleTools: key.NewBinding(
			key.WithKeys("t"),
			key.WithHelp("t", "toggle tools"),
		),
		ToggleAborted: key.NewBinding(
			key.WithKeys("u"),
			key.WithHelp("u", "toggle aborted"),
		),
		ToggleAgents: key.NewBinding(
			key.WithKeys("a"),
			key.WithHelp("a", "agents expand/collapse"),
		),
		ToggleEvents: key.NewBinding(
			key.WithKeys("v"),
			key.WithHelp("v", "toggle events"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
	}
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.FocusLeft, k.FocusRight, k.Tab, k.ToggleSort, k.ToggleGrouping, k.Search, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.FocusLeft, k.FocusRight, k.Tab, k.ToggleSort, k.ToggleGrouping},
		{k.PageDown, k.PageUp, k.NextPage, k.PrevPage, k.Search, k.Esc, k.ToggleHelp},
		{k.Export, k.Copy, k.ToggleTools, k.ToggleAborted, k.ToggleAgents, k.ToggleEvents, k.Quit},
	}
}
