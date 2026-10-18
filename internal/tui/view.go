// View assembly for the picker: top bar with provider tabs, the collapsible
// sidebar (workspaces + agents), the session list, the detail panel, and the
// dynamic bottom bar.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/QuietSugar/reup/internal/provider"
	"github.com/QuietSugar/reup/internal/render"
)

func activeSourceLabel(m Model) string {
	if m.tabIdx > 0 && m.tabIdx < len(m.tabs) {
		return m.tabs[m.tabIdx]
	}
	return "all"
}

func topBar(m Model) string {
	var tabs []string
	for i, t := range m.tabs {
		label := t
		if t == "" {
			label = "All"
		}
		if i == m.tabIdx {
			tabs = append(tabs, selectedStyle.Render(fmt.Sprintf("[%s %d]", label, m.tabCounts[t])))
		} else {
			tabs = append(tabs, dimStyle.Render(fmt.Sprintf(" %s ", label)))
		}
	}
	left := "reup  " + strings.Join(tabs, " ")
	// Session/workspaces counts live in the sidebar; the right side only
	// shows the active source (plus the scan spinner while loading).
	right := "source: " + activeSourceLabel(m)
	if m.pending > 0 {
		right = m.spin.View() + " " + right
	}

	pad := m.width - lineWidth(left) - lineWidth(right)
	if pad < 1 {
		left = cut(left, m.width-lineWidth(right))
		pad = m.width - lineWidth(left) - lineWidth(right)
		if pad < 1 {
			pad = 1
		}
	}
	return left + strings.Repeat(" ", pad) + right
}

// sidebarLines renders the two sidebar sections: WORKSPACES (filter the list
// by workspace) and AGENTS (enable/disable providers).
func sidebarLines(m Model, w int) []string {
	lines := []string{headerStyle.Render("WORKSPACES")}
	for i, g := range m.groups {
		marker := "  "
		if m.wsFilter == g.Key {
			marker = "▸ "
		}
		label := cut(g.Label(), w-10)
		sel := m.focus == focusSidebar && m.sideCur == i
		line := marker + pad(label, w-10) + fmt.Sprintf("%2d", len(g.Sessions))
		if sel {
			line = selectedStyle.Render(line)
		}
		lines = append(lines, panelLine(line, w))
	}
	if len(m.groups) == 0 {
		lines = append(lines, dimStyle.Render("  (none)"))
	}
	lines = append(lines, "", headerStyle.Render("AGENTS"))
	for j, name := range m.allProviderNames() {
		idx := len(m.groups) + j
		check := "[ ]"
		if providerEnabled(name) {
			check = "[x]"
		}
		sel := m.focus == focusSidebar && m.sideCur == idx
		line := fmt.Sprintf("%s %s", check, name)
		if sel {
			line = selectedStyle.Render(line)
		}
		lines = append(lines, panelLine(line, w))
	}
	return lines
}

func providerEnabled(name string) bool {
	return provider.IsEnabled(name)
}

// previewLines renders the detail panel: a header line plus a brief session
// detail (field labels dim, values plain); long values wrap so nothing is
// lost.
func previewLines(m Model, w, h int) []string {
	lines := []string{headerStyle.Render("DETAIL")}
	s := m.selectedSession()
	if s == nil {
		lines = append(lines, dimStyle.Render("(no selection)"))
		return lines
	}
	inner := w - 2 // panel edge margin
	for _, raw := range render.MetaLines(s) {
		trimmed := strings.TrimSpace(raw)
		idx := strings.Index(trimmed, ":")
		if idx <= 0 {
			lines = append(lines, panelLine(trimmed, w))
			continue
		}
		label, value := trimmed[:idx+1], strings.TrimSpace(trimmed[idx+1:])
		head := metaLabelStyle.Render(" " + label)
		if lineWidth(label)+1+lineWidth(value) <= inner {
			// Label and value fit on one line.
			lines = append(lines, panelLine(head+" "+metaValueStyle.Render(value), w))
			continue
		}
		// Value too long: label line, then the value wrapped below.
		lines = append(lines, panelLine(head, w))
		for _, seg := range wrapLines([]string{value}, inner-2) {
			lines = append(lines, panelLine("  "+metaValueStyle.Render(seg), w))
		}
	}
	lines = append(lines, "", helpStyle.Render(
		fmt.Sprintf("prompts: %d  ·  vv conversation", len(s.Prompts))))
	_ = h
	return lines
}

