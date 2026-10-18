// Provider/agent management, the hide/archive modal opener, and the
// async message types shared with model.go's Update.
package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/QuietSugar/reup/internal/config"
	"github.com/QuietSugar/reup/internal/provider"
	"github.com/QuietSugar/reup/internal/render"
	"github.com/QuietSugar/reup/internal/session"
)

type loadedMsg struct {
	generation int
	name       string
	sessions   []session.Session
	err        error
}

// Model is the picker's bubbletea model. Exported for teatest.

// --- provider / agent management -------------------------------------------

// syncProviders re-derives tabs and the session pool after a provider
// enable/disable. Outside tests it re-reads the registry so availability and
// the enabled set stay authoritative.
func (m *Model) syncProviders() {
	if m.filters.Source == "" && len(provider.All()) > 0 {
		m.providers = provider.Active()
	}
	seen := map[string]bool{}
	tabs := []string{""}
	enabled := map[string]bool{}
	for _, p := range m.providers {
		name := p.Name()
		if !provider.IsEnabled(name) || seen[name] {
			continue
		}
		seen[name] = true
		enabled[name] = true
		tabs = append(tabs, name)
	}
	m.tabs = tabs
	if m.tabIdx >= len(m.tabs) {
		m.tabIdx = 0
	}
	kept := m.all[:0]
	for _, s := range m.all {
		if enabled[s.Source] {
			kept = append(kept, s)
		}
	}
	m.all = kept
	m.rebuild()
}

// toggleAgent flips one provider's enabled state, persists it, and applies it
// live. Enabling schedules a rescan so the provider's sessions load.
func (m *Model) toggleAgent(name string) tea.Cmd {
	cfg, err := config.Load()
	if err != nil {
		cfg = config.Config{}
	}
	if cfg.IsDisabled(name) {
		cfg.Enable(name)
	} else {
		cfg.Disable(name)
	}
	if err := config.Save(cfg); err != nil {
		m.warnings = append(m.warnings, "warning: could not save config: "+err.Error())
		return nil
	}
	provider.SetDisabled(cfg.Disabled)

	had := false
	for _, s := range m.all {
		if s.Source == name {
			had = true
			break
		}
	}
	m.syncProviders()
	if !had && provider.Get(name) != nil && provider.IsEnabled(name) {
		return m.rescanCmds() // newly enabled — load its sessions
	}
	return nil
}

func (m *Model) openProviderModal() {
	m.modal = modalProviders
	m.provCur = 0
	m.provTemp = map[string]bool{}
	for _, name := range provider.DisabledNames() {
		m.provTemp[name] = true
	}
}

// openMutateModal arms the hide/archive confirmation for the selected
// session. Providers with an official archive (kimi) use it — including the
// auto-start-server flow; every other provider hides the session in
// reup's own list, which needs no server at all.
func (m *Model) openMutateModal() {
	s := m.selectedSession()
	if s == nil {
		return
	}
	m.mutTarget = s
	m.mutCur = 0
	m.mutCheck = nil
	m.mutBusy = false
	m.mutStarter = nil

	for _, p := range m.providers {
		if p.Name() != s.Source {
			continue
		}
		if mut := provider.MutatorFor(p); mut != nil {
			m.mutator = mut
			m.mutStarter = provider.StarterFor(p)
			m.mutActions = []string{"archive"}
			m.mutHide = false
			m.mutAvail = mut.ServerAvailable()
			if m.mutAvail {
				m.mutCheck = mut.CheckSession(s.SessionID)
			}
			m.modal = modalMutate
			return
		}
	}
	// No official archive: reup-side hide.
	m.mutator = nil
	m.mutActions = []string{"hide"}
	m.mutHide = true
	m.mutAvail = true
	m.modal = modalMutate
}

// mutatedMsg reports the outcome of one delete/archive call.
type mutatedMsg struct {
	source, id, action string
	err                error
}

// openDetailModal shows the selected session's full detail (every prompt,
// long lines wrapped) in a scrollable popup.
func (m *Model) openDetailModal() {
	s := m.selectedSession()
	if s == nil {
		return
	}
	bw := m.width / 2
	if bw < 52 {
		bw = 52
	}
	if bw > 100 {
		bw = 100
	}
	if bw > m.width-6 {
		bw = m.width - 6
	}
	m.detail.Width = bw - 6
	h := m.height - 8
	if h < 6 {
		h = 6
	}
	m.detail.Height = h
	var body []string
	conv := render.ConversationLines(s)
	if len(conv) == 0 {
		body = []string{dimStyle.Render("(no prompts in this session)")}
	} else {
		body = wrapLines(conv, m.detail.Width)
	}
	header := cut(s.Title, m.detail.Width)
	m.detail.SetContent(strings.Join(append([]string{headerStyle.Render(header), ""}, body...), "\n"))
	m.detail.GotoTop()
	m.modal = modalDetail
}

func (m *Model) rescanCmds() tea.Cmd {
	m.generation++
	m.pending = len(m.providers)
	m.warnings = nil
	cmds := append([]tea.Cmd{m.spin.Tick}, m.scanCmds()...)
	return tea.Batch(cmds...)
}

func (m Model) scanCmds() []tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.providers))
	for _, p := range m.providers {
		p := p
		f, generation := m.filters, m.generation
		cmds = append(cmds, func() tea.Msg {
			ss, err := p.ListSessions(f)
			return loadedMsg{generation: generation, name: p.Name(), sessions: ss, err: err}
		})
	}
	return cmds
}
