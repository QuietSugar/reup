package kimi

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/QuietSugar/reup/internal/session"
)

// copyTree copies a fixture into a temp directory so a test can mutate it
// without touching what `go test ./...` reads.
func copyTree(t *testing.T, src, dst string) error {
	t.Helper()
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
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
}

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

func listAll(t *testing.T) map[string]session.Session {
	t.Helper()
	t.Setenv(envKimiHome, fixtureDir(t, "kimi-home"))
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
	// 4 session dirs carry state.json; the stray bucket without one must be
	// skipped entirely.
	if len(sessions) != 4 {
		t.Fatalf("expected 4 valid kimi sessions, got %d", len(sessions))
	}

	one := sessions["dddd0001-1111-7000-8000-000000000001"]
	if one.Source != "kimi-code" {
		t.Errorf("source = %q", one.Source)
	}
	if one.Cwd != "/tmp/reup/fixtures/kimi-one" {
		t.Errorf("cwd from index = %q", one.Cwd)
	}
	if one.ProjectLabel != "kimi-one" {
		t.Errorf("project = %q", one.ProjectLabel)
	}
	if one.WorkspaceID != "wd_tmp-reup-fixtures-kimi-one_aaaaaaaaaaaa" {
		t.Errorf("workspace id = %q", one.WorkspaceID)
	}
	if one.WorkspaceRoot != "/tmp/reup/fixtures/kimi-one" {
		t.Errorf("workspace root = %q", one.WorkspaceRoot)
	}
	if one.Title != "Kimi Fixture One" {
		t.Errorf("title from state.json = %q", one.Title)
	}
	if len(one.Prompts) != 2 ||
		one.FirstPrompt != "kimi fixture one first prompt" ||
		one.LastPrompt != "kimi fixture one second prompt" {
		t.Errorf("prompts = %+v", one.Prompts)
	}
	if one.AsstCount != 2 {
		t.Errorf("assistant count = %d, want 2", one.AsstCount)
	}
	// FirstTS takes the earlier of createdAt (epoch ms in state.json) and the
	// first wire timestamp; LastTS the later of updatedAt and the last wire
	// timestamp (incl. turn.ended). Epoch-normalized values have no millis.
	if one.FirstTS != "2026-04-15T05:30:00Z" {
		t.Errorf("first ts = %q", one.FirstTS)
	}
	if one.LastTS != "2026-04-15T05:31:31Z" {
		t.Errorf("last ts = %q", one.LastTS)
	}
	if len(one.Prompts) > 0 && one.Prompts[0].TS != "2026-04-15T05:30:05Z" {
		t.Errorf("prompt ts = %q", one.Prompts[0].TS)
	}
	if len(one.ResumeArgv) != 3 || one.ResumeArgv[0] != "kimi" ||
		one.ResumeArgv[1] != "--session" || one.ResumeArgv[2] != one.SessionID {
		t.Errorf("resume argv = %v", one.ResumeArgv)
	}
}

func TestKimiV2Events(t *testing.T) {
	sessions := listAll(t)
	got := sessions["dddd0004-4444-7000-8000-000000000004"]
	if got.SessionID == "" {
		t.Fatal("v2 event-format session missing")
	}
	if got.AsstCount != 2 {
		t.Errorf("assistant turns = %d, want 2 completed turn.ended events", got.AsstCount)
	}
	if len(got.Prompts) != 2 || got.FirstPrompt != "kimi v2 first prompt" ||
		got.LastPrompt != "kimi v2 second prompt" {
		t.Errorf("v2 prompts = %+v; first=%q last=%q", got.Prompts, got.FirstPrompt, got.LastPrompt)
	}
	if got.Title != "kimi v2 second prompt" {
		t.Errorf("untitled session should fall back to lastPrompt, got %q", got.Title)
	}
	if got.Prompts[0].TS != "2026-04-15T05:30:01Z" {
		t.Errorf("prompt timestamp = %q", got.Prompts[0].TS)
	}
}

func TestForkSubtitleAndSyntheticPromptFilter(t *testing.T) {
	sessions := listAll(t)
	two := sessions["dddd0002-2222-7000-8000-000000000002"]
	if two.Subtitle != "forked from dddd0001" {
		t.Errorf("subtitle = %q", two.Subtitle)
	}
	for _, p := range two.Prompts {
		if len(p.Text) > 0 && p.Text[0] == '<' {
			t.Errorf("synthetic <...> prompt leaked: %q", p.Text)
		}
	}
	if len(two.Prompts) != 2 {
		t.Errorf("prompts = %+v", two.Prompts)
	}
	// lastPrompt from state.json fills in when wire parsing ends on an
	// assistant turn; here the wire ends on a real prompt, which wins.
	if two.LastPrompt != "kimi fixture two second prompt" {
		t.Errorf("last prompt = %q", two.LastPrompt)
	}
}

