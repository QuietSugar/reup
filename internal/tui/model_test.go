package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/QuietSugar/reup/internal/config"
	"github.com/QuietSugar/reup/internal/provider"
	"github.com/QuietSugar/reup/internal/session"
	"github.com/QuietSugar/reup/internal/textutil"
)

// stubProvider serves a fixed session list under a fixed name.
type stubProvider struct {
	name     string
	sessions []session.Session
}

func (p *stubProvider) Name() string      { return p.name }
func (p *stubProvider) Badge() string     { return p.name }
func (p *stubProvider) BadgeANSI() string { return "" }
func (p *stubProvider) IsAvailable() bool { return true }
func (p *stubProvider) LoadDetail(string) (*session.Session, error) {
	return nil, nil
}
func (p *stubProvider) ListSessions(session.Filters) ([]session.Session, error) {
	return p.sessions, nil
}

func demoSessions() []session.Session {
	return []session.Session{
		{Source: "kimi-code", SessionID: "k1", Cwd: "/ws/a", ProjectLabel: "a", Title: "a1", LastTS: "2026-09-29T10:00:00Z", FirstPrompt: "alpha prompt"},
		{Source: "opencode", SessionID: "o1", Cwd: "/ws/a", ProjectLabel: "a", Title: "a2", LastTS: "2026-09-29T09:00:00Z", FirstPrompt: "beta prompt"},
		{Source: "kimi-code", SessionID: "k2", Cwd: "/ws/b", ProjectLabel: "b", Title: "b1", LastTS: "2026-09-29T08:00:00Z", FirstPrompt: "gamma prompt"},
	}
}

// newDashboard builds a picker over two stub providers and delivers the initial
// scan results, so every test starts from a fully loaded three-panel screen.
func newDashboard(t *testing.T, width int) Model {
	t.Helper()
	t.Setenv("REUP_TIPS_FILE", filepath.Join(t.TempDir(), "missing-tips.md"))
	var kimi, oc []session.Session
	for _, s := range demoSessions() {
		if s.Source == "kimi-code" {
			kimi = append(kimi, s)
		} else {
			oc = append(oc, s)
		}
	}
	m := NewModel([]provider.Provider{
		&stubProvider{name: "kimi-code", sessions: kimi},
		&stubProvider{name: "opencode", sessions: oc},
	}, session.Filters{})
	up, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
	m = up.(Model)
	for _, cmd := range m.scanCmds() {
		up, _ = m.Update(cmd())
		m = up.(Model)
	}
	return m
}

func sessionIDs(m Model) []string {
	var out []string
	for _, r := range m.rows {
		if r.kind == rowSession {
			out = append(out, r.s.SessionID)
		}
	}
	return out
}

func TestTabsFilterSessions(t *testing.T) {
	m := newDashboard(t, 120)
	if got := sessionIDs(m); len(got) != 3 {
		t.Fatalf("all tab: %v, want 3 sessions", got)
	}
	// tab → kimi-code only.
	up, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = up.(Model)
	got := sessionIDs(m)
	if len(got) != 2 || got[0] != "k1" || got[1] != "k2" {
		t.Fatalf("kimi tab: %v, want k1+k2", got)
	}
	// shift+tab back to all.
	up, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = up.(Model)
	if got = sessionIDs(m); len(got) != 3 {
		t.Fatalf("back to all: %v", got)
	}
}

// The sidebar's WORKSPACES section filters the list by workspace; toggling the
// same entry again clears the filter.
func TestSidebarWorkspaceFilter(t *testing.T) {
	m := newDashboard(t, 120)
	m.focus = focusSidebar
	m.sideCur = 0 // /ws/a (newest)

	up, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = up.(Model)
	if m.wsFilter == "" || len(m.selectable) != 2 {
		t.Fatalf("workspace filter not applied: filter=%q selectable=%v", m.wsFilter, m.selectable)
	}
	up, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = up.(Model)
	if m.wsFilter != "" || len(m.selectable) != 3 {
		t.Fatalf("workspace filter not cleared: filter=%q selectable=%v", m.wsFilter, m.selectable)
	}
}

// Group mode (g) interleaves workspace header rows; navigation must land on
// sessions only.
// Group mode is the default; g toggles between grouped rows and the flat list.
func TestGroupModeToggleAndNavigation(t *testing.T) {
	m := newDashboard(t, 120)
	if !m.grouped || len(m.rows) != 5 || len(m.selectable) != 3 {
		t.Fatalf("default group mode: rows=%d selectable=%d, want 5/3", len(m.rows), len(m.selectable))
	}
	if m.rows[0].kind != rowGroup {
		t.Fatal("first row should be a group header in group mode")
	}

	// g → flat: exactly the three sessions, no headers.
	up, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	m = up.(Model)
	if m.grouped || len(m.rows) != 3 {
		t.Fatalf("flat mode after g: rows=%d grouped=%v, want 3 sessions", len(m.rows), m.grouped)
	}

	// g again → back to grouped; navigation must land on sessions only.
	up, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	m = up.(Model)
	m.cursor = m.selectable[0]
	for i := 0; i < 4; i++ {
		m.moveCursor(1)
		if m.rows[m.cursor].kind != rowSession {
			t.Fatalf("cursor landed on a header row at %d", m.cursor)
		}
	}
}

