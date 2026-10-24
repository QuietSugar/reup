package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Mouse support: click the top tabs to switch provider source, the sidebar
// rows to filter by workspace (second click on an agent toggles it), a list
// row to move the cursor (double-click resumes), and wheel-scroll the list.
// Trade-off of the enabled mouse tracking: text selection now needs Shift.

const doubleClickWindow = 600 * time.Millisecond

// tabAt maps an x coordinate on the top bar to a tab index, mirroring
// topBar's geometry: "reup  " prefix, then tabs joined by single spaces
// (selected tabs render as "[name count]", others as " name ").
func tabAt(m Model, x int) int {
	const prefix = 9 // len("reup  ")
	if x < prefix {
		return -1
	}
	pos := prefix
	for i, t := range m.tabs {
		label := t
		if t == "" {
			label = "All"
		}
		w := len(label) + 2
		if i == m.tabIdx {
			w += 1 + len(fmt.Sprint(m.tabCounts[t])) // " name N"
		}
		if x < pos+w {
			return i
		}
		pos += w + 1 // join space
	}
	return -1
}

// handleMouseClick routes a left press to the region under the cursor.
func (m *Model) handleMouseClick(x, y int) (tea.Model, tea.Cmd) {
	mainTop := 1 // top bar
	if len(m.warnings) > 0 {
		mainTop++
	}
	if m.filtering {
		mainTop++
	}
	if y < mainTop {
		if i := tabAt(*m, x); i >= 0 {
			// Clicking the tab bar hands focus back to the list: a tab
			// switch from the sidebar would otherwise leave focus there,
			// making j/k/enter drive the sidebar instead of the sessions.
			m.focus = focusList
			if i != m.tabIdx {
				m.tabIdx = i
				m.rebuild()
			}
		}
		return m, nil
	}
	if y >= mainTop+m.mainHeight() {
		return m, nil // bottom bar: nothing clickable yet
	}
	li := y - mainTop
	sideW, prevW, listW := m.panelWidths()
	_ = prevW
	if sideW > 0 && x <= sideW {
		return m.sidebarClick(li)
	}
	listX := 0
	if sideW > 0 {
		listX = sideW + 1
	}
	if x >= listX && x < listX+listW {
		// Same focus rule as the tab bar: interacting with the session
		// list means keyboard navigation should drive it.
		m.focus = focusList
		return m.listClick(li)
	}
	_ = prevW
	return m, nil // preview column: nothing clickable yet
}

// sidebarClick maps a click inside the sidebar column. Line 0 is the
// WORKSPACES header; group rows follow, then a blank line, the AGENTS header,
// and the agent rows.
func (m *Model) sidebarClick(li int) (tea.Model, tea.Cmd) {
	if li == 0 {
		return m, nil
	}
	if li <= len(m.groups) {
		i := li - 1
		m.focus = focusSidebar
		m.sideCur = i
		key := m.groups[i].Key
		if m.wsFilter == key {
			m.wsFilter = ""
		} else {
			m.wsFilter = key
		}
		m.rebuild()
		return m, nil
	}
	agentIdx := li - len(m.groups) - 3 // blank line + AGENTS header
	names := m.allProviderNames()
	if agentIdx < 0 || agentIdx >= len(names) {
		return m, nil
	}
	m.focus = focusSidebar
	m.sideCur = len(m.groups) + agentIdx
	name := names[agentIdx]
	now := time.Now()
	// Agents toggle on a second click: a single click only selects, so an
	// accidental click never disables a provider.
	if m.lastClickKey == "agent:"+name && now.Sub(m.lastClickAt) <= doubleClickWindow {
		m.lastClickKey, m.lastClickAt = "", time.Time{}
		cmd := m.toggleAgent(name)
		return m, cmd
	}
	m.lastClickKey, m.lastClickAt = "agent:"+name, now
	return m, nil
}

// listClick maps a click inside the session list column (line 0 is the
// column header). A click moves the cursor to the row; a second click on the
// same session row within the double-click window resumes it.
func (m *Model) listClick(li int) (tea.Model, tea.Cmd) {
	if li == 0 {
		return m, nil // column header
	}
	row := m.offset + li - 1
	if row < 0 || row >= len(m.rows) {
		return m, nil
	}
	r := m.rows[row]
	m.cursor = row
	m.clampOffset()
	if r.kind != rowSession {
		m.lastClickRow, m.lastClickKey = -1, ""
		return m, nil
	}
	now := time.Now()
	if m.lastClickRow == row && now.Sub(m.lastClickAt) <= doubleClickWindow {
		m.selected = &m.rows[row].s
		m.lastClickRow, m.lastClickAt = -1, time.Time{}
		return m, tea.Quit
	}
	m.lastClickRow, m.lastClickAt = row, now
	return m, nil
}

// handleMouseWheel scrolls the session list with the wheel.
func (m *Model) handleMouseWheel(up bool) tea.Cmd {
	if up {
		m.moveCursor(-3)
	} else {
		m.moveCursor(3)
	}
	return nil
}
