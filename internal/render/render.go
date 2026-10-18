// Package render holds the provider-agnostic renderers — index table, full
// detail box (also the TUI preview), and JSON. The list output keeps raw ANSI
// escapes (not lipgloss) so `reup list` stays byte-stable for scripts and
// the QA assertions; NO_COLOR is honored manually, as before.
package render

import (
	"fmt"
	"os"
	"strings"

	"github.com/QuietSugar/reup/internal/cwd"
	"github.com/QuietSugar/reup/internal/session"
	"github.com/QuietSugar/reup/internal/textutil"
	"github.com/QuietSugar/reup/internal/workspace"
)

const ansiReset = "\x1b[0m"

// BadgeANSI maps source → badge color. Kept here so the renderer does not
// import provider packages.
var BadgeANSI = map[string]string{
	"codebuddy": "\x1b[36m", // cyan
	"kimi-code": "\x1b[35m", // magenta
	"opencode":  "\x1b[34m", // blue
}

const (
	FirstPromptWidth = 78
	AuxWidth         = 46
)

func noColor() bool {
	return os.Getenv("NO_COLOR") != ""
}

// Badge renders a fixed-width badge like "[cb]   ", "[kimi] " or "[oc]   ", 7 visible cols.
func Badge(source, ansi string) string {
	text := "[" + source + "]"
	switch source {
	case "codebuddy":
		text = "[cb]"
	case "kimi-code":
		text = "[kimi]"
	case "opencode":
		text = "[oc]"
	}
	padded := textutil.PadDisplay(text, 7)
	if noColor() {
		return padded
	}
	return ansi + padded + ansiReset
}

// FmtLastShort renders the "MM-DD HH:MM:SS" form used in the index table.
func FmtLastShort(ts string) string {
	return textutil.FmtTS(ts, false)
}

// indexHeader is the single column header shared by the flat and grouped
// index tables.
func indexHeader() string {
	return fmt.Sprintf(
		"%-17s %-7s %-25s %-3s  %s",
		"last_activity", "src", "project", "mk", "first prompt")
}

// indexRow renders one session as a single table row. Kept separate so the
// flat and grouped tables cannot drift apart.
func indexRow(s *session.Session) string {
	last := textutil.PadDisplay(FmtLastShort(s.LastTS), 17)
	badge := Badge(s.Source, BadgeANSI[s.Source])
	projLabel := s.ProjectLabel
	if cwd.Missing(s) {
		projLabel = textutil.DirDeletedLabel
	}
	proj := textutil.PadDisplay(textutil.TrimDisplay(projLabel, 25), 25)
	msgs := s.AsstCount + len(s.Prompts)
	markers := volumeANSI(textutil.VolumeMarker(msgs)) + "  "
	first := textutil.TrimDisplay(s.FirstPrompt, FirstPromptWidth)
	firstPadded := textutil.PadDisplay(first, FirstPromptWidth)
	aux := ""
	if s.Title != "" {
		aux = textutil.TrimDisplay(s.Title, AuxWidth)
	} else if s.Subtitle != "" {
		aux = textutil.TrimDisplay(s.Subtitle, AuxWidth)
	}
	label := firstPadded
	if aux != "" {
		label = firstPadded + "  " + aux
	}
	return fmt.Sprintf("%s %s %s %s  %s", last, badge, proj, markers, label)
}

// volumeANSI colors the conversation-weight bar as a heat ramp — gray for
// small, yellow for medium, bold red for large. Plain glyph under NO_COLOR.
func volumeANSI(glyph string) string {
	if noColor() {
		return glyph
	}
	switch glyph {
	case "▁":
		return "\x1b[90m" + glyph + ansiReset
	case "▄":
		return "\x1b[33m" + glyph + ansiReset
	case "█":
		return "\x1b[31m\x1b[1m" + glyph + ansiReset
	}
	return glyph
}

// Index renders the compact one-line-per-session table (flat, ungrouped).
func Index(sessions []session.Session) string {
	if len(sessions) == 0 {
		return "(no sessions)"
	}
	out := []string{indexHeader(), strings.Repeat("─", 140)}
	for i := range sessions {
		out = append(out, indexRow(&sessions[i]))
	}
	return strings.Join(out, "\n")
}