// The fuzzy filter narrows the list; clearing it restores everything.
func TestFuzzyFilter(t *testing.T) {
	m := newDashboard(t, 120)
	m.filterValue = "alpha"
	m.rebuild()
	if got := sessionIDs(m); len(got) != 1 || got[0] != "k1" {
		t.Fatalf("filter 'alpha': %v, want only k1", got)
	}
	m.filterValue = ""
	m.rebuild()
	if len(m.selectable) != 3 {
		t.Fatalf("clear filter: %d rows, want 3", len(m.selectable))
	}
}

// Panel defaults follow the terminal width: ≥140 shows the sidebar, <100
// hides the preview.
func TestPanelDefaultsByWidth(t *testing.T) {
	for _, tc := range []struct {
		width       int
		wantSidebar bool
		wantPreview bool
	}{
		{160, true, true},
		{120, false, true},
		{90, false, false},
	} {
		t.Setenv("REUP_TIPS_FILE", filepath.Join(t.TempDir(), "missing-tips.md"))
		m := NewModel(nil, session.Filters{})
		up, _ := m.Update(tea.WindowSizeMsg{Width: tc.width, Height: 40})
		got := up.(Model)
		if got.sidebarOn != tc.wantSidebar || got.previewOn != tc.wantPreview {
			t.Errorf("width %d: sidebar=%v preview=%v", tc.width, got.sidebarOn, got.previewOn)
		}
	}
}

// Toggling a panel key flips the panel even on narrow terminals.
func TestPanelToggles(t *testing.T) {
	m := newDashboard(t, 90) // both off by default
	up, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m = up.(Model)
	if !m.sidebarOn {
		t.Error("o should turn the sidebar on")
	}
	up, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = up.(Model)
	if !m.previewOn {
		t.Error("v should turn the preview on")
	}
}

// The session row must mark a deleted working directory before the user
// commits to the row.
func TestSessionRowMarksDeletedDirectory(t *testing.T) {
	m := newDashboard(t, 120)
	gone := filepath.Join(t.TempDir(), "vanished")
	m.all = append(m.all, session.Session{
		Source: "kimi-code", SessionID: "gone", Cwd: gone,
		ProjectLabel: "gone", Title: "Lost session", LastTS: "2026-09-29T11:00:00Z",
	})
	m.rebuild()
	out := m.sessionRow(&m.all[len(m.all)-1], false, 120)
	if !strings.Contains(out, textutil.DirDeletedLabel) {
		t.Errorf("row should carry the deleted label: %q", out)
	}
}

func TestHelpModalOpensAndCloses(t *testing.T) {
	m := newDashboard(t, 120)
	up, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = up.(Model)
	if m.modal != modalHelp {
		t.Fatal("help modal did not open")
	}
	if view := m.View(); !strings.Contains(view, "KEYS") || !strings.Contains(view, "Session tips") {
		t.Errorf("help modal content missing:\n%s", view)
	}
	up, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = up.(Model)
	if m.modal != modalNone {
		t.Fatal("esc did not close the help modal")
	}
}

