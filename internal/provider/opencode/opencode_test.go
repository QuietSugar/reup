package opencode

import (
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/QuietSugar/reup/internal/session"
	_ "modernc.org/sqlite"
)

func fixtureDir(t *testing.T, parts ...string) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(append([]string{filepath.Dir(thisFile), "..", "..", "..", "tests", "fixtures"}, parts...)...)
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

func newProvider(t *testing.T) *Provider {
	t.Helper()
	t.Setenv(envData, fixtureDir(t, "opencode-home"))
	t.Setenv(envBin, filepath.Join(fixtureDir(t, "..", "mock-bin", "opencode")))
	return New()
}

func listAll(t *testing.T) map[string]session.Session {
	t.Helper()
	p := newProvider(t)
	sessions, err := p.ListSessions(session.Filters{AllTime: true, Days: -1})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]session.Session{}
	for _, s := range sessions {
		if _, dup := out[s.SessionID]; dup {
			t.Fatalf("duplicate session %s after dedupe", s.SessionID)
		}
		out[s.SessionID] = s
	}
	return out
}

func TestSQLiteParsing(t *testing.T) {
	sessions := listAll(t)
	// session_v2 holds 6 rows; archived + child are skipped and the legacy-only
	// decoy never appears, leaving 4 roots.
	if len(sessions) != 4 {
		t.Fatalf("expected 4 sessions (4 session_v2 roots), got %d: %v",
			len(sessions), sessions)
	}

	one := sessions["ses_aaaaaaaaaaaaaaaaaaaaaaaaaaaa"]
	if one.Source != "opencode" {
		t.Errorf("source = %q", one.Source)
	}
	if one.Title != "OpenCode Fixture One" {
		t.Errorf("title = %q", one.Title)
	}
	if one.Cwd != "/tmp/reup/fixtures/oc-one" {
		t.Errorf("directory = %q", one.Cwd)
	}
	if one.ProjectLabel != "oc-one" {
		t.Errorf("project = %q", one.ProjectLabel)
	}
	// No workspace_id column value, so project_id supplies the native id.
	if one.WorkspaceID != "proj-oc" {
		t.Errorf("workspace id = %q, want project_id fallback", one.WorkspaceID)
	}
	if len(one.Prompts) != 0 {
		// prompts are kept in agg only; Session.Prompts is not populated
		t.Errorf("prompts slice should stay empty (render uses first/last), got %d", len(one.Prompts))
	}
	if one.FirstPrompt != "opencode fixture one first prompt" ||
		one.LastPrompt != "opencode fixture one second prompt" {
		t.Errorf("prompts = %q / %q", one.FirstPrompt, one.LastPrompt)
	}
	// ses_aaaa is dual-projected (session_message + legacy message/part hold
	// the same two turns, as opencode 1.18.x writes both). The legacy copies
	// must collapse into the durable ones: 2 turns, not 4.
	if one.AsstCount != 2 {
		t.Errorf("assistant count = %d, want 2 (dual-write dedupe failed)", one.AsstCount)
	}
	// epoch ms → RFC3339
	if one.FirstTS != "2026-04-15T05:00:00Z" {
		t.Errorf("first ts = %q", one.FirstTS)
	}
	if one.LastTS != "2026-04-15T05:05:00Z" {
		t.Errorf("last ts = %q", one.LastTS)
	}
	if len(one.ResumeArgv) != 3 || one.ResumeArgv[0] != "opencode" ||
		one.ResumeArgv[1] != "--session" || one.ResumeArgv[2] != one.SessionID {
		t.Errorf("resume argv = %v", one.ResumeArgv)
	}
}

// project_id "global" is opencode's sentinel for "not a real project"; it must
// not surface as a workspace id.
func TestGlobalProjectHasNoWorkspaceID(t *testing.T) {
	sessions := listAll(t)
	d := sessions["ses_dddddddddddddddddddddddddddd"]
	if d.SessionID == "" {
		t.Fatal("session with project_id=global missing")
	}
	if d.WorkspaceID != "" {
		t.Errorf("workspace id = %q, want empty for the global sentinel", d.WorkspaceID)
	}
}

func TestArchivedAndChildSkipped(t *testing.T) {
	sessions := listAll(t)
	if _, ok := sessions["ses_bbbbbbbbbbbbbbbbbbbbbbbbbbbb"]; ok {
		t.Error("archived session must be skipped")
	}
	if _, ok := sessions["ses_cccccccccccccccccccccccccccc"]; ok {
		t.Error("child (parent_id) session must be skipped")
	}
}

func TestLegacyMessagePartPairDoesNotLeakAssistantText(t *testing.T) {
	sessions := listAll(t)
	one := sessions["ses_aaaaaaaaaaaaaaaaaaaaaaaaaaaa"]
	if one.LastPrompt == "legacy part prompt that must not leak" ||
		one.FirstPrompt == "legacy part prompt that must not leak" {
		t.Errorf("assistant part text leaked as prompt: %q / %q",
			one.FirstPrompt, one.LastPrompt)
	}
}

