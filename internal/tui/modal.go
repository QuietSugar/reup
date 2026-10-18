// Generic modal support: when a modal is open it owns the keyboard and the
// whole screen renders as a centered, bordered box on a blank canvas.
package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/QuietSugar/reup/internal/config"
	"github.com/QuietSugar/reup/internal/provider"
)

type modalKind int

const (
	modalNone modalKind = iota
	modalHelp
	modalProviders
	modalDetail
	modalMutate
)

var modalKeys = []struct{ key, action string }{
	{"↑↓ / k j", "move"},
	{"/", "filter sessions"},
	{"enter", "resume"},
	{"d", "hide / archive session"},
	{"esc", "cancel / clear filter"},
	{"tab ⇄", "provider source"},
	{"w", "focus list ↔ sidebar"},
	{"space", "sidebar: filter ws / toggle agent"},
	{"g", "group ↔ flat list"},
	{"o / v", "toggle sidebar / preview"},
	{"v v", "full detail popup"},
	{"ctrl-r", "rescan"},
	{"mouse", "click tabs / sidebar / rows"},
	{"P", "provider management"},
	{"?", "this help"},
}

// allProviderNames lists every known provider: the picker's own set first
// (enabled ones), then anything else the registry knows (covers disabled
// providers, which are not part of the picker's provider list).
func (m Model) allProviderNames() []string {
	seen := map[string]bool{}
	var names []string
	for _, p := range m.providers {
		if !seen[p.Name()] {
			seen[p.Name()] = true
			names = append(names, p.Name())
		}
	}
	for _, p := range provider.All() {
		if !seen[p.Name()] {
			seen[p.Name()] = true
			names = append(names, p.Name())
		}
	}
	sort.Strings(names)
	return names
}

func (m Model) providerModalContent() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("PROVIDERS") +
		helpStyle.Render("  space toggle · enter save · esc cancel") + "\n")
	names := m.allProviderNames()
	for i, name := range names {
		check := "[x]" // enabled
		if m.provTemp[name] {
			check = "[ ]"
		}
		cursor := "  "
		if i == m.provCur {
			cursor = cursorStyle.Render("▌ ")
		}
		b.WriteString(fmt.Sprintf("%s%s %s\n", cursor, check, name))
	}
	b.WriteString(dimStyle.Render("\nspace toggles scanning; enter persists to the config file."))
	return b.String()
}

func (m Model) helpContent() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("KEYS") + "\n")
	for _, k := range modalKeys {
		b.WriteString(fmt.Sprintf("  %-10s %s\n", k.key, k.action))
	}
	b.WriteString("\n" + headerStyle.Render("TIPS") + "\n")
	if custom := loadCustomTips(); custom != "" {
		// A user-defined tips file replaces the built-in per-source tips.
		b.WriteString(custom)
		return b.String()
	}
	src := ""
	if m.tabIdx > 0 && m.tabIdx < len(m.tabs) {
		src = m.tabs[m.tabIdx]
	}
	b.WriteString(tipsForSource(src))
	b.WriteString("\n\n" + dimStyle.Render("custom tips: ~/.config/reup/tips.md (REUP_TIPS_FILE)"))
	return b.String()
}

func (m Model) renderModal() string {
	var content string
	boxW := m.width / 2
	if boxW < 46 {
		boxW = 46
	}
	if boxW > 76 {
		boxW = 76
	}
	if boxW > m.width-4 {
		boxW = m.width - 4
	}
	switch m.modal {
	case modalHelp:
		content = m.helpContent()
	case modalProviders:
		content = m.providerModalContent()
	case modalDetail:
		content = m.detail.View() + "\n" +
			dimStyle.Render("↑↓ scroll · v / esc close")
	case modalMutate:
		content = m.mutateModalContent()
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("8")).
		Padding(1, 2).
		Width(boxW).
		Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

// updateModal routes keys to the open modal. handled=false means the key was
// not consumed (only possible when no modal is open).
func (m *Model) updateModal(msg tea.Msg) (tea.Cmd, bool) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil, false
	}
	switch m.modal {
	case modalHelp:
		switch key.String() {
		case "esc", "?", "q", "enter":
			m.modal = modalNone
		}
		return nil, true // swallow everything while open

	case modalDetail:
		switch key.String() {
		case "esc", "q", "v", "enter":
			m.modal = modalNone
		case "up", "k":
			m.detail.LineUp(1)
		case "down", "j":
			m.detail.LineDown(1)
		case "pgup", "ctrl+u", "b":
			m.detail.HalfPageUp()
		case "pgdown", "ctrl+d", "f":
			m.detail.HalfPageDown()
		case "home":
			m.detail.GotoTop()
		case "end", "G":
			m.detail.GotoBottom()
		}
		return nil, true // swallow

	case modalProviders:
		names := m.allProviderNames()
		switch key.String() {
		case "esc":
			m.modal = modalNone
		case "up", "k":
			if m.provCur > 0 {
				m.provCur--
			}
		case "down", "j":
			if m.provCur < len(names)-1 {
				m.provCur++
			}
		case " ":
			if m.provCur < len(names) {
				name := names[m.provCur]
				m.provTemp[name] = !m.provTemp[name]
			}
		case "enter":
			m.applyProviderModal()
			m.modal = modalNone
			return m.rescanCmds(), true
		}
		return nil, true // swallow

	case modalMutate:
		if m.mutBusy {
			return nil, true // mutation in flight: swallow everything
		}
		switch key.String() {
		case "esc", "q":
			m.modal = modalNone
		case "up", "k":
			if m.mutCur > 0 {
				m.mutCur--
			}
		case "down", "j":
			if m.mutCur < len(m.mutActions)-1 {
				m.mutCur++
			}
		case "enter":
			// Confirm is allowed when the server is up and the session is
			// manageable, OR when the provider can start its server itself.
			if m.mutCheck != nil || m.mutCur >= len(m.mutActions) {
				return nil, true
			}
			if !m.mutAvail && m.mutStarter == nil {
				return nil, true // unavailable and nothing reup can do
			}
			target, action, mut, starter, avail :=
				*m.mutTarget, m.mutActions[m.mutCur], m.mutator, m.mutStarter, m.mutAvail
			m.mutBusy = true
			return func() tea.Msg {
				if !avail {
					if err := starter.StartServer(); err != nil {
						return mutatedMsg{source: target.Source, id: target.SessionID,
							action: action, err: fmt.Errorf("start server: %v", err)}
					}
				}
				var err error
				if action == "archive" {
					err = mut.ArchiveSession(target.SessionID)
					if err == nil && !avail && starter != nil {
						// reup brought the server up for this archive;
						// shut it down again (graceful, official endpoint).
						// Stop failures are advisory — the archive itself
						// already succeeded.
						_ = starter.StopServer()
					}
				} else {
					// Resumer-side hide: record the key in the config. The
					// session stays on disk and in the agent's own UI.
					cfg, cfgErr := config.Load()
					if cfgErr != nil {
						err = fmt.Errorf("hide: %v", cfgErr)
					} else {
						cfg.Hide(target.Source + "\x00" + target.SessionID)
						err = config.Save(cfg)
					}
				}
				return mutatedMsg{source: target.Source, id: target.SessionID, action: action, err: err}
			}, true
		}
		return nil, true // swallow
	}
	return nil, false
}