// The provider modal persists toggles to the config file and applies them to
// the live session pool.
func TestProviderModalAppliesConfig(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("REUP_CONFIG", cfgPath)
	t.Cleanup(func() { provider.SetDisabled(nil) })

	m := newDashboard(t, 120)
	up, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	m = up.(Model)
	if m.modal != modalProviders {
		t.Fatal("provider modal did not open")
	}
	names := m.allProviderNames()
	if len(names) != 2 {
		t.Fatalf("provider modal rows = %v, want the two stub providers", names)
	}
	// cursor 0 = first (alphabetical) name; toggle it off, then save.
	m.provCur = 0
	up, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = up.(Model)
	up, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = up.(Model)

	off := names[0]
	if provider.IsEnabled(off) {
		t.Fatalf("%s should be disabled after save", off)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil || !strings.Contains(string(data), off) {
		t.Fatalf("config not persisted: %v %s", err, data)
	}
	if got := sessionIDs(m); strings.Contains(strings.Join(got, ","), off) {
		t.Errorf("disabled provider sessions still listed: %v", got)
	}
}

func TestFormatLastActivity(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	cases := []struct{ ts, want string }{
		{"2026-09-25T06:30:00Z", "4d5h"},
		{"2026-09-29T07:55:00Z", "4h5m"},
		{"2026-09-29T11:35:00Z", "25m"},
		{"2026-09-29T12:01:00Z", "now"},
		{"invalid", "?"},
	}
	for _, tc := range cases {
		if got := formatLastActivity(tc.ts, now); got != tc.want {
			t.Errorf("formatLastActivity(%q) = %q, want %q", tc.ts, got, tc.want)
		}
	}
}

// The column header must line up with the fixed columns the rows use,
// otherwise the picker shows a header that points at nothing.
func TestColumnHeaderAlignsWithRowColumns(t *testing.T) {
	m := newDashboard(t, 120)
	s := m.all[0]
	_, _, listW := m.panelWidths()
	row := m.sessionRow(&s, false, listW)

	head := columnHeader()
	for _, label := range []string{"age", "agent", "title"} {
		if !strings.Contains(head, label) {
			t.Errorf("column header is missing the %q label: %q", label, head)
		}
	}
	plain := func(s string) string {
		var b strings.Builder
		for i := 0; i < len(s); i++ {
			if s[i] == 0x1b {
				for i < len(s) && s[i] != 'm' {
					i++
				}
				continue
			}
			b.WriteByte(s[i])
		}
		return b.String()
	}
	colOf := func(s, sub string) int {
		i := strings.Index(s, sub)
		if i < 0 {
			return -1
		}
		return textutil.DisplayWidth(s[:i])
	}
	h, r := plain(head), plain(row)
	for _, pair := range [][2]string{
		{"agent", "kimi-code"},
		{"title", "a1"},
	} {
		hc, rc := colOf(h, pair[0]), colOf(r, pair[1])
		if hc < 0 || rc < 0 {
			t.Fatalf("could not locate %q in header/row: %q / %q", pair[0], h, r)
		}
		if hc != rc {
			t.Errorf("%q column starts at display column %d in the header but %d in the row:\nheader: %q\nrow:    %q",
				pair[0], hc, rc, h, r)
		}
	}
}

// The group header line shows the count first, then the abbreviated workspace
// directory — provider names are not listed there.
func TestGroupRowShowsCountThenPath(t *testing.T) {
	m := newDashboard(t, 120)
	m.grouped = true
	m.rebuild()
	var headers []string
	for _, r := range m.rows {
		if r.kind == rowGroup {
			headers = append(headers, m.groupRow(r.g, 120))
		}
	}
	if len(headers) != 2 {
		t.Fatalf("got %d group rows, want 2", len(headers))
	}
	if !strings.Contains(headers[0], "2 sessions") {
		t.Errorf("merged group should report 2 sessions: %q", headers[0])
	}
	if strings.Contains(headers[0], "kimi-code") || strings.Contains(headers[0], "opencode") {
		t.Errorf("group row must not list provider names: %q", headers[0])
	}
	if !strings.Contains(headers[0], "/ws/a") {
		t.Errorf("group row should show the workspace path: %q", headers[0])
	}
}

func TestConfigRoundTripStillWorks(t *testing.T) {
	p := filepath.Join(t.TempDir(), "reup", "config.json")
	t.Setenv("REUP_CONFIG", p)
	var c config.Config
	c.Disable("kimi-code")
	if err := config.Save(c); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load()
	if err != nil || len(got.Disabled) != 1 || got.Disabled[0] != "kimi-code" {
		t.Errorf("config round-trip: %+v %v", got, err)
	}
}

// The age column uses tiered units so it never outgrows its column:
// days/hours up to a month, then months, then years (with months).
func TestFormatLastActivityTieredUnits(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		ago  time.Duration
		want string
	}{
		{29*24*time.Hour + 5*time.Hour, "29d5h"},
		{60 * 24 * time.Hour, "2mo"},
		{400 * 24 * time.Hour, "1y1mo"},
		{735 * 24 * time.Hour, "2y"},
	}
	for _, c := range cases {
		ts := now.Add(-c.ago).Format(time.RFC3339)
		if got := formatLastActivity(ts, now); got != c.want {
			t.Errorf("formatLastActivity(%q) = %q, want %q", ts, got, c.want)
		}
	}
	// Everything must fit the fixed age column.
	for _, d := range []int{3, 45, 200, 800, 1500} {
		ts := now.Add(-time.Duration(d) * 24 * time.Hour).Format(time.RFC3339)
		if got := formatLastActivity(ts, now); textutil.DisplayWidth(got) > colAge {
			t.Errorf("age %q (%d cols) outgrew the column", got, textutil.DisplayWidth(got))
		}
	}
}

