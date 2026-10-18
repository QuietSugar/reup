// Navigation, panel layout, and preview helpers for the picker model.
package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/QuietSugar/reup/internal/render"
)

// --- navigation ------------------------------------------------------------

func (m Model) pageSize() int {
	h := m.listBodyHeight()
	if h < 1 {
		return 1
	}
	return h
}

// moveCursor steps the selection by delta session rows, skipping headers.
func (m *Model) moveCursor(delta int) {
	if len(m.selectable) == 0 {
		return
	}
	j := sort.SearchInts(m.selectable, m.cursor)
	exact := j < len(m.selectable) && m.selectable[j] == m.cursor
	var k int
	switch {
	case exact:
		k = j + delta
	case delta < 0:
		k = j - 1
	default:
		k = j
	}
	if k < 0 {
		k = 0
	}
	if k >= len(m.selectable) {
		k = len(m.selectable) - 1
	}
	m.cursor = m.selectable[k]
	m.rememberSelection()
	m.clampOffset()
	m.refreshPreview()
}

func (m *Model) jumpTo(end bool) {
	if len(m.selectable) == 0 {
		return
	}
	k := 0
	if end {
		k = len(m.selectable) - 1
	}
	m.cursor = m.selectable[k]
	m.rememberSelection()
	m.clampOffset()
	m.refreshPreview()
}

// clampOffset keeps the cursor inside the visible scroll window.
func (m *Model) clampOffset() {
	h := m.listBodyHeight()
	if h <= 0 {
		return
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	if max := len(m.rows) - h; m.offset > max {
		m.offset = max
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m *Model) refreshPreview() {
	s := m.selectedSession()
	if s == nil {
		m.preview.SetContent("")
		return
	}
	meta := append(render.MetaLines(s),
		"", helpStyle.Render(fmt.Sprintf("prompts: %d  ·  vv conversation", len(s.Prompts))))
	m.preview.SetContent(strings.Join(wrapLines(meta, m.preview.Width), "\n"))
	m.preview.GotoTop()
}

// --- panels & layout -------------------------------------------------------

// panelWidths resolves the effective panel widths for the current width and
// toggle state, guaranteeing the list keeps workable room.
func (m Model) panelWidths() (sideW, prevW, listW int) {
	listW = m.width
	if m.sidebarOn && m.width >= 100 {
		sideW = 20
		listW -= sideW + 1 // + separator
	}
	if m.previewOn && listW >= 90 {
		prevW = listW * 2 / 5
		if prevW > 42 {
			prevW = 42
		}
		if prevW < 30 {
			prevW = 30
		}
		listW -= prevW + 1 // + separator
	}
	if listW < 30 && prevW > 0 {
		listW += prevW + 1
		prevW = 0
	}
	if listW < 30 && sideW > 0 {
		listW += sideW + 1
		sideW = 0
	}
	return sideW, prevW, listW
}

func (m Model) mainHeight() int {
	h := m.height - 2 // top + bottom bars
	if len(m.warnings) > 0 {
		h--
	}
	if m.filtering {
		h--
	}
	if h < 1 {
		h = 1
	}
	return h
}

func (m Model) listBodyHeight() int { return m.mainHeight() - 1 } // column header line

func (m *Model) resize() {
	if m.width == 0 || m.height == 0 {
		return
	}
	_, prevW, _ := m.panelWidths()
	m.preview.Width = prevW
	if m.preview.Width < 10 {
		m.preview.Width = 10
	}
	m.preview.Height = m.mainHeight() - 2
	if m.preview.Height < 3 {
		m.preview.Height = 3
	}
	m.ready = true
	m.clampOffset()
}
