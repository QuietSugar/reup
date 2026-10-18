// Package textutil holds shared formatters ported from the Python utils.py.
// Display-width handling is CJK-aware via go-runewidth (Korean text is a
// first-class use case).
package textutil

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
)

// ParseISO parses an RFC3339-ish timestamp. Returns ok=false on empty or
// unparseable input. Naive timestamps (no offset) are treated as UTC.
func ParseISO(ts string) (time.Time, bool) {
	if ts == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t, true
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, ts, time.UTC); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// FmtTS renders "YYYY-MM-DD HH:MM:SS" (or without year when includeYear is
// false). Pure string surgery, mirroring the Python implementation.
func FmtTS(ts string, includeYear bool) string {
	if ts == "" {
		return "?"
	}
	s := strings.Replace(ts, "T", " ", 1)
	if len(s) > 19 {
		s = s[:19]
	}
	if !includeYear && len(s) >= 10 && s[4] == '-' {
		return s[5:]
	}
	return s
}

// FmtDuration renders the gap between two timestamps as "<1min", "Nmin", "XhYm".
func FmtDuration(a, b string) string {
	da, okA := ParseISO(a)
	db, okB := ParseISO(b)
	if !okA || !okB {
		return "?"
	}
	mins := int(db.Sub(da).Minutes())
	if mins < 1 {
		return "<1min"
	}
	if mins < 60 {
		return fmt.Sprintf("%dmin", mins)
	}
	return fmt.Sprintf("%dh%dm", mins/60, mins%60)
}

func oneLine(txt string) string {
	r := strings.NewReplacer("\n", " ", "\r", " ", "\t", " ")
	return r.Replace(txt)
}

// Trim collapses whitespace control chars and cuts to limit code points
// (appending "…" when truncated). Counts runes, not display width.
func Trim(txt string, limit int) string {
	one := oneLine(txt)
	runes := []rune(one)
	if len(runes) <= limit {
		return one
	}
	cut := limit - 1
	if cut < 0 {
		cut = 0
	}
	return string(runes[:cut]) + "…"
}

// DisplayWidth is the terminal cell width of s (CJK chars count as 2).
func DisplayWidth(s string) int {
	return runewidth.StringWidth(s)
}

// TrimDisplay truncates to max display width, appending "…" when cut.
// Hand-rolled loop (not runewidth.Truncate) to mirror the Python boundary
// behavior exactly: accumulated width never exceeds maxW-1 before the "…".
func TrimDisplay(txt string, maxW int) string {
	one := oneLine(txt)
	if DisplayWidth(one) <= maxW {
		return one
	}
	var b strings.Builder
	w := 0
	for _, ch := range one {
		cw := runewidth.RuneWidth(ch)
		if w+cw > maxW-1 {
			break
		}
		b.WriteRune(ch)
		w += cw
	}
	return b.String() + "…"
}

// PadDisplay right-pads s with spaces to the target display width.
func PadDisplay(s string, targetW int) string {
	w := DisplayWidth(s)
	if w >= targetW {
		return s
	}
	return s + strings.Repeat(" ", targetW-w)
}

// DirDeletedLabel replaces the project column for a session whose recorded
// working directory no longer exists. Such a session cannot be resumed until
// the directory is recreated, so the row must not pass itself off as a live
// project — and it must not read as a bare "(unknown)" either. Parenthesized
// to match the providers' own "(unknown)" placeholder.
const DirDeletedLabel = "(deleted)"

// VolumeMarker is the fixed 1-col conversation weight bar. Block glyphs scale
// with the session size, so the level reads at a glance:
//
//	<20 messages   blank   (nothing worth glancing at)
//	20–49          ▁
//	50–149         ▄
//	150+           █
func VolumeMarker(totalMsgs int) string {
	switch {
	case totalMsgs < 20:
		return " "
	case totalMsgs < 50:
		return "▁"
	case totalMsgs < 150:
		return "▄"
	default:
		return "█"
	}
}