// IndexGrouped renders the index table with a header line per workspace,
// merging sessions that share a working directory across providers.
func IndexGrouped(sessions []session.Session) string {
	if len(sessions) == 0 {
		return "(no sessions)"
	}
	blocks := make([]string, 0)
	for _, g := range workspace.GroupBy(sessions) {
		lines := []string{groupHeader(g)}
		for i := range g.Sessions {
			lines = append(lines, indexRow(&g.Sessions[i]))
		}
		blocks = append(blocks, strings.Join(lines, "\n"))
	}
	out := []string{indexHeader(), strings.Repeat("─", 140), strings.Join(blocks, "\n\n")}
	return strings.Join(out, "\n")
}

// groupHeader labels one workspace block: the directory (or native id) plus its
// session count and the providers that contributed to it.
func groupHeader(g workspace.Group) string {
	label := g.Dir
	if label == "" {
		label = g.WorkspaceID
	}
	if label == "" {
		label = "(no workspace)"
	}
	noun := "sessions"
	if len(g.Sessions) == 1 {
		noun = "session"
	}
	deleted := ""
	if len(g.Sessions) > 0 && cwd.Missing(&g.Sessions[0]) {
		deleted = "  (deleted)"
	}
	return fmt.Sprintf("── %s  ·  %d %s  ·  %s%s",
		label, len(g.Sessions), noun, strings.Join(g.Providers, ", "), deleted)
}

// MetaLines renders a session's identity/timing metadata: no prompt content.
// The picker's detail panel shows these; the conversation itself has its own
// view (ConversationLines) because it can be arbitrarily long.
func MetaLines(s *session.Session) []string {
	dir := s.Cwd
	switch {
	case dir == "":
		dir = "(none)"
	case cwd.Missing(s):
		dir += "  (directory no longer exists — recreate it before resuming)"
	}
	lines := []string{
		fmt.Sprintf(" source:         [%s]", s.Source),
		fmt.Sprintf(" project:        %s", s.ProjectLabel),
		fmt.Sprintf(" session id:     %s", s.SessionID),
		fmt.Sprintf(" started:        %s", textutil.FmtTS(s.FirstTS, true)),
		fmt.Sprintf(" last activity:  %s", textutil.FmtTS(s.LastTS, true)),
		fmt.Sprintf(" duration:       %s", textutil.FmtDuration(s.FirstTS, s.LastTS)),
		fmt.Sprintf(" cwd:            %s", dir),
		fmt.Sprintf(" activity:       %d user prompts / ~%d assistant activity", len(s.Prompts), s.AsstCount),
	}
	if s.Title != "" {
		lines = append(lines, fmt.Sprintf(" title:          %s", s.Title))
	}
	if s.Subtitle != "" {
		lines = append(lines, fmt.Sprintf(" context:        %s", s.Subtitle))
	}
	return lines
}

// ConversationLines renders the session's prompts as a numbered list, one
// entry per prompt (timestamps included when known). Long lines are wrapped
// by the caller to its panel width.
func ConversationLines(s *session.Session) []string {
	lines := make([]string, 0, len(s.Prompts))
	for i, p := range s.Prompts {
		stamp := ""
		if _, ok := textutil.ParseISO(p.TS); ok {
			stamp = textutil.FmtTS(p.TS, false) + "  "
		}
		lines = append(lines, fmt.Sprintf("[%d] %s%s", i+1, stamp, textutil.Trim(p.Text, 400)))
	}
	return lines
}

// DetailLines composes metadata plus the first/last prompts; negative first or
// last means "show every prompt". Used by `list --full` and the detail panel.
func DetailLines(s *session.Session, first, last int) []string {
	lines := MetaLines(s)
	prompts := ConversationLines(s)
	total := len(prompts)
	if total == 0 {
		if s.FirstPrompt != "" {
			lines = append(lines, " prompts", "  "+textutil.Trim(s.FirstPrompt, 350))
		}
		return lines
	}
	lines = append(lines, " prompts")
	if first < 0 || last < 0 || first+last >= total {
		lines = append(lines, prompts...)
		return lines
	}
	lines = append(lines, prompts[:first]...)
	lines = append(lines, fmt.Sprintf("  … %d more", total-first-last))
	lines = append(lines, prompts[total-last:]...)
	return lines
}

// FullBox renders the single-session detail box (used by `list --full`).
func FullBox(s *session.Session) string {
	const width = 72
	bar := strings.Repeat("─", width)
	lines := []string{"┌" + bar}
	for _, l := range DetailLines(s, 3, 2) {
		lines = append(lines, "│"+l)
	}
	lines = append(lines, "└"+bar)
	return strings.Join(lines, "\n")
}