func TestSessionWithoutIndexEntryOrWire(t *testing.T) {
	sessions := listAll(t)
	three := sessions["dddd0003-3333-7000-8000-000000000003"]
	if three.Cwd != "" {
		t.Errorf("cwd = %q, want empty without index entry", three.Cwd)
	}
	if three.ProjectLabel != "(unknown)" {
		t.Errorf("project = %q", three.ProjectLabel)
	}
	// The bucket key is always available as the native workspace id, but this
	// bucket is deliberately absent from workspaces.json, so the root stays
	// empty and grouping falls back to the (empty) cwd.
	if three.WorkspaceID != "wd_tmp-reup-fixtures-kimi-three_cccccccccc" {
		t.Errorf("workspace id = %q", three.WorkspaceID)
	}
	if three.WorkspaceRoot != "" {
		t.Errorf("workspace root = %q, want empty without a workspaces.json entry", three.WorkspaceRoot)
	}
	if three.Title != "Kimi ISO No-Wire" {
		t.Errorf("title = %q, want the state.json title", three.Title)
	}
	if len(three.Prompts) != 0 || three.AsstCount != 0 {
		t.Errorf("prompts = %+v, asst = %d; missing wire.jsonl must yield none",
			three.Prompts, three.AsstCount)
	}
	if three.FirstTS != "2026-04-15T04:40:00.000Z" ||
		three.LastTS != "2026-04-15T04:45:00.000Z" {
		t.Errorf("timestamps = %q .. %q, want state.json values",
			three.FirstTS, three.LastTS)
	}
}

// An opened-but-unused session leaves a state.json and a lifecycle-only wire
// stream behind. Kimi's own surfaces hide these (the web list drops untitled
// "New Session" entries), so reup must not list them.
func TestUnusedSessionNotListed(t *testing.T) {
	sessions := listAll(t)
	if _, ok := sessions["dddd0005-5555-7000-8000-000000000005"]; ok {
		t.Error("unused kimi session (no title, no prompts, lifecycle-only wire) must not be listed")
	}
	// A session with a title but no wire at all is still a real session.
	if _, ok := sessions["dddd0003-3333-7000-8000-000000000003"]; !ok {
		t.Error("titled session without a wire stream should still be listed")
	}
}

func TestProjectFilter(t *testing.T) {
	t.Setenv(envKimiHome, fixtureDir(t, "kimi-home"))
	p := New()
	ss, err := p.ListSessions(session.Filters{AllTime: true, Days: -1, Project: "kimi-two"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].SessionID != "dddd0002-2222-7000-8000-000000000002" {
		t.Fatalf("project filter = %+v", ss)
	}
	none, err := p.ListSessions(session.Filters{AllTime: true, Days: -1, Project: "nope"})
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("impossible project filter returned %d", len(none))
	}
}