// defaultPathBudget is the display-width budget ShortenPath uses when the
// caller does not supply one.
const defaultPathBudget = 44

// ShortenPath abbreviates p to at most budget display columns, degrading
// progressively so the tail stays readable:
//
//   - paths of three components or fewer that already fit are unchanged;
//   - otherwise the last component is shown in full, its parent in up to four
//     characters, and components further back collapse progressively
//     (3, 2, then a single leading rune) behind an ellipsis;
//   - when that is still too wide, the parent tightens to three characters and
//     the earliest components are dropped one by one;
//   - as a last resort the final component itself is cut to the budget, so the
//     result never outgrows it no matter how deep the path.
func ShortenPath(p string, budget int) string {
	s := strings.TrimSpace(p)
	if s == "" {
		return ""
	}
	if budget <= 0 {
		budget = defaultPathBudget
	}
	clean := strings.TrimRight(s, "/")
	if clean == "" {
		return "/"
	}

	var parts []string
	for _, part := range strings.Split(strings.TrimPrefix(clean, "/"), "/") {
		if part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return "/"
	}
	// Abbreviation gains nothing on very short paths that already fit.
	if len(parts) <= 3 && DisplayWidth(clean) <= budget {
		return clean
	}

	// capAt limits a component's runes by its distance from the end (0 = last
	// component, unlimited). Tight mode trades the parent down to three runes
	// and everything older to one.
	// tier scores how aggressively components away from the end are capped:
	// relaxed keeps several runes per component (used when the budget
	// allows), normal is the historical balance, tight is one rune.
	capAt := func(dist int, tier int) int {
		if dist == 0 {
			return -1
		}
		if tier == 2 { // tight
			if dist == 1 {
				return 3
			}
			return 1
		}
		if tier == 0 { // relaxed
			switch {
			case dist == 1:
				return 8
			case dist == 2:
				return 6
			case dist == 3:
				return 4
			}
			return 2
		}
		switch dist { // normal
		case 1:
			return 4
		case 2:
			return 3
		case 3:
			return 2
		}
		return 1
	}

	// render joins the last keep components behind an ellipsis.
	render := func(keep int, tier int) (string, int) {
		var b strings.Builder
		b.WriteString("…/")
		w := DisplayWidth("…/")
		for i := len(parts) - keep; i < len(parts); i++ {
			part := parts[i]
			if dist := len(parts) - 1 - i; dist > 0 {
				part = cutRunes(part, capAt(dist, tier))
			}
			if i < len(parts)-1 {
				part += "/"
			}
			b.WriteString(part)
			w += DisplayWidth(part)
		}
		return b.String(), w
	}

	candidates := []struct {
		keep int
		tier int
	}{{len(parts), 0}, {len(parts), 1}, {len(parts), 2}}
	for keep := len(parts) - 1; keep >= 1; keep-- {
		candidates = append(candidates, struct {
			keep int
			tier int
		}{keep, 2})
	}
	for _, c := range candidates {
		if out, w := render(c.keep, c.tier); w <= budget {
			return out
		}
	}

	// Last resort: the final component alone, cut to what remains of the
	// budget — the result can then never outgrow it.
	prefix := "…/"
	rest := budget - DisplayWidth(prefix)
	if rest <= 0 {
		return "…"
	}
	return prefix + cutWidth(parts[len(parts)-1], rest)
}

// cutRunes cuts s to at most max runes.
func cutRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

// cutWidth cuts s to at most w display columns without adding an ellipsis.
func cutWidth(s string, w int) string {
	if DisplayWidth(s) <= w {
		return s
	}
	var b strings.Builder
	used := 0
	for _, ch := range s {
		cw := runewidth.RuneWidth(ch)
		if used+cw > w {
			break
		}
		b.WriteRune(ch)
		used += cw
	}
	return b.String()
}
