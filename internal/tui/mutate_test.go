package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/QuietSugar/reup/internal/config"
	"github.com/QuietSugar/reup/internal/provider"
	"github.com/QuietSugar/reup/internal/session"
)

// mutStub wraps a stubProvider with a recording SessionMutator capability, so
// tests can pin what the picker calls without any real agent.
type mutStub struct {
	stubProvider
	available     bool
	checkErr      error
	autoStartable bool
	started       int
	stopped       int
	archived      []string
	archiveErr    error
	sessionsAfter []session.Session // returned by ListSessions once mutated
}

func (s *mutStub) ServerAvailable() bool { return s.available }
func (s *mutStub) CheckSession(string) error {
	return s.checkErr
}

func (s *mutStub) ArchiveSession(id string) error {
	s.archived = append(s.archived, id)
	return s.archiveErr
}
func (s *mutStub) ListSessions(f session.Filters) ([]session.Session, error) {
	if s.sessionsAfter != nil && len(s.archived) > 0 {
		return s.sessionsAfter, nil
	}
	return s.sessions, nil
}

var _ provider.SessionMutator = (*mutStub)(nil)

// mutStarterStub adds the ServerStarter capability on top of mutStub, mimicking
// kimi web: StartServer flips availability, as if the server came up.
type mutStarterStub struct {
	mutStub
	started int
	stopped int
}

func (s *mutStarterStub) StartServer() error {
	s.started++
	s.available = true
	return nil
}

func (s *mutStarterStub) StartCommand() string { return "kimi web --no-open" }

func (s *mutStarterStub) StopServer() error {
	s.stopped++
	return nil
}

var _ provider.ServerStarter = (*mutStarterStub)(nil)

// newMutDashboard builds a loaded picker whose single provider has the
// mutation capability.
func newMutDashboard(t *testing.T, mut *mutStub) Model {
	t.Helper()
	m := NewModel([]provider.Provider{mut}, session.Filters{})
	up, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = up.(Model)
	for _, cmd := range m.scanCmds() {
		up, _ = m.Update(cmd())
		m = up.(Model)
	}
	// Park the cursor on the first session row.
	m.cursor = m.selectable[0]
	return m
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// runCmd executes a command and returns its message, or nil when the command
// does not produce one immediately. The idle-timeout tick is a wall-clock
// timer; draining it synchronously would stall for its full duration, so
// the drain helper below drops it (idle behavior has dedicated tests).
func runCmd(cmd tea.Cmd) tea.Msg {
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(50 * time.Millisecond):
		return nil
	}
}

// drive feeds keys to the model, draining any commands they return (so the
// mutation cmd and the follow-up rescan are executed synchronously).
func drive(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	for _, k := range keys {
		up, cmd := m.Update(key(k))
		m = up.(Model)
		m = drain(t, m, cmd)
	}
	return m
}

// drain executes a command chain until no message arrives immediately.
// Batch messages enqueue every sub-command; timer commands never produce a
// message immediately and are dropped.
func drain(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := runCmd(c)
		if msg == nil {
			continue
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		up, next := m.Update(msg)
		m = up.(Model)
		queue = append(queue, next)
	}
	return m
}

func TestMutateModalOpensAndCancels(t *testing.T) {
	mut := &mutStub{stubProvider: stubProvider{
		name: "kimi-code", sessions: []session.Session{
			{Source: "kimi-code", SessionID: "k1", Cwd: "/ws/a", Title: "t1", LastTS: "2026-09-29T10:00:00Z"},
		},
	}, available: true}
	m := newMutDashboard(t, mut)

	m = drive(t, m, "d")
	if m.modal != modalMutate {
		t.Fatal("d did not open the mutate modal")
	}
	view := m.View()
	if !strings.Contains(view, "t1") || !strings.Contains(view, "archive") {
		t.Errorf("modal should show title and the archive action:\n%s", view)
	}

	m = drive(t, m, "esc")
	if m.modal != modalNone {
		t.Fatal("esc did not close the modal")
	}
	if len(mut.archived) != 0 {
		t.Error("cancel must not call the mutator")
	}
}

func TestMutateModalDefaultsToArchiveForKimi(t *testing.T) {
	mut := &mutStub{stubProvider: stubProvider{
		name: "kimi-code", sessions: []session.Session{
			{Source: "kimi-code", SessionID: "k1", Cwd: "/ws/a", Title: "t1", LastTS: "2026-09-29T10:00:00Z"},
		},
	}, available: true}
	m := newMutDashboard(t, mut)

	m = drive(t, m, "d", "enter")
	if len(mut.archived) != 1 || mut.archived[0] != "k1" {
		t.Errorf("archive calls = %v, want [k1] (default highlight)", mut.archived)
	}

}

func TestMutateFailureBecomesWarning(t *testing.T) {
	mut := &mutStub{stubProvider: stubProvider{
		name: "opencode", sessions: []session.Session{
			{Source: "opencode", SessionID: "o1", Cwd: "/ws/a", Title: "t1", LastTS: "2026-09-29T10:00:00Z"},
		},
	}, available: true, archiveErr: errDeleteBoom{}}
	m := newMutDashboard(t, mut)

	m = drive(t, m, "d", "enter")
	if len(m.warnings) == 0 || !strings.Contains(m.warnings[0], "boom") {
		t.Errorf("warnings = %v, want the mutator error", m.warnings)
	}
	if ids := sessionIDs(m); len(ids) != 1 {
		t.Errorf("failed archive must keep the row, rows = %v", ids)
	}
}

