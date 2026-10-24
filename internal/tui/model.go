// Package tui is the native session picker (bubbletea): a three-panel
// dashboard — top bar with provider tabs, collapsible workspace sidebar,
// full-height session list, and a detail panel.
//
// The picker never execs: it quits with a selection recorded, and the CLI
// performs the exec after the terminal is restored.
package tui

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/sahilm/fuzzy"

	"github.com/QuietSugar/reup/internal/brand"
	"github.com/QuietSugar/reup/internal/provider"
	"github.com/QuietSugar/reup/internal/session"
	"github.com/QuietSugar/reup/internal/workspace"
)

type focus int

const (
	focusList focus = iota
	focusSidebar
)

type Model struct {
	filters   session.Filters
	providers []provider.Provider
	all       []session.Session

	tabs   []string // "" = all sources, then enabled provider names
	tabIdx int

	width, height int
	ready, sized  bool

	sidebarOn, previewOn bool
	focus                focus
	sideCur              int
	wsFilter             string // group key of the workspace the sidebar filters by

	grouped bool // group-mode rows (default: on; g toggles the flat list)

	cursor, offset int
	filtering      bool
	filterInput    textinput.Model
	filterValue    string

	preview viewport.Model
	detail  viewport.Model // full-detail popup content
	spin    spinner.Model

	lastV time.Time // double-tap detection for the full-detail popup

	// Idle exit: each scheduled timer carries the idleID generation that
	// created it; user input bumps the generation, so a superseded timer
	// firing later is ignored. timedOut marks the quit as an idle timeout so
	// Pick can say why the picker disappeared.
	idleID   int
	timedOut bool

	modal      modalKind
	provCur    int
	provTemp   map[string]bool
	mutator    provider.SessionMutator
	mutStarter provider.ServerStarter
	mutTarget  *session.Session
	mutActions []string // "archive" (native) or "hide" (reup-side)
	mutCur     int
	mutAvail   bool
	mutCheck   error // per-session gate: agent may not manage this session
	mutHide    bool  // reup-side hide (no mutator for this provider)
	mutBusy    bool

	pending, generation int
	loaded, noSessions  bool
	warnings            []string

	rows       []row
	selectable []int
	groups     []workspace.Group
	tabCounts  map[string]int

	selKey   string
	selected *session.Session

	// click double-tap detection (list rows, sidebar agents)
	lastClickAt  time.Time
	lastClickRow int
	lastClickKey string
}

// NewModel builds the picker over the given providers (already filtered to
// active / requested-source ones by the caller).
func NewModel(providers []provider.Provider, filters session.Filters) Model {
	ti := textinput.New()
	ti.Placeholder = "filter sessions…"
	ti.Prompt = "/"

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	m := Model{
		filters:     filters,
		providers:   providers,
		grouped:     true,
		tabs:        []string{""},
		filterInput: ti,
		preview:     viewport.New(0, 0),
		spin:        sp,
		provTemp:    map[string]bool{},
		pending:     len(providers),
	}
	for _, p := range providers {
		m.tabs = append(m.tabs, p.Name())
	}
	return m
}

// Selected returns the chosen session (nil on cancel).
func (m Model) Selected() *session.Session { return m.selected }

// NoSessions reports whether loading finished with zero sessions.
func (m Model) NoSessions() bool { return m.noSessions }

// --- data pipeline ---------------------------------------------------------

// tabSessions returns the sessions of the active provider tab (before any
// sidebar/text filtering).
func (m Model) tabSessions() []session.Session {
	src := ""
	if m.tabIdx > 0 && m.tabIdx < len(m.tabs) {
		src = m.tabs[m.tabIdx]
	}
	var out []session.Session
	for _, s := range m.all {
		if src == "" || s.Source == src {
			out = append(out, s)
		}
	}
	return out
}

// applyTextFilter narrows ss by the fuzzy filter value, preserving order.
func applyTextFilter(ss []session.Session, query string) []session.Session {
	query = strings.TrimSpace(query)
	if query == "" {
		return ss
	}
	targets := make([]string, len(ss))
	for i := range ss {
		targets[i] = filterValue(&ss[i])
	}
	matches := fuzzy.Find(query, targets)
	idx := make([]int, 0, len(matches))
	for _, mt := range matches {
		idx = append(idx, mt.Index)
	}
	sort.Ints(idx)
	out := make([]session.Session, 0, len(idx))
	for _, i := range idx {
		out = append(out, ss[i])
	}
	return out
}