func filterLine(m Model) string {
	return "/" + m.filterInput.View() + "  " +
		helpStyle.Render(fmt.Sprintf("%d matches", len(m.selectable)))
}

func bottomBar(m Model) string {
	var parts []string
	switch {
	case m.modal == modalDetail:
		parts = []string{"↑↓ scroll", "v / esc close"}
	case m.modal == modalMutate:
		switch {
		case m.mutBusy:
			parts = []string{"working…"}
		case !m.mutAvail && m.mutStarter != nil:
			parts = []string{"enter start + continue", "esc cancel"}
		case !m.mutAvail:
			parts = []string{"agent not running", "esc close"}
		case m.mutCheck != nil:
			parts = []string{"session not manageable", "esc close"}
		default:
			parts = []string{"↑↓ choose", "enter confirm", "esc cancel"}
		}
	case m.modal != modalNone:
		parts = []string{"↑↓ move", "space toggle", "enter apply", "esc close"}
	case m.filtering:
		parts = []string{"type to filter", "enter apply", "esc cancel"}
	case m.focus == focusSidebar:
		parts = []string{"↑↓ move", "space select", "w list", "esc back"}
	default:
		parts = []string{
			"↑↓ browse", "/ filter", "tab source", "w sidebar", "g group",
			"o sidebar", "v preview", "d hide", "ctrl-r rescan",
			"P providers", "? help", "enter resume", "esc quit",
		}
	}
	return helpStyle.Render(strings.Join(parts, " · "))
}

// renderPanel clips each line to w and pads every line to exactly w display
// columns (ANSI-aware), so panel borders never shift with the content.
func renderPanel(lines []string, w, h int) string {
	if h > 0 && len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	for i, l := range lines {
		lines[i] = padLine(l, w)
	}
	return strings.Join(lines, "\n")
}

func separatorLines(h int) string {
	lines := make([]string, h)
	for i := range lines {
		lines[i] = dimStyle.Render("│")
	}
	return strings.Join(lines, "\n")
}

// mainArea renders sidebar │ list │ preview for the current panel state.
func mainArea(m Model) string {
	sideW, prevW, listW := m.panelWidths()
	h := m.mainHeight()

	list := []string{columnHeader()}
	end := m.offset + h
	if end > len(m.rows) {
		end = len(m.rows)
	}
	for i := m.offset; i < end; i++ {
		r := m.rows[i]
		if r.kind == rowGroup {
			list = append(list, m.groupRow(r.g, listW))
		} else {
			list = append(list, m.sessionRow(&r.s, i == m.cursor, listW))
		}
	}

	out := renderPanel(list, listW, h)
	if sideW > 0 {
		side := renderPanel(sidebarLines(m, sideW), sideW, h)
		out = lipgloss.JoinHorizontal(lipgloss.Top, side, separatorLines(h), out)
	}
	if prevW > 0 {
		prev := renderPanel(previewLines(m, prevW, h), prevW, h)
		out = lipgloss.JoinHorizontal(lipgloss.Top, out, separatorLines(h), prev)
	}
	return out
}

// View renders the whole picker screen.
func (m Model) View() string {
	if !m.ready {
		return "loading…"
	}
	if m.modal != modalNone {
		return m.renderModal()
	}
	var b strings.Builder
	b.WriteString(topBar(m) + "\n")
	if len(m.warnings) > 0 {
		b.WriteString(warnStyle.Render(cut(strings.Join(m.warnings, " · "), m.width)) + "\n")
	}
	if m.filtering {
		b.WriteString(filterLine(m) + "\n")
	}
	b.WriteString(mainArea(m))
	b.WriteString("\n" + bottomBar(m))
	return b.String()
}

// columnHeader is the list panel's column header, aligned to sessionRow's
// fixed columns (2-col cursor prefix, then one space between columns).
func columnHeader() string {
	head := strings.Join([]string{
		pad("sz", 2),
		pad("age", colAge),
		pad("agent", colAgent),
	}, " ")
	return groupHeaderStyle.Render("  " + head + " title")
}
