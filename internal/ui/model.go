package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"codex-trace/internal/config"
	"codex-trace/internal/export"
	"codex-trace/internal/index"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
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

	indexing       bool
	searchMode     bool
	searchQuery    string
	focusOnList    bool
	includeTools   bool
	includeAborted bool
	includeEvents  bool
	rendering      bool
	renderNonce    int

	selectedID string
	sessions   map[string]index.Session
	messages   map[string][]index.Message
	rendered   map[string]string

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

type sessionItem struct {
	s index.Session
}

func (i sessionItem) Title() string {
	if i.s.Workdir != "" {
		base := filepath.Base(i.s.Workdir)
		if base != "." && base != "/" {
			return base
		}
	}
	return shorten(i.s.ID, 28)
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

		indexing:    true,
		focusOnList: true,
		sessions:    make(map[string]index.Session),
		messages:    make(map[string][]index.Message),
		rendered:    make(map[string]string),
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
			m.viewport.SetContent(msg.rendered)
			m.viewport.GotoTop()
		}

	case tea.KeyMsg:
		if m.searchMode {
			switch msg.String() {
			case "esc":
				m.searchMode = false
				m.searchQuery = ""
				m.search.SetValue("")
				m.search.Blur()
				cmds = append(cmds, m.sessionsCmd(""))
				return m, tea.Batch(cmds...)
			case "enter":
				m.searchMode = false
				m.search.Blur()
				m.searchQuery = strings.TrimSpace(m.search.Value())
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
		case key.Matches(msg, m.keys.ToggleTools):
			m.includeTools = !m.includeTools
			return m, m.renderSelected(true)
		case key.Matches(msg, m.keys.ToggleAborted):
			m.includeAborted = !m.includeAborted
			return m, m.renderSelected(true)
		case key.Matches(msg, m.keys.ToggleEvents):
			m.includeEvents = !m.includeEvents
			return m, m.renderSelected(true)
		case key.Matches(msg, m.keys.Export):
			if m.selectedID != "" {
				cmds = append(cmds, m.exportCmd(m.selectedID))
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
			case "pgup", "b":
				m.viewport.HalfViewUp()
			case "pgdown", "f", "space":
				m.viewport.HalfViewDown()
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
	items := make([]list.Item, 0, len(in))
	m.sessions = make(map[string]index.Session, len(in))
	for _, s := range in {
		m.sessions[s.ID] = s
		items = append(items, sessionItem{s: s})
	}
	m.list.SetItems(items)

	if len(in) == 0 {
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
		for idx, s := range in {
			if s.ID == m.selectedID {
				selectIdx = idx
				break
			}
		}
	}
	m.list.Select(selectIdx)
	m.selectedID = in[selectIdx].ID
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
		return nil
	}

	msgs, ok := m.messages[m.selectedID]
	if !ok {
		m.viewport.SetContent("Loading transcript...")
		return nil
	}

	cacheKey := fmt.Sprintf("%s|w=%d|t=%t|a=%t|e=%t", m.selectedID, m.viewport.Width, m.includeTools, m.includeAborted, m.includeEvents)
	if !force {
		if rendered, ok := m.rendered[cacheKey]; ok {
			m.viewport.SetContent(rendered)
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
	return m.renderTranscriptCmd(sessionID, cacheKey, msgs, toggles, wrap, nonce)
}

func (m Model) renderTranscriptCmd(
	sessionID, cacheKey string,
	msgs []index.Message,
	toggles index.TranscriptToggles,
	wrap int,
	nonce int,
) tea.Cmd {
	return func() tea.Msg {
		md := export.BuildTranscriptMarkdown(msgs, toggles)
		if strings.TrimSpace(md) == "" {
			md = "_No transcript content with current filters._"
		}
		md = sanitizeMarkdownForDisplay(md)

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

func sanitizeMarkdownForDisplay(md string) string {
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

	bodyHeight := m.height - 2
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

	status := m.statusLine()
	left, right := m.paneWidths()
	leftPane := panelStyle(m.focusOnList).Width(left).Height(m.height - 2).Render(m.list.View())
	rightPane := panelStyle(!m.focusOnList).Width(right).Height(m.height - 2).Render(m.viewport.View())
	body := lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)

	helpView := m.help.View(m.keys)
	if m.searchMode {
		helpView = m.search.View() + "  " + helpView
	} else if m.searchQuery != "" {
		helpView = "search: " + m.searchQuery + "  " + helpView
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		status,
		body,
		helpView,
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
	}
	if m.includeTools {
		status += "  [tools]"
	}
	if m.includeAborted {
		status += "  [aborted]"
	}
	if m.includeEvents {
		status += "  [events]"
	}
	if m.rendering {
		status += "  [rendering]"
	}
	if strings.TrimSpace(m.status) != "" {
		status += "  " + shorten(strings.TrimSpace(m.status), 80)
	}
	if m.err != nil {
		status += "  err=" + m.err.Error()
	}
	return statusStyle.Render(status)
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

var (
	statusStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("252")).
		Background(lipgloss.Color("24")).
		Padding(0, 1)
)

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
	Up            key.Binding
	Down          key.Binding
	Tab           key.Binding
	Search        key.Binding
	Esc           key.Binding
	Export        key.Binding
	ToggleTools   key.Binding
	ToggleAborted key.Binding
	ToggleEvents  key.Binding
	Quit          key.Binding
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
		Tab: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "toggle focus"),
		),
		Search: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "search"),
		),
		Esc: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "clear search"),
		),
		Export: key.NewBinding(
			key.WithKeys("e"),
			key.WithHelp("e", "export markdown"),
		),
		ToggleTools: key.NewBinding(
			key.WithKeys("t"),
			key.WithHelp("t", "toggle tools"),
		),
		ToggleAborted: key.NewBinding(
			key.WithKeys("a"),
			key.WithHelp("a", "toggle aborted"),
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
	return []key.Binding{k.Up, k.Down, k.Tab, k.Search, k.Export, k.ToggleTools, k.ToggleAborted, k.ToggleEvents, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Tab, k.Search, k.Esc},
		{k.Export, k.ToggleTools, k.ToggleAborted, k.ToggleEvents, k.Quit},
	}
}