type errDeleteBoom struct{}

func (errDeleteBoom) Error() string { return "boom" }

func TestMutateUnavailableServerDisablesConfirm(t *testing.T) {
	mut := &mutStub{stubProvider: stubProvider{
		name: "kimi-code", sessions: []session.Session{
			{Source: "kimi-code", SessionID: "k1", Cwd: "/ws/a", Title: "t1", LastTS: "2026-09-29T10:00:00Z"},
		},
	}, available: false}
	m := newMutDashboard(t, mut)

	m = drive(t, m, "d")
	view := stripANSI(m.View())
	if !strings.Contains(view, "not running") {
		t.Errorf("modal should explain the agent must be running:\n%s", view)
	}
	if !strings.Contains(view, "kimi web") {
		t.Errorf("modal should show the simplest start command:\n%s", view)
	}
	m = drive(t, m, "enter")
	if len(mut.archived) != 0 {
		t.Error("confirm must be a no-op when the agent is unavailable")
	}
}

// opencode has no official archive: d offers the reup-side hide, which
// needs no server at all.
func TestMutateOpenCodeOffersHide(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("REUP_CONFIG", cfgPath)
	mut := &mutStub{stubProvider: stubProvider{
		name: "opencode", sessions: []session.Session{
			{Source: "opencode", SessionID: "o1", Cwd: "/ws/a", Title: "t1", LastTS: "2026-09-29T10:00:00Z"},
		},
	}}
	_ = mut // plain dashboard: opencode has no mutator, the stub is unused
	m := newDashboard(t, 120)
	m.cursor = m.selectable[0]

	m = drive(t, m, "d")
	if !m.mutHide {
		t.Fatal("opencode should use the reup-side hide path")
	}
	if strings.Contains(stripANSI(m.View()), "not running") {
		t.Error("hide needs no server — no availability hint expected")
	}
}

// A per-session gate (e.g. codebuddy's API only manages sessions opened in
// the running instance) must surface in the modal and disable confirm.
func TestMutateCheckFailureDisablesConfirm(t *testing.T) {
	mut := &mutStub{stubProvider: stubProvider{
		name: "codebuddy", sessions: []session.Session{
			{Source: "codebuddy", SessionID: "c1", Cwd: "/ws/a", Title: "t1", LastTS: "2026-09-29T10:00:00Z"},
		},
	}, available: true, checkErr: errNotManaged{}}
	m := newMutDashboard(t, mut)

	m = drive(t, m, "d")
	view := stripANSI(m.View())
	if !strings.Contains(view, "cannot manage this session") {
		t.Errorf("modal should explain the session is not manageable:\n%s", view)
	}
	m = drive(t, m, "enter")
	if len(mut.archived) != 0 {
		t.Error("confirm must be a no-op when CheckSession fails")
	}
}

type errNotManaged struct{}

func (errNotManaged) Error() string { return "not managed by the running instance" }

// Providers without an official archive hide reup-side: d writes the
// hidden key to the config and the row disappears (no agent interaction).
func TestProviderWithoutMutatorHidesInConfig(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("REUP_CONFIG", cfgPath)
	m := newDashboard(t, 120) // plain stubs: no SessionMutator
	m.cursor = m.selectable[0]

	m = drive(t, m, "d")
	if !m.mutHide || m.modal != modalMutate {
		t.Fatalf("d should open the hide modal, got modal=%v hide=%v", m.modal, m.mutHide)
	}
	if !strings.Contains(stripANSI(m.View()), "HIDE SESSION") {
		t.Errorf("modal should be titled HIDE SESSION:\n%s", m.View())
	}

	m = drive(t, m, "enter")
	if ids := sessionIDs(m); len(ids) != 2 {
		t.Errorf("rows after hide = %d, want 2", len(ids))
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hidden) != 1 || !strings.Contains(cfg.Hidden[0], "\x00") {
		t.Errorf("config hidden = %v, want one session key", cfg.Hidden)
	}
}

// The busy state swallows keys so nothing can interrupt an in-flight call.
func TestMutateBusySwallowsKeys(t *testing.T) {
	mut := &mutStub{stubProvider: stubProvider{
		name: "opencode", sessions: []session.Session{
			{Source: "opencode", SessionID: "o1", Cwd: "/ws/a", Title: "t1", LastTS: "2026-09-29T10:00:00Z"},
		},
	}, available: true}
	m := newMutDashboard(t, mut)

	up, cmd := m.Update(key("d"))
	m = up.(Model)
	up, cmd = m.Update(key("enter")) // arms mutBusy, returns the mutation cmd
	m = up.(Model)
	if !m.mutBusy {
		t.Fatal("enter did not arm mutBusy")
	}
	up, _ = m.Update(key("esc")) // must be swallowed
	m = up.(Model)
	if m.modal != modalMutate {
		t.Fatal("esc leaked through the busy state")
	}
	_ = cmd // delivered separately below
	up, _ = m.Update(mutatedMsg{source: "opencode", id: "o1", action: "delete"})
	m = up.(Model)
	if m.modal != modalNone || m.mutBusy {
		t.Error("mutatedMsg must close the modal and clear busy")
	}
	// Give the rescan cmd a beat to be collected by the runtime (not needed
	// synchronously here — the loadedMsg generation guard covers staleness).
	_ = time.Now()
}