// startCommand returns the simplest official way to bring up the agent's
// local API server, shown in the mutate modal when it is unreachable.
// opencode never needs one (its CLI manages its own service).
func startCommand(source string) string {
	switch source {
	case "kimi-code":
		return "kimi web"
	case "codebuddy":
		return "codebuddy --serve"
	}
	return ""
}

// starterCommand returns the display form of the auto-startable server
// command (kimi web --no-open), or the source name as a fallback.
func (m Model) starterCommand() string {
	if m.mutStarter != nil {
		if s, ok := m.mutStarter.(interface{ StartCommand() string }); ok {
			return s.StartCommand()
		}
	}
	return ""
}

// mutateModalContent renders the delete/archive confirmation: session identity,
// the action choices, and the availability hint that gates confirm.
func (m Model) mutateModalContent() string {
	s := m.mutTarget
	var b strings.Builder
	heading := "HIDE SESSION"
	if !m.mutHide {
		heading = "ARCHIVE SESSION"
	}
	b.WriteString(headerStyle.Render(heading) + "\n")
	b.WriteString(headerStyle.Render(cut(s.Title, 48)) + "\n")
	b.WriteString(dimStyle.Render(s.Source+" · "+cut(s.SessionID, 40)) + "\n\n")

	if m.mutHide {
		b.WriteString("reup stops showing this session.\n")
		b.WriteString(dimStyle.Render(
			"The file stays on disk and the agent keeps showing it. Token") + "\n")
		b.WriteString(dimStyle.Render(
			"stats are unaffected. Restore: reup hidden") + "\n")
		b.WriteString(helpStyle.Render("enter hide · esc cancel"))
		return b.String()
	}

	if m.mutBusy {
		b.WriteString(dimStyle.Render("working…") + "\n")
		return b.String()
	}
	if !m.mutAvail {
		if m.mutStarter == nil {
			b.WriteString(warnStyle.Render("⚠ "+s.Source+" is not running — start the agent first") + "\n")
			if cmd := startCommand(s.Source); cmd != "" {
				b.WriteString("start it with:  " + helpStyle.Render(cmd) + "\n")
			}
			b.WriteString(dimStyle.Render(
				"then esc and press d again. reup never starts the agent itself.") + "\n")
			return b.String()
		}
		// The provider can bring its server up itself (kimi web, detached).
		b.WriteString(warnStyle.Render("⚠ "+s.Source+" is not running") + "\n")
		b.WriteString("Enter will start it automatically: " +
			helpStyle.Render(m.starterCommand()) + "\n")
		b.WriteString(dimStyle.Render(
			"detached — no terminal, no browser. It is stopped again afterwards.") + "\n")
		return b.String()
	}
	if m.mutCheck != nil {
		b.WriteString(warnStyle.Render("⚠ cannot manage this session:") + "\n")
		b.WriteString(cut(m.mutCheck.Error(), 52) + "\n")
		b.WriteString(dimStyle.Render(
			"reup only calls the official API, which refuses this one.") + "\n")
		return b.String()
	}
	line := "archive   (restorable — the agent can bring it back)"
	if m.mutCur == 0 {
		line = cursorStyle.Render("▌ ") + selectedStyle.Render(line)
	} else {
		line = "  " + line
	}
	b.WriteString(line + "\n")
	b.WriteString(dimStyle.Render("\nvia the agent's official API; storage files are never touched.") + "\n")
	b.WriteString(helpStyle.Render("enter confirm · esc cancel"))
	return b.String()
}

// applyProviderModal persists the temporary selection and applies it live.
func (m *Model) applyProviderModal() {
	var disabled []string
	for name, off := range m.provTemp {
		if off {
			disabled = append(disabled, name)
		}
	}
	sort.Strings(disabled)
	cfg, err := config.Load()
	if err != nil {
		cfg = config.Config{}
	}
	cfg.Disabled = disabled
	if err := config.Save(cfg); err != nil {
		m.warnings = append(m.warnings, "warning: could not save config: "+err.Error())
		return
	}
	provider.SetDisabled(cfg.Disabled)
	m.syncProviders()
}