// Sessions that predate an upgrade exist only in the legacy message+part
// pair — opencode ships no backfill migration for them, so the provider must
// surface them from the v1 tables alone.
func TestFilters(t *testing.T) {
	p := newProvider(t)
	off, err := p.ListSessions(session.Filters{Date: "2020-01-01", Days: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(off) != 0 {
		t.Errorf("off-day filter returned %d", len(off))
	}
	on, err := p.ListSessions(session.Filters{Date: "2026-04-15", Days: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(on) != 4 {
		t.Errorf("on-day filter returned %d, want 4", len(on))
	}
	proj, err := p.ListSessions(session.Filters{AllTime: true, Days: -1, Project: "oc-three"})
	if err != nil {
		t.Fatal(err)
	}
	if len(proj) != 1 || proj[0].SessionID != "ses_dddddddddddddddddddddddddddd" {
		t.Errorf("project filter = %+v", proj)
	}
	proj2, err := p.ListSessions(session.Filters{AllTime: true, Days: -1, Project: "oc-two"})
	if err != nil {
		t.Fatal(err)
	}
	// oc-two matches the legacy-only session; the archived one is skipped.
	if len(proj2) != 1 || proj2[0].SessionID != "ses_eeeeeeeeeeeeeeeeeeeeeeeeeeee" {
		t.Errorf("project filter oc-two = %+v", proj2)
	}
	// An explicit narrow window vs the 2026-04-15 fixtures: today is far past,
	// so nothing qualifies. (The zero value no longer implies a window —
	// Days == 0 means "no time limit" and is the CLI default.)
	recent, err := p.ListSessions(session.Filters{Days: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 0 {
		t.Errorf("3-day window should exclude ancient fixtures, got %d", len(recent))
	}
}

// opencode v2 writes new sessions to session_v2 only, leaving `session` as a
// v1-compat copy that still contains deleted rows. Reading `session` alone
// hides every v2-era session (the reported hall-wiki bug); reading it at all
// resurrects deleted ones.
func TestSessionV2TablePreferred(t *testing.T) {
	sessions := listAll(t)
	four, ok := sessions["ses_ffffffffffffffffffffffffffff"]
	if !ok {
		t.Fatal("session_v2-only session missing from the listing")
	}
	if four.Title != "OpenCode Fixture Four" {
		t.Errorf("title = %q", four.Title)
	}
	if four.FirstPrompt != "opencode fixture four v2-only prompt" {
		t.Errorf("first prompt = %q", four.FirstPrompt)
	}
	if four.AsstCount != 1 {
		t.Errorf("assistant count = %d, want 1", four.AsstCount)
	}
	if _, ok := sessions["ses_9999999999999999999999999999"]; ok {
		t.Error("session deleted under v2 survived from the v1-compat table")
	}
}

// Databases that predate the v2 schema only have `session`; the provider must
// still list them.
func TestLegacySessionTableFallback(t *testing.T) {
	root := t.TempDir()
	copyTree(t, fixtureDir(t, "opencode-home"), root)
	db, err := sql.Open("sqlite", filepath.Join(root, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP TABLE session_v2"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	t.Setenv(envData, root)
	t.Setenv(envBin, filepath.Join(fixtureDir(t, "..", "mock-bin", "opencode")))
	p := New()
	sessions, err := p.ListSessions(session.Filters{AllTime: true, Days: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 4 {
		t.Fatalf("legacy table listed %d sessions, want 4", len(sessions))
	}
	found := false
	for _, s := range sessions {
		if s.SessionID == "ses_ffffffffffffffffffffffffffff" {
			t.Error("session_v2-only row leaked into a legacy-only database")
		}
		if s.SessionID == "ses_9999999999999999999999999999" {
			found = true
		}
	}
	if !found {
		t.Error("legacy-only session missing when session_v2 is absent")
	}
}

func TestLoadDetail(t *testing.T) {
	p := newProvider(t)
	s, err := p.LoadDetail("ses_aaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if s == nil || s.Title != "OpenCode Fixture One" {
		t.Fatalf("LoadDetail = %+v", s)
	}
	missing, err := p.LoadDetail("ses_nope")
	if err != nil {
		t.Fatal(err)
	}
	if missing != nil {
		t.Fatalf("LoadDetail(missing) = %+v", missing)
	}
}

func TestAvailability(t *testing.T) {
	p := newProvider(t)
	if !p.IsAvailable() {
		t.Error("fixture db + mock binary should be available")
	}
	t.Setenv(envData, "/nonexistent/reup-qa")
	if p.IsAvailable() {
		t.Error("missing data root must not be available")
	}
}

// TestNoTimeFilterByDefault locks in the CLI default: Days == 0 (the zero
// value) must impose no time limit. The fixtures are dated 2026-04-15, far
// outside any recent window, so a stale default would silently drop them all.
func TestNoTimeFilterByDefault(t *testing.T) {
	p := newProvider(t)
	sessions, err := p.ListSessions(session.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 4 {
		t.Fatalf("zero-value filter listed %d sessions, want 4 (no time limit)", len(sessions))
	}
}