// rebuild recomputes groups, rows, and the cursor from the current state.
// It is the single funnel every mutation goes through, so grouping, the
// sidebar, the tabs, and the selection can never disagree.
func (m *Model) rebuild() {
	base := m.tabSessions()
	m.groups = workspace.GroupBy(base)

	m.tabCounts = map[string]int{"": len(m.all)}
	for _, s := range m.all {
		m.tabCounts[s.Source]++
	}

	if m.wsFilter != "" {
		var f []session.Session
		for _, s := range base {
			if workspace.Key(&s) == m.wsFilter {
				f = append(f, s)
			}
		}
		base = f
	}
	base = applyTextFilter(base, m.filterValue)
	provider.SortSessions(base, false)

	m.rows = make([]row, 0, len(base)+1)
	m.selectable = m.selectable[:0]
	if m.grouped {
		for _, g := range workspace.GroupBy(base) {
			m.rows = append(m.rows, row{kind: rowGroup, g: g})
			for _, s := range g.Sessions {
				m.selectable = append(m.selectable, len(m.rows))
				m.rows = append(m.rows, row{kind: rowSession, s: s})
			}
		}
	} else {
		for _, s := range base {
			m.selectable = append(m.selectable, len(m.rows))
			m.rows = append(m.rows, row{kind: rowSession, s: s})
		}
	}
	m.restoreSelection()
	m.refreshPreview()
}

// restoreSelection puts the cursor back on the previously selected session
// (matched by identity), else on the first session row.
func (m *Model) restoreSelection() {
	if m.selKey != "" {
		for i, r := range m.rows {
			if r.kind == rowSession && sessionKey(&r.s) == m.selKey {
				m.cursor = i
				m.clampOffset()
				return
			}
		}
	}
	if len(m.selectable) > 0 {
		m.cursor = m.selectable[0]
	} else {
		m.cursor = 0
	}
	m.clampOffset()
}

func (m *Model) rememberSelection() {
	if m.cursor < len(m.rows) && m.rows[m.cursor].kind == rowSession {
		m.selKey = sessionKey(&m.rows[m.cursor].s)
	}
}

func (m *Model) selectedSession() *session.Session {
	if m.cursor < len(m.rows) && m.rows[m.cursor].kind == rowSession {
		s := m.rows[m.cursor].s
		return &s
	}
	return nil
}

// idleTimeout is how long the picker waits without input before quitting on
// its own. A package var so tests can shorten it.
var idleTimeout = 30 * time.Minute

// idleMsg fires when the idle timeout elapses; id is the Model.idleID
// generation that scheduled it, so stale timers are ignored.
type idleMsg struct{ id int }

// idleCmd schedules the idle-timeout check for the current generation.
func (m Model) idleCmd() tea.Cmd {
	id := m.idleID
	return tea.Tick(idleTimeout, func(time.Time) tea.Msg { return idleMsg{id} })
}

// --- bubbletea plumbing ----------------------------------------------------

// Update is the bubbletea entry point. It wraps update() to reset the idle
// timeout on real user input — keys and mouse presses/wheel; motion events
// are too chatty to count as activity.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	reset := false
	switch msg := msg.(type) {
	case tea.KeyMsg:
		reset = true
	case tea.MouseMsg:
		reset = msg.Action == tea.MouseActionPress ||
			msg.Type == tea.MouseWheelUp || msg.Type == tea.MouseWheelDown
	}
	if reset {
		m.idleID++
	}
	next, cmd := m.update(msg)
	if !reset {
		return next, cmd
	}
	// Some handlers (mouse click) return *Model; normalize to a value.
	switch got := next.(type) {
	case Model:
		m = got
	case *Model:
		m = *got
	}
	return m, tea.Batch(cmd, m.idleCmd())
}

