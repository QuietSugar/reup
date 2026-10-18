// Row building and rendering for the session list. A "row" is one visible
// line of the list panel: either a session or a workspace group header
// (group mode). The list component owns cursor/offset over these rows.
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/QuietSugar/reup/internal/cwd"
	"github.com/QuietSugar/reup/internal/session"
	"github.com/QuietSugar/reup/internal/textutil"
	"github.com/QuietSugar/reup/internal/workspace"
)

const (
	colAge   = 7
	colAgent = 9 // widest full provider name: "codebuddy" / "kimi-code"
)

type rowKind int

const (
	rowSession rowKind = iota
	rowGroup
)

type row struct {
	kind rowKind
	s    session.Session
	g    workspace.Group
}

// filterValue is the fuzzy-search surface of a session.
func filterValue(s *session.Session) string {
	return s.ProjectLabel + " " + s.FirstPrompt + " " + s.Title + " " + s.Subtitle
}

func sessionKey(s *session.Session) string { return s.Source + "\x00" + s.SessionID }

// workspacePath renders a workspace's directory for headers: progressively
// abbreviated to the given display budget, or the native id / placeholder
// when no directory is known.
func workspacePath(g workspace.Group, budget int) string {
	if g.Dir != "" {
		return textutil.ShortenPath(g.Dir, budget)
	}
	if g.WorkspaceID != "" {
		return g.WorkspaceID
	}
	return "(no workspace)"
}

func formatLastActivity(ts string, now time.Time) string {
	last, ok := textutil.ParseISO(ts)
	if !ok {
		return "?"
	}
	age := now.Sub(last)
	if age < time.Minute {
		return "now"
	}
	minutes := int(age / time.Minute)
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	hours := int(age / time.Hour)
	if hours < 24 {
		return fmt.Sprintf("%dh%dm", hours, minutes%60)
	}
	days := int(age / (24 * time.Hour))
	// Tiered units keep the string inside the fixed age column: beyond a
	// month the finer units stop carrying information.
	switch {
	case days < 30:
		return fmt.Sprintf("%dd%dh", days, hours%24)
	case days < 365:
		return fmt.Sprintf("%dmo", days/30)
	}
	years := days / 365
	if months := (days % 365) / 30; months > 0 {
		return fmt.Sprintf("%dy%dmo", years, months)
	}
	return fmt.Sprintf("%dy", years)
}

// sessionRow renders one session as a fixed-prefix line: cursor, age, agent,
// then a title that flexes to the remaining width, then the volume bar.
func (m Model) sessionRow(s *session.Session, selected bool, w int) string {
	// Volume bar leads the row: it is a fixed-width column, so putting it up
	// front keeps the wider columns from shifting it around.
	glyph := textutil.VolumeMarker(s.AsstCount + len(s.Prompts))
	marker := volumeStyle[glyph].Render(pad(glyph, 2))
	last := pad(formatLastActivity(s.LastTS, time.Now()), colAge)
	agent := badgeStyle[s.Source].Render(pad(s.Source, colAgent))

	fixed := 2 + 2 + 1 + colAge + 1 + colAgent + 1 // cursor + marker + sp + age + sp + agent + sp
	titleW := w - fixed
	if titleW < 8 {
		titleW = 8
	}
	title := strings.TrimSpace(s.Title)
	if title == "" {
		title = strings.TrimSpace(s.FirstPrompt)
	}
	if cwd.Missing(s) {
		title += " " + textutil.DirDeletedLabel
	}
	// Pad the title to its column so every row has identical width and the
	// panels never shift when the content changes.
	titleCol := pad(cut(title, titleW), titleW)
	if selected {
		titleCol = selectedStyle.Render(titleCol)
	}

	cursor := "  "
	if selected {
		cursor = cursorStyle.Render("▌ ")
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(fmt.Sprintf("%s%s %s %s %s",
		cursor, marker, dimStyle.Render(last), agent, titleCol))
}

// groupRow renders a workspace group header line (group mode only).
func (m Model) groupRow(g workspace.Group, w int) string {
	noun := "sessions"
	if len(g.Sessions) == 1 {
		noun = "session"
	}
	// The header has the whole list width minus the count prefix — give the
	// path as much room as actually fits instead of a fixed fraction.
	line := fmt.Sprintf("%d %s  ·  %s", len(g.Sessions), noun, workspacePath(g, max(20, w-16)))
	return lipgloss.NewStyle().MaxWidth(w).Render(groupHeaderStyle.Render(pad(cut(line, w), w)))
}
