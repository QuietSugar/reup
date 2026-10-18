package codebuddy

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/QuietSugar/reup/internal/session"
)

func fixtureRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "..", "tests", "fixtures", "codebuddy")
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// copyTree copies a fixture into a temp directory so a test can mutate it
// without touching what `go test ./...` reads.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func listAll(t *testing.T) map[string]session.Session {
	t.Helper()
	t.Setenv("REUP_CODEBUDDY_HOME", fixtureRoot(t))
	p := New()
	sessions, err := p.ListSessions(session.Filters{AllTime: true, Days: -1})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]session.Session{}
	for _, s := range sessions {
		out[s.SessionID] = s
	}
	return out
}

func TestFixtureParsing(t *testing.T) {
	sessions := listAll(t)
	// Two fixture sessions: the healthy alpha one and the stale-cwd vault one.
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d, want 2", len(sessions))
	}

	got := sessions["cb111111-1111-4111-8111-111111111111"]
	if got.Source != "codebuddy" {
		t.Errorf("source = %q", got.Source)
	}
	if got.ProjectLabel != "codebuddy-alpha" || got.Cwd != "/tmp/reup/fixtures/codebuddy-alpha" {
		t.Errorf("project/cwd = %q / %q", got.ProjectLabel, got.Cwd)
	}
	// The encoded project bucket is the native workspace id.
	if got.WorkspaceID != "-tmp-reup-fixtures-codebuddy-alpha" {
		t.Errorf("workspace id = %q", got.WorkspaceID)
	}
	if got.Title != "CodeBuddy fixture session" {
		t.Errorf("title = %q", got.Title)
	}
	if got.FirstPrompt != "synthetic first prompt" || got.LastPrompt != "synthetic second prompt" || len(got.Prompts) != 2 {
		t.Errorf("prompts = %+v", got.Prompts)
	}
	if got.AsstCount != 1 {
		t.Errorf("assistant count = %d, want 1", got.AsstCount)
	}
	if got.FirstTS != "2026-04-15T05:00:00Z" || got.LastTS != "2026-04-15T05:05:07Z" {
		t.Errorf("timestamps = %q .. %q", got.FirstTS, got.LastTS)
	}
	wantArgv := []string{"codebuddy", "--resume", got.SessionID}
	if strings.Join(got.ResumeArgv, " ") != strings.Join(wantArgv, " ") {
		t.Errorf("resume argv = %v", got.ResumeArgv)
	}

	// The vault session carries a deliberately bogus stored cwd; its workspace
	// id still comes from the bucket it lives in.
	stale := sessions["cb222222-2222-4222-8222-222222222222"]
	if stale.Cwd != "/bogus/wrong/path" {
		t.Errorf("stale cwd = %q", stale.Cwd)
	}
	if stale.WorkspaceID != "-tmp-reup-fixtures-obsidian-path-with-space-vault" {
		t.Errorf("stale workspace id = %q", stale.WorkspaceID)
	}
	if stale.Title != "stale cwd regression" {
		t.Errorf("stale title = %q", stale.Title)
	}
}

func TestExtractTextContentBlockTypes(t *testing.T) {
	for _, typ := range []string{"text", "input_text", "output_text"} {
		t.Run(typ, func(t *testing.T) {
			got := extractText([]byte(`[ {"type":"` + typ + `","text":"hello"} ]`))
			if got != "hello" {
				t.Fatalf("extractText() = %q, want hello", got)
			}
		})
	}
}

func TestProjectAndDateFilters(t *testing.T) {
	t.Setenv("REUP_CODEBUDDY_HOME", fixtureRoot(t))
	p := New()

	matched, err := p.ListSessions(session.Filters{AllTime: true, Days: -1, Project: "BUDDY-ALPHA"})
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 {
		t.Fatalf("project filter returned %d sessions, want 1", len(matched))
	}

	missing, err := p.ListSessions(session.Filters{AllTime: true, Days: -1, Project: "does-not-match"})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("non-matching project filter returned %d sessions", len(missing))
	}

	byDate, err := p.ListSessions(session.Filters{Date: "2026-04-15", Days: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(byDate) != 2 {
		t.Fatalf("date filter returned %d sessions, want 2", len(byDate))
	}
}

func TestAvailabilityAndDetailLookup(t *testing.T) {
	t.Setenv("REUP_CODEBUDDY_HOME", fixtureRoot(t))
	p := New()
	if !p.IsAvailable() {
		t.Fatal("fixture projects directory should be available")
	}
	got, err := p.LoadDetail("cb111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Title != "CodeBuddy fixture session" {
		t.Fatalf("detail lookup = %+v", got)
	}
}

// TestNoTimeFilterByDefault locks in the CLI default: Days == 0 (the zero
// value) must impose no time limit. The fixtures are dated 2026-04-15, far
// outside any recent window, so a stale default would silently drop them all.
func TestNoTimeFilterByDefault(t *testing.T) {
	t.Setenv("REUP_CODEBUDDY_HOME", fixtureRoot(t))
	p := New()
	sessions, err := p.ListSessions(session.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("zero-value filter listed %d sessions, want 2 (no time limit)", len(sessions))
	}
}

// Sub-agent traffic lands in the same file as the main conversation, flagged
// with isSidechain. It is not part of this session, so it must not inflate the
// prompt list, the assistant-turn estimate, or the activity window.
func TestSidechainRecordsDoNotCount(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codebuddy")
	copyTree(t, fixtureRoot(t), home)

	target := filepath.Join(home, "projects", "-tmp-reup-fixtures-codebuddy-alpha",
		"cb111111-1111-4111-8111-111111111111.jsonl")
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("fixture session file: %v", err)
	}
	f, err := os.OpenFile(target, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	// Epoch ms after the session's own last record (1776229507), so a leak
	// would also move the activity window.
	sidechain := `{"type":"message","role":"user","timestamp":1776230000000,"sessionId":"cb111111-1111-4111-8111-111111111111","cwd":"/tmp/reup/fixtures/codebuddy-alpha","isSidechain":true,"message":{"content":[{"type":"input_text","text":"SUBAGENT PROMPT MUST NOT COUNT"}]}}
{"type":"message","role":"assistant","timestamp":1776230005000,"sessionId":"cb111111-1111-4111-8111-111111111111","isSidechain":true,"message":{"content":[{"type":"output_text","text":"subagent reply"}]}}
`
	if _, err := f.WriteString(sidechain); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("REUP_CODEBUDDY_HOME", home)
	p := New()
	sessions, err := p.ListSessions(session.Filters{AllTime: true, Days: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d, want 2", len(sessions))
	}
	var one session.Session
	for _, s := range sessions {
		if s.SessionID == "cb111111-1111-4111-8111-111111111111" {
			one = s
			break
		}
	}
	if one.SessionID == "" {
		t.Fatal("main session missing")
	}
	if len(one.Prompts) != 2 {
		t.Errorf("prompts = %d, want 2 — a sidechain user record was counted", len(one.Prompts))
	}
	if one.AsstCount != 1 {
		t.Errorf("asst count = %d, want 1 — a sidechain assistant record was counted", one.AsstCount)
	}
	if one.LastTS != "2026-04-15T05:05:07Z" {
		t.Errorf("last ts = %q, want the main session's own 2026-04-15T05:05:07Z", one.LastTS)
	}
	for _, pr := range one.Prompts {
		if strings.Contains(pr.Text, "SUBAGENT") {
			t.Errorf("sidechain prompt leaked into the prompt list: %q", pr.Text)
		}
	}
}