func (m Model) Init() tea.Cmd {
	cmds := append([]tea.Cmd{m.spin.Tick, m.idleCmd()}, m.scanCmds()...)
	return tea.Batch(cmds...)
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case idleMsg:
		if msg.id == m.idleID {
			m.timedOut = true
			return m, tea.Quit
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if !m.sized {
			m.sized = true
			m.sidebarOn = m.width >= 140
			m.previewOn = m.width >= 100
		}
		m.resize()
		return m, nil

	case loadedMsg:
		if msg.generation != m.generation {
			return m, nil
		}
		if m.pending > 0 {
			m.pending--
		}
		if msg.err != nil {
			m.warnings = append(m.warnings,
				fmt.Sprintf("warning: %s provider failed: %v", msg.name, msg.err))
		} else {
			kept := m.all[:0]
			for _, existing := range m.all {
				if existing.Source != msg.name {
					kept = append(kept, existing)
				}
			}
			ss := provider.FilterHidden(msg.sessions, provider.HiddenKeys())
			m.all = append(kept, ss...)
			m.rebuild()
		}
		if m.pending == 0 {
			firstLoad := !m.loaded
			m.loaded = true
			if firstLoad && len(m.all) == 0 {
				m.noSessions = true
				return m, tea.Quit
			}
		}
		return m, nil

	case spinner.TickMsg:
		if m.pending > 0 {
			var cmd tea.Cmd
			m.spin, cmd = m.spin.Update(msg)
			return m, cmd
		}
		return m, nil

	case mutatedMsg:
		m.mutBusy = false
		m.modal = modalNone
		if msg.err != nil {
			m.warnings = append(m.warnings, "warning: "+msg.err.Error())
			return m, nil
		}
		if msg.action == "hide" {
			// Resumer-side hide: nothing changed on disk, so there is
			// nothing to rescan — the row is simply dropped.
			kept := m.all[:0]
			for _, existing := range m.all {
				if existing.Source == msg.source && existing.SessionID == msg.id {
					continue
				}
				kept = append(kept, existing)
			}
			m.all = kept
			if m.selKey == msg.source+"\x00"+msg.id {
				m.selKey = ""
			}
			m.rebuild()
			return m, nil
		}
		// Drop the row immediately (the listing state after a delete, or
		// after a kimi archive whose sessions are skipped on parse), then
		// rescan just that provider to heal any divergence. The generation
		// bump also discards scan results that were in flight before the
		// mutation returned.
		kept := m.all[:0]
		for _, existing := range m.all {
			if existing.Source == msg.source && existing.SessionID == msg.id {
				continue
			}
			kept = append(kept, existing)
		}
		m.all = kept
		if m.selKey == msg.source+"\x00"+msg.id {
			m.selKey = ""
		}
		m.rebuild()
		for _, p := range m.providers {
			if p.Name() != msg.source {
				continue
			}
			m.generation++
			m.pending = 1
			f, gen, prov := m.filters, m.generation, p
			return m, tea.Batch(m.spin.Tick, func() tea.Msg {
				ss, err := prov.ListSessions(f)
				return loadedMsg{generation: gen, name: prov.Name(), sessions: ss, err: err}
			})
		}
		return m, nil

	case tea.MouseMsg:
		if m.modal != modalNone || m.filtering {
			return m, nil
		}
		switch {
		case msg.Type == tea.MouseLeft && msg.Action == tea.MouseActionPress:
			return m.handleMouseClick(msg.X, msg.Y)
		case msg.Type == tea.MouseWheelUp:
			m.focus = focusList // the wheel scrolls sessions, so keyboard nav should too
			m.handleMouseWheel(true)
			return m, nil
		case msg.Type == tea.MouseWheelDown:
			m.focus = focusList
			m.handleMouseWheel(false)
			return m, nil
		}
		return m, nil

	case tea.KeyMsg:
		if m.modal != modalNone {
			cmd, _ := m.updateModal(msg)
			return m, cmd
		}
		if msg.String() == "ctrl+r" {
			m.generation++
			m.pending = len(m.providers)
			m.warnings = nil
			cmds := append([]tea.Cmd{m.spin.Tick}, m.scanCmds()...)
			return m, tea.Batch(cmds...)
		}
		if m.filtering {
			return m.updateFilter(msg)
		}
		if m.focus == focusSidebar {
			return m.updateSidebar(msg)
		}
		switch msg.String() {
		case "enter":
			if s := m.selectedSession(); s != nil {
				sel := s
				m.selected = sel
				return m, tea.Quit
			}
			return m, nil
		case "esc":
			switch {
			case m.filterValue != "":
				m.filterValue = ""
				m.filterInput.SetValue("")
				m.rebuild()
				return m, nil
			case m.wsFilter != "":
				m.wsFilter = ""
				m.rebuild()
				return m, nil
			}
			return m, tea.Quit
		case "ctrl+c":
			return m, tea.Quit
		case "tab":
			m.switchTab(1)
			return m, nil
		case "shift+tab", "left":
			m.switchTab(-1)
			return m, nil
		case "right":
			m.switchTab(1)
			return m, nil
		case "/":
			m.filtering = true
			m.filterInput.Focus()
			m.filterInput.CursorEnd()
			return m, textinput.Blink
		case "w":
			if m.sidebarOn && m.width >= 100 {
				m.focus = focusSidebar
			}
			return m, nil
		case "o":
			m.sidebarOn = !m.sidebarOn
			m.resize()
			return m, nil
		case "v":
			now := time.Now()
			if now.Sub(m.lastV) <= 600*time.Millisecond {
				m.lastV = time.Time{}
				m.openDetailModal()
				return m, nil
			}
			m.lastV = now
			m.previewOn = !m.previewOn
			m.resize()
			return m, nil
		case "g":
			m.grouped = !m.grouped
			m.rebuild()
			return m, nil
		case "d":
			m.openMutateModal()
			return m, nil
		case "P":
			m.openProviderModal()
			return m, nil
		case "?":
			m.modal = modalHelp
			return m, nil
		case "up", "k":
			m.moveCursor(-1)
			return m, nil
		case "down", "j":
			m.moveCursor(1)
			return m, nil
		case "pgup", "b":
			m.moveCursor(-m.pageSize())
			return m, nil
		case "pgdown", "f":
			m.moveCursor(m.pageSize())
			return m, nil
		case "home":
			m.jumpTo(false)
			return m, nil
		case "end", "G":
			m.jumpTo(true)
			return m, nil
		case "ctrl+d":
			m.preview.HalfPageDown()
			return m, nil
		case "ctrl+u":
			m.preview.HalfPageUp()
			return m, nil
		}
	}
	return m, nil
}

