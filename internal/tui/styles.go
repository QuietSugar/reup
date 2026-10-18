// Shared lipgloss styles and small rendering helpers for the picker panels.
package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/QuietSugar/reup/internal/textutil"
)

var (
	headerStyle      = lipgloss.NewStyle().Bold(true)
	helpStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	warnStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	groupHeaderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Bold(true)
	selectedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true)
	cursorStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("13"))
	normalStyle      = lipgloss.NewStyle()
	dimStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	metaLabelStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))  // cyan labels
	metaValueStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("15")) // bright values

	badgeStyle = map[string]lipgloss.Style{
		"codebuddy": lipgloss.NewStyle().Foreground(lipgloss.Color("6")), // cyan
		"kimi-code": lipgloss.NewStyle().Foreground(lipgloss.Color("5")), // magenta
		"opencode":  lipgloss.NewStyle().Foreground(lipgloss.Color("4")), // blue
		"dsh":       lipgloss.NewStyle().Foreground(lipgloss.Color("2")), // green
	}

	// volumeStyle is a heat ramp for the conversation-weight bar: the bigger
	// the session, the hotter the color.
	volumeStyle = map[string]lipgloss.Style{
		" ": normalStyle,
		"▁": dimStyle,
		"▄": lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		"█": lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true),
	}
)

// pad pads s to target display columns (no truncation).
func pad(s string, target int) string { return textutil.PadDisplay(s, target) }

// cut truncates s to max display columns (appends "…" when cut).
func cut(s string, max int) string { return textutil.TrimDisplay(s, max) }

// panelLine clips one panel line to w display columns without wrapping.
func panelLine(l string, w int) string {
	if w <= 0 {
		return ""
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(l)
}

// stripANSI removes ANSI escape sequences so text width can be measured.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			continue
		}
		i++ // skip ESC
		for i < len(s) {
			c := s[i]
			// CSI sequences end with a letter; OSC with BEL. Good enough for
			// the SGR codes lipgloss emits.
			if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == 0x07 {
				break
			}
			i++
		}
	}
	return b.String()
}

// lineWidth is the display width of possibly-colored text.
func lineWidth(s string) int { return textutil.DisplayWidth(stripANSI(s)) }

// wrapLines word-wraps each line to w display columns (ANSI-aware via
// lipgloss), so long values like titles and cwds show in full.
func wrapLines(lines []string, w int) []string {
	if w <= 0 {
		return lines
	}
	var out []string
	for _, l := range lines {
		if lineWidth(l) <= w {
			out = append(out, l)
			continue
		}
		out = append(out, strings.Split(lipgloss.NewStyle().Width(w).Render(l), "\n")...)
	}
	return out
}

// padLine clips l to w display columns and right-pads it to exactly w, so
// every line of a panel has identical width and the panel borders never move.
func padLine(l string, w int) string {
	if w <= 0 {
		return ""
	}
	dw := lineWidth(l)
	if dw > w {
		return panelLine(l, w)
	}
	if dw == w {
		return l
	}
	return l + strings.Repeat(" ", w-dw)
}