// vv opens the full-detail popup (every prompt, wrapped), esc closes it.
func TestDetailPopupOpensWithDoubleV(t *testing.T) {
	m := newDashboard(t, 120)
	m.all[0].Prompts = nil
	for i := 0; i < 12; i++ {
		m.all[0].Prompts = append(m.all[0].Prompts,
			session.Prompt{TS: m.all[0].LastTS, Text: fmt.Sprintf("prompt number %d", i+1)})
	}
	m.rebuild()

	up, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = up.(Model)
	if m.modal != modalNone {
		t.Fatal("a single v must only toggle the preview panel")
	}
	up, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = up.(Model)
	if m.modal != modalDetail {
		t.Fatal("vv did not open the full-detail popup")
	}
	content := m.View()
	if !strings.Contains(content, "prompt number 12") {
		t.Error("full-detail popup is missing later prompts")
	}
	up, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = up.(Model)
	if m.modal != modalNone {
		t.Fatal("esc did not close the detail popup")
	}
}

// Every panel line is padded to the same width, so the layout never shifts
// when the content changes (e.g. after a provider tab switch).
func TestPanelLinesHaveFixedWidth(t *testing.T) {
	m := newDashboard(t, 120)
	m.all[0].Title = "x"
	m.all[1].Title = strings.Repeat("long", 30)
	m.rebuild()
	m.switchTab(1)
	m.switchTab(-1)

	for _, l := range strings.Split(mainArea(m), "\n") {
		if got := lineWidth(l); got != m.width {
			t.Errorf("panel line width = %d, want %d: %q", got, m.width, l)
		}
	}
}

// Detail panel: field labels are styled differently from their values, and
// the alignment survives the styling.
func TestDetailPanelStylesLabels(t *testing.T) {
	// lipgloss downgrades to plain text without a TTY; force color on so the
	// label styling is observable.
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	m := newDashboard(t, 120)
	panel := renderPanel(previewLines(m, 44, 30), 44, 30)
	var src string
	for _, l := range strings.Split(panel, "\n") {
		if strings.Contains(stripANSI(l), "source:") {
			src = l
			break
		}
	}
	if src == "" {
		t.Fatal("source line missing from the detail panel")
	}
	if !strings.Contains(src, "\x1b[") {
		t.Errorf("field label is not styled: %q", src)
	}
	if !strings.Contains(stripANSI(src), "[kimi-code]") {
		t.Errorf("value lost by styling: %q", src)
	}
	if lineWidth(src) != 44 {
		t.Errorf("styled line width = %d, want 44", lineWidth(src))
	}
}

// --- idle timeout -----------------------------------------------------------

func TestIdleTimeoutQuits(t *testing.T) {
	m := NewModel(nil, session.Filters{})
	next, cmd := m.Update(idleMsg{id: m.idleID})
	got := next.(Model)
	if !got.timedOut {
		t.Fatal("idle timeout did not mark the model as timed out")
	}
	if cmd == nil {
		t.Fatal("idle timeout returned no quit command")
	}
}

func TestStaleIdleTimerIgnored(t *testing.T) {
	m := NewModel(nil, session.Filters{})
	next, cmd := m.Update(idleMsg{id: m.idleID + 1})
	got := next.(Model)
	if got.timedOut {
		t.Fatal("stale idle timer quit the picker")
	}
	if cmd != nil {
		t.Fatal("stale idle timer returned a command")
	}
}

func TestUserInputResetsIdleTimer(t *testing.T) {
	m := NewModel(nil, session.Filters{})
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	got := next.(Model)
	if got.idleID != 1 {
		t.Fatalf("idleID after input = %d, want 1", got.idleID)
	}
	if cmd == nil {
		t.Fatal("input returned no command (idle timer not rescheduled)")
	}
	// The pre-input timer (generation 0) must not quit the picker later.
	next, _ = got.Update(idleMsg{id: 0})
	if next.(Model).timedOut {
		t.Fatal("stale pre-input timer quit the picker after input")
	}
	// The current generation's timer must.
	next, _ = got.Update(idleMsg{id: 1})
	if !next.(Model).timedOut {
		t.Fatal("current-generation idle timer did not quit the picker")
	}
}

func TestMousePressResetsIdleTimerButMotionDoesNot(t *testing.T) {
	m := NewModel(nil, session.Filters{})
	press := tea.MouseMsg{Type: tea.MouseLeft, Action: tea.MouseActionPress}
	if next, _ := m.Update(press); next.(Model).idleID != 1 {
		t.Fatal("mouse press did not reset the idle timer")
	}
	motion := tea.MouseMsg{Type: tea.MouseLeft, Action: tea.MouseActionMotion}
	if next, _ := m.Update(motion); next.(Model).idleID != 0 {
		t.Fatal("mouse motion must not reset the idle timer")
	}
}
