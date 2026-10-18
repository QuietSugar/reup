package textutil

import (
	"strings"
	"testing"
)

func TestFmtTS(t *testing.T) {
	if got := FmtTS("2026-04-15T01:00:05.000Z", true); got != "2026-04-15 01:00:05" {
		t.Errorf("with year: %q", got)
	}
	if got := FmtTS("2026-04-15T01:00:05.000Z", false); got != "04-15 01:00:05" {
		t.Errorf("without year: %q", got)
	}
	if got := FmtTS("", true); got != "?" {
		t.Errorf("empty: %q", got)
	}
}

func TestFmtDuration(t *testing.T) {
	cases := []struct {
		a, b, want string
	}{
		{"2026-04-15T01:00:00Z", "2026-04-15T01:00:30Z", "<1min"},
		{"2026-04-15T01:00:00Z", "2026-04-15T01:05:00Z", "5min"},
		{"2026-04-15T01:00:00Z", "2026-04-15T02:30:00Z", "1h30m"},
		{"", "2026-04-15T01:00:00Z", "?"},
	}
	for _, c := range cases {
		if got := FmtDuration(c.a, c.b); got != c.want {
			t.Errorf("FmtDuration(%q, %q) = %q, want %q", c.a, c.b, got, c.want)
		}
	}
}

func TestTrim(t *testing.T) {
	if got := Trim("a\nb\tc", 10); got != "a b c" {
		t.Errorf("whitespace collapse: %q", got)
	}
	if got := Trim("abcdefghij", 5); got != "abcd…" {
		t.Errorf("truncate: %q", got)
	}
	// Rune-based, not byte-based: Korean chars count as 1 each.
	if got := Trim("가나다라마바사", 5); got != "가나다라…" {
		t.Errorf("korean truncate: %q", got)
	}
}

func TestDisplayWidthKorean(t *testing.T) {
	if got := DisplayWidth("가나다"); got != 6 {
		t.Errorf("DisplayWidth(가나다) = %d, want 6", got)
	}
	if got := DisplayWidth("abc"); got != 3 {
		t.Errorf("DisplayWidth(abc) = %d, want 3", got)
	}
}

func TestTrimDisplay(t *testing.T) {
	if got := TrimDisplay("hello", 10); got != "hello" {
		t.Errorf("no trim: %q", got)
	}
	// 가나다라 = width 8 > 6; cut so width ≤ 5 then append …
	got := TrimDisplay("가나다라", 6)
	if got != "가나…" {
		t.Errorf("korean trim: %q", got)
	}
	if w := DisplayWidth(got); w > 6 {
		t.Errorf("trimmed width %d exceeds max 6", w)
	}
}

func TestPadDisplay(t *testing.T) {
	if got := PadDisplay("가나", 6); got != "가나  " {
		t.Errorf("pad korean: %q", got)
	}
	if got := PadDisplay("abcdef", 3); got != "abcdef" {
		t.Errorf("no pad when over: %q", got)
	}
}

func TestVolumeMarker(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{{5, " "}, {20, "▁"}, {49, "▁"}, {50, "▄"}, {149, "▄"}, {150, "█"}}
	for _, c := range cases {
		if got := VolumeMarker(c.in); got != c.want {
			t.Errorf("VolumeMarker(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestShortenPath(t *testing.T) {
	cases := []struct {
		in     string
		budget int
		want   string
	}{
		{"", 44, ""},
		{"/", 44, "/"},
		{"/ws/a", 44, "/ws/a"},   // short paths stay readable
		{"/a/b/c", 44, "/a/b/c"}, // three components: unchanged
		// Progressive: parent gets 4 chars, then 3 / 2 / 1 the further back.
		{"/home/user/git-repo/github.com/QuietSugar/reup", 44, "…/ho/us/git-/github/QuietSug/reup"},
		{"/home/user/working/project/infra-notes", 44, "…/ho/user/workin/project/infra-notes"},
		// Too wide: the parent tightens to 3 chars, then the earliest
		// components are dropped until it fits.
		{"/home/user/working/project/infra-notes", 15, "…/infra-notes"},
		// A giant final component is cut to the budget as a last resort.
		{"/x/y/" + strings.Repeat("a", 40), 15, "…/" + strings.Repeat("a", 13)},
	}
	for _, c := range cases {
		if got := ShortenPath(c.in, c.budget); got != c.want {
			t.Errorf("ShortenPath(%q, %d) = %q, want %q", c.in, c.budget, got, c.want)
		}
	}
	// The result never exceeds the budget, and the final component survives
	// whenever it can.
	got := ShortenPath("/home/user/git-repo/github.com/QuietSugar/reup", 44)
	if DisplayWidth(got) > 44 {
		t.Errorf("result outgrew the budget: %q (%d cols)", got, DisplayWidth(got))
	}
	if !strings.HasSuffix(got, "/reup") {
		t.Errorf("last component lost: %q", got)
	}
}

func TestParseISO(t *testing.T) {
	if _, ok := ParseISO("2026-04-15T01:00:05.000Z"); !ok {
		t.Error("RFC3339 with millis+Z should parse")
	}
	if _, ok := ParseISO("2026-04-15T10:00:05+09:00"); !ok {
		t.Error("offset form should parse")
	}
	if _, ok := ParseISO(""); ok {
		t.Error("empty must not parse")
	}
	if _, ok := ParseISO("not-a-date"); ok {
		t.Error("garbage must not parse")
	}
}