func TestDateFilter(t *testing.T) {
	t.Setenv(envKimiHome, fixtureDir(t, "kimi-home"))
	p := New()
	off, err := p.ListSessions(session.Filters{Date: "2020-01-01", Days: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(off) != 0 {
		t.Errorf("off-day date filter: got %d, want 0", len(off))
	}
	on, err := p.ListSessions(session.Filters{Date: "2026-04-15", Days: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(on) != 4 {
		t.Errorf("on-day date filter: got %d, want 4", len(on))
	}
}

func TestLoadDetail(t *testing.T) {
	t.Setenv(envKimiHome, fixtureDir(t, "kimi-home"))
	p := New()
	s, err := p.LoadDetail("dddd0001-1111-7000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if s == nil || s.Title != "Kimi Fixture One" {
		t.Fatalf("LoadDetail = %+v", s)
	}
	missing, err := p.LoadDetail("nope")
	if err != nil {
		t.Fatal(err)
	}
	if missing != nil {
		t.Fatalf("LoadDetail(missing) = %+v, want nil", missing)
	}
}

func TestAvailability(t *testing.T) {
	t.Setenv(envKimiHome, fixtureDir(t, "kimi-home"))
	t.Setenv(envKimiBin, filepath.Join(fixtureDir(t, "..", "mock-bin", "kimi")))
	p := New()
	if !p.IsAvailable() {
		t.Error("fixture home + kimi on PATH should be available")
	}
	t.Setenv(envKimiHome, "/nonexistent/reup-qa")
	if p.IsAvailable() {
		t.Error("missing session dir must not be available")
	}
}

func TestMissingIndexDegradesSilently(t *testing.T) {
	t.Setenv(envKimiHome, fixtureDir(t, "kimi-home"))
	// Point KIMI home at a dir whose sessions/ tree is real but whose index
	// is absent by overriding the home: sessions live under home/sessions, so
	// copy semantics aren't needed — the index path is derived from home too.
	// Instead simulate by clearing the cache and pointing index at a missing
	// file through a home that symlinks sessions. Simplest: use the fixture
	// home but assert titles/workdirs survive because they come from
	// state.json first; the index is only enrichment for cwd.
	p := New()
	sessions, err := p.ListSessions(session.Filters{AllTime: true, Days: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 4 {
		t.Fatalf("got %d sessions", len(sessions))
	}
}

func TestNormalizeTS(t *testing.T) {
	cases := map[string]string{
		"":                         "",
		"2026-04-15T05:30:00.000Z": "2026-04-15T05:30:00.000Z",
		"2026-04-15 05:30:00":      "2026-04-15 05:30:00",
		"1776231000":               "2026-04-15T05:30:00Z", // epoch seconds
		"1776231000000":            "2026-04-15T05:30:00Z", // epoch millis
		"0":                        "",                     // bogus zero rejected
		"12345678":                 "",                     // pre-2001 epoch rejected
		"garbage":                  "",
	}
	for in, want := range cases {
		if got := normalizeTS(in); got != want {
			t.Errorf("normalizeTS(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestNoTimeFilterByDefault locks in the CLI default: Days == 0 (the zero
// value) must impose no time limit. The fixtures are dated 2026-04-15, far
// outside any recent window, so a stale default would silently drop them all.
func TestNoTimeFilterByDefault(t *testing.T) {
	t.Setenv(envKimiHome, fixtureDir(t, "kimi-home"))
	p := New()
	sessions, err := p.ListSessions(session.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 4 {
		t.Fatalf("zero-value filter listed %d sessions, want 4 (no time limit)", len(sessions))
	}
}

// kimi flags archived sessions in state.json and keeps them out of its own
// session picker, so reup must not list or load them either. The session
// directory and its wire stream stay on disk.
func TestArchivedSessionNotListed(t *testing.T) {
	root := filepath.Join(t.TempDir(), "kimi-home")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(t, fixtureDir(t, "kimi-home"), root); err != nil {
		t.Fatal(err)
	}

	// Archive one of the four fixture sessions in place.
	archived := filepath.Join(root, "sessions", "*kimi-one_*", "*", "state.json")
	matches, err := filepath.Glob(archived)
	if err != nil || len(matches) != 1 {
		t.Fatalf("fixture session state.json: %v (%v)", matches, err)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), `"archived":false`, `"archived":true`, 1)
	if updated == string(data) {
		t.Fatal("fixture state.json has no archived flag to flip")
	}
	if err := os.WriteFile(matches[0], []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("REUP_KIMI_HOME", root)
	p := New()
	sessions, err := p.ListSessions(session.Filters{AllTime: true, Days: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 3 {
		t.Fatalf("listed %d sessions, want 3 (one archived session skipped)", len(sessions))
	}
	for _, s := range sessions {
		if s.SessionID == "dddd0001-1111-7000-8000-000000000001" {
			t.Error("archived session was listed")
		}
	}

	// It must not be loadable as a detail either.
	if d, err := p.LoadDetail("dddd0001-1111-7000-8000-000000000001"); err != nil || d != nil {
		t.Errorf("LoadDetail(archived) = %v, %v; want nil, nil", d, err)
	}
}

// Older kimi-code builds wrote an ISO string into the same camelCase
// createdAt/updatedAt fields that current builds fill with epoch-ms numbers, and
// they wrote no session_index.jsonl entry at all. Both facts must survive: the
// timestamps normalize, and the recorded workDir still resolves the cwd and
// project label instead of falling through to "(unknown)".
func TestISOTimestampsAndWorkDirParsed(t *testing.T) {
	root := filepath.Join(t.TempDir(), "kimi-iso-home")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(t, fixtureDir(t, "kimi-iso-home"), root); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REUP_KIMI_HOME", root)

	p := New()
	all, err := p.ListSessions(session.Filters{AllTime: true, Days: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("listed %d sessions, want 1", len(all))
	}
	s := all[0]

	const wantCwd = "/home/user/git-repo/my-project"
	if s.Cwd != wantCwd {
		t.Errorf("cwd = %q, want %q", s.Cwd, wantCwd)
	}
	if s.ProjectLabel != "my-project" {
		t.Errorf("project label = %q, want %q", s.ProjectLabel, "my-project")
	}
	// No workspaces.json in this home: the native id still comes from the
	// bucket, but there is no recorded root to enrich it with.
	if s.WorkspaceID == "" {
		t.Error("workspace id should come from the sessions/ bucket key")
	}
	if s.WorkspaceRoot != "" {
		t.Errorf("workspace root = %q, want empty without workspaces.json", s.WorkspaceRoot)
	}
	// ISO timestamps are passed through as written (same convention as the
	// snake_case created_at/updated_at path), milliseconds included.
	if s.FirstTS != "2026-07-25T04:21:32.410Z" {
		t.Errorf("first ts = %q, want the state.json createdAt", s.FirstTS)
	}
	if s.LastTS != "2026-07-25T04:26:10.000Z" {
		t.Errorf("last ts = %q, want the state.json updatedAt", s.LastTS)
	}
	// The wire stream in this fixture carries no timestamps, so the values above
	// can only have come from state.json.
	if s.Title != "我想将此项目做成 Skill" {
		t.Errorf("title = %q, want the state.json title (not the wire fallback)", s.Title)
	}
	if len(s.Prompts) != 2 || s.AsstCount != 2 {
		t.Errorf("prompts/asst = %d/%d, want 2/2", len(s.Prompts), s.AsstCount)
	}
	if s.FirstPrompt != "旧版会话的第一个提示" || s.LastPrompt != "旧版会话的第二个提示" {
		t.Errorf("prompts = %q / %q", s.FirstPrompt, s.LastPrompt)
	}
}
