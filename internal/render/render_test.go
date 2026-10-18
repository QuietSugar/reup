package render

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuietSugar/reup/internal/session"
	"github.com/QuietSugar/reup/internal/textutil"
)

func TestFullBoxShowsFirstPromptWhenPromptListIsEmpty(t *testing.T) {
	box := FullBox(&session.Session{FirstPrompt: "Show this in the detail preview"})
	if !strings.Contains(box, "Show this in the detail preview") {
		t.Fatalf("detail box is missing first prompt:\n%s", box)
	}
}

func TestFullBoxLabelsAssistantActivityAsApproximate(t *testing.T) {
	box := FullBox(&session.Session{
		Prompts:   []session.Prompt{{Text: "one"}, {Text: "two"}},
		AsstCount: 7,
	})
	if !strings.Contains(box, "2 user prompts / ~7 assistant activity") {
		t.Fatalf("detail box does not mark assistant activity as approximate:\n%s", box)
	}
}

func TestRenderersOmitTokenAndCacheStatistics(t *testing.T) {
	sessions := []session.Session{{
		Source: "codebuddy", SessionID: "fixture-session", ProjectLabel: "fixture",
	}}
	outputs := []string{
		Index(sessions),
		FullBox(&sessions[0]),
		JSON(sessions),
	}
	for _, output := range outputs {
		lower := strings.ToLower(output)
		if strings.Contains(lower, "token") || strings.Contains(lower, "cache hit") {
			t.Errorf("render output still contains usage statistics:\n%s", output)
		}
	}
}

func TestIndexGroupedMergesByDirectory(t *testing.T) {
	shared := t.TempDir()
	sessions := []session.Session{
		{Source: "kimi-code", SessionID: "k1", ProjectLabel: filepath.Base(shared), Cwd: shared, FirstPrompt: "from kimi", LastTS: "2026-04-15T10:00:00Z"},
		{Source: "opencode", SessionID: "o1", ProjectLabel: filepath.Base(shared), Cwd: shared, FirstPrompt: "from opencode", LastTS: "2026-04-15T09:00:00Z"},
		{Source: "kimi-code", SessionID: "k2", ProjectLabel: "other", Cwd: t.TempDir(), FirstPrompt: "elsewhere", LastTS: "2026-04-15T08:00:00Z"},
	}
	out := IndexGrouped(sessions)
	if n := strings.Count(out, "── "); n != 2 {
		t.Fatalf("want 2 workspace headers, got %d:\n%s", n, out)
	}
	if !strings.Contains(out, "2 sessions") {
		t.Errorf("merged group should report 2 sessions:\n%s", out)
	}
	if !strings.Contains(out, "kimi-code, opencode") {
		t.Errorf("merged header should list both providers, sorted:\n%s", out)
	}
	if !strings.Contains(out, "1 session  ·  kimi-code") {
		t.Errorf("single-member group should use the singular noun:\n%s", out)
	}
	// The two merged rows must sit under the first header, before the second.
	firstHdr := strings.Index(out, "── "+shared)
	secondHdr := strings.LastIndex(out, "\n── ")
	if firstHdr < 0 || secondHdr < 0 || firstHdr > secondHdr {
		t.Fatalf("unexpected group layout:\n%s", out)
	}
	if !strings.Contains(out[firstHdr:secondHdr], "from kimi") ||
		!strings.Contains(out[firstHdr:secondHdr], "from opencode") {
		t.Fatalf("both merged rows must sit under the shared header:\n%s", out)
	}
}

func TestIndexGroupedEmptyAndUnknown(t *testing.T) {
	if got := IndexGrouped(nil); got != "(no sessions)" {
		t.Errorf("empty grouped index = %q", got)
	}
	// A session with no directory and no native id lands under a header that
	// does not pretend to have a workspace.
	out := IndexGrouped([]session.Session{{Source: "kimi-code", SessionID: "x"}})
	if !strings.Contains(out, "── (no workspace)") {
		t.Errorf("unknown workspace header missing:\n%s", out)
	}
}

func TestRenderersFlagMissingWorkingDirectory(t *testing.T) {
	gone := session.Session{
		Source: "kimi-code", SessionID: "s1", ProjectLabel: "old-project",
		Cwd: filepath.Join(t.TempDir(), "deleted-project"), FirstPrompt: "hello",
	}
	idx := Index([]session.Session{gone})
	// The project column must say the directory is gone, not pass the stale
	// name off as a live project.
	if !strings.Contains(idx, textutil.DirDeletedLabel) {
		t.Errorf("index row should mark the deleted directory:\n%s", idx)
	}
	if strings.Contains(idx, "old-project") {
		t.Errorf("index row should not show the stale project name:\n%s", idx)
	}
	box := FullBox(&gone)
	if !strings.Contains(box, "directory no longer exists") {
		t.Errorf("detail box should mark the deleted directory:\n%s", box)
	}
	if !strings.Contains(box, gone.Cwd) {
		t.Errorf("detail box should still show the missing path:\n%s", box)
	}

	alive := session.Session{
		Source: "kimi-code", SessionID: "s2", ProjectLabel: "live-project",
		Cwd: t.TempDir(), FirstPrompt: "hello",
	}
	if idx := Index([]session.Session{alive}); strings.Contains(idx, textutil.DirDeletedLabel) {
		t.Errorf("a session whose cwd exists must not be flagged:\n%s", idx)
	}
	if box := FullBox(&alive); strings.Contains(box, "directory no longer exists") {
		t.Errorf("a session whose cwd exists must not be flagged:\n%s", box)
	}

	// The grouped header identifies the workspace by its (stale) directory, so
	// it must carry the deleted marker too.
	if grp := IndexGrouped([]session.Session{gone}); !strings.Contains(grp, textutil.DirDeletedLabel) {
		t.Errorf("grouped header should mark the deleted workspace:\n%s", grp)
	}
	if grp := IndexGrouped([]session.Session{alive}); strings.Contains(grp, textutil.DirDeletedLabel) {
		t.Errorf("grouped header for an existing directory must not be flagged:\n%s", grp)
	}
}