func (m *Model) switchTab(delta int) {
	if len(m.tabs) > 1 {
		m.tabIdx = (m.tabIdx + delta + len(m.tabs)) % len(m.tabs)
	}
	m.rebuild()
}

func (m Model) updateFilter(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if ok {
		switch key.String() {
		case "enter":
			m.filtering = false
			m.filterInput.Blur()
			return m, nil
		case "esc":
			m.filtering = false
			m.filterValue = ""
			m.filterInput.SetValue("")
			m.filterInput.Blur()
			m.rebuild()
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.filterInput, cmd = m.filterInput.Update(msg)
	if m.filterInput.Value() != m.filterValue {
		m.filterValue = m.filterInput.Value()
		m.rebuild()
	}
	return m, cmd
}

func (m Model) updateSidebar(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	items := len(m.groups) + len(m.providers)
	switch key.String() {
	case "esc", "w":
		m.focus = focusList
		return m, nil
	case "up", "k":
		if m.sideCur > 0 {
			m.sideCur--
		}
		return m, nil
	case "down", "j":
		if m.sideCur < items-1 {
			m.sideCur++
		}
		return m, nil
	case " ", "enter":
		return m, m.activateSidebarItem()
	}
	return m, nil
}

// activateSidebarItem applies the item under the sidebar cursor: workspace
// items toggle the workspace filter, agent items toggle the provider.
func (m *Model) activateSidebarItem() tea.Cmd {
	if m.sideCur < len(m.groups) {
		g := m.groups[m.sideCur]
		if m.wsFilter == g.Key {
			m.wsFilter = ""
		} else {
			m.wsFilter = g.Key
		}
		m.rebuild()
		return nil
	}
	j := m.sideCur - len(m.groups)
	if j >= 0 && j < len(m.providers) {
		return m.toggleAgent(m.providers[j].Name())
	}
	return nil
}

// Pick runs the picker. Returns (selection, sawNoSessions, error); selection
// is nil on cancel.
func Pick(filters session.Filters) (*session.Session, bool, error) {
	var providers []provider.Provider
	if filters.Source != "" {
		p := provider.Get(filters.Source)
		if p == nil {
			return nil, false, fmt.Errorf("unknown provider: %s", filters.Source)
		}
		if !p.IsAvailable() {
			return nil, false, fmt.Errorf(
				"%s provider not available (binary or session directory missing)", filters.Source)
		}
		providers = []provider.Provider{p}
	} else {
		providers = provider.Active()
	}

	prog := tea.NewProgram(NewModel(providers, filters), tea.WithAltScreen(), tea.WithMouseCellMotion())
	final, err := prog.Run()
	if err != nil {
		return nil, false, err
	}
	m, ok := final.(Model)
	if !ok {
		return nil, false, fmt.Errorf("unexpected model type")
	}
	if m.timedOut {
		fmt.Fprintf(os.Stderr, "%s: no activity for %s — exiting\n",
			brand.Name, idleTimeout.Round(time.Second))
	}
	return m.Selected(), m.NoSessions(), nil
}
