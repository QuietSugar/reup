package agy

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/QuietSugar/reup/internal/session"
	_ "modernc.org/sqlite"
)

func writeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newTestProvider(t *testing.T) *Provider {
	t.Helper()
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "conversation_summaries.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE conversation_summaries (
		conversation_id TEXT PRIMARY KEY,
		title TEXT NOT NULL DEFAULT "",
		preview TEXT NOT NULL DEFAULT "",
		last_modified_time TEXT NOT NULL,
		workspace_uris TEXT NOT NULL,
		project_id TEXT NOT NULL DEFAULT "",
		nesting_depth INTEGER NOT NULL DEFAULT 0
	)`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	rows := []struct {
		id, title, preview, modified, workspaces, project string
		depth                                             int
	}{
		{"agy-main-001", "Fixture AGY", "summary preview", "2026-04-15 05:05:00.123456789+00:00", `["file:///tmp/reup/fixtures/agy%20alpha"]`, "project-agy-alpha", 0},
		{"agy-second-002", "", "Second session preview", "2026-04-16T06:00:00Z", `["file:///tmp/reup/fixtures/agy-beta"]`, "project-agy-beta", 0},
		{"agy-child-003", "Child", "child prompt", "2026-04-15T07:00:00Z", `[]`, "", 1},
		{"agy-internal-004", "Internal", "internal prompt", "2026-04-15T07:00:00Z", `[]`, "", 0},
	}
	for _, row := range rows {
		if _, err := db.Exec(`INSERT INTO conversation_summaries
			(conversation_id, title, preview, last_modified_time, workspace_uris, project_id, nesting_depth)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, row.id, row.title, row.preview, row.modified, row.workspaces, row.project, row.depth); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	writeFixtureFile(t, filepath.Join(root, "cache", "conversation_metadata.json"),
		`{"conversations":{"agy-internal-004":{"is_internal":true}}}`)
	transcript := strings.Join([]string{
		`{"type":"USER_INPUT","created_at":"2026-04-15T05:00:00Z","content":"<USER_SETTINGS_CHANGE>changed setting ` + "`Model Selection`" + ` from Auto to Gemini 3.5 Flash.</USER_SETTINGS_CHANGE>\n<USER_REQUEST>First agy prompt</USER_REQUEST>\n<ADDITIONAL_METADATA>synthetic metadata</ADDITIONAL_METADATA>"}`,
		`{"type":"PLANNER_RESPONSE","created_at":"2026-04-15T05:01:00Z","content":"First answer"}`,
		`{not complete json`,
		`{"type":"USER_INPUT","created_at":"2026-04-15T05:03:00Z","content":"Second agy prompt"}`,
		`{"type":"PLANNER_RESPONSE","created_at":"2026-04-15T05:05:00Z","content":"Second answer"}`,
	}, "\n")
	writeFixtureFile(t, filepath.Join(root, "brain", "agy-main-001", ".system_generated", "logs", "transcript.jsonl"), transcript)

	t.Setenv(envHome, root)
	t.Setenv(envBin, "/bin/true")
	return New()
}

func listAll(t *testing.T, p *Provider) map[string]session.Session {
	t.Helper()
	sessions, err := p.ListSessions(session.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]session.Session, len(sessions))
	for _, s := range sessions {
		out[s.SessionID] = s
	}
	return out
}

func TestListSessions(t *testing.T) {
	p := newTestProvider(t)
	if !p.IsAvailable() {
		t.Fatal("provider should be available with a database and agy binary")
	}
	sessions := listAll(t, p)
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want 2: %+v", len(sessions), sessions)
	}
	if _, ok := sessions["agy-child-003"]; ok {
		t.Error("nested subagent conversation must be skipped")
	}
	if _, ok := sessions["agy-internal-004"]; ok {
		t.Error("internal conversation must be skipped")
	}

	one := sessions["agy-main-001"]
	if one.Source != "agy" || one.Title != "Fixture AGY" {
		t.Errorf("source/title = %q / %q", one.Source, one.Title)
	}
	if one.Cwd != "/tmp/reup/fixtures/agy alpha" || one.WorkspaceRoot != one.Cwd {
		t.Errorf("cwd/workspace root = %q / %q", one.Cwd, one.WorkspaceRoot)
	}
	if one.ProjectLabel != "agy alpha" || one.WorkspaceID != "project-agy-alpha" {
		t.Errorf("project/workspace id = %q / %q", one.ProjectLabel, one.WorkspaceID)
	}
	if one.FirstTS != "2026-04-15T05:00:00Z" || one.LastTS != "2026-04-15T05:05:00.123456789Z" {
		t.Errorf("timestamps = %q / %q", one.FirstTS, one.LastTS)
	}
	if one.FirstPrompt != "First agy prompt" || one.LastPrompt != "Second agy prompt" {
		t.Errorf("prompts = %q / %q", one.FirstPrompt, one.LastPrompt)
	}
	if one.AsstCount != 2 || len(one.Prompts) != 0 {
		t.Errorf("assistant/prompt counts = %d / %d", one.AsstCount, len(one.Prompts))
	}
	wantArgv := []string{"agy", "--conversation", "agy-main-001"}
	if !reflect.DeepEqual(one.ResumeArgv, wantArgv) {
		t.Errorf("resume argv = %v, want %v", one.ResumeArgv, wantArgv)
	}

	two := sessions["agy-second-002"]
	if two.Title != "Second session preview" || two.FirstPrompt != "Second session preview" {
		t.Errorf("summary fallback title/prompt = %q / %q", two.Title, two.FirstPrompt)
	}
	if two.LastTS != "2026-04-16T06:00:00Z" {
		t.Errorf("summary timestamp = %q", two.LastTS)
	}
}

func TestFilters(t *testing.T) {
	p := newTestProvider(t)
	onDate, err := p.ListSessions(session.Filters{Date: "2026-04-15"})
	if err != nil {
		t.Fatal(err)
	}
	if len(onDate) != 1 || onDate[0].SessionID != "agy-main-001" {
		t.Errorf("date filter = %+v", onDate)
	}
	offDate, err := p.ListSessions(session.Filters{Date: "2020-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	if len(offDate) != 0 {
		t.Errorf("off-date filter returned %d sessions", len(offDate))
	}
	project, err := p.ListSessions(session.Filters{Project: "BETA"})
	if err != nil {
		t.Fatal(err)
	}
	if len(project) != 1 || project[0].SessionID != "agy-second-002" {
		t.Errorf("project filter = %+v", project)
	}
	recent, err := p.ListSessions(session.Filters{Days: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 0 {
		t.Errorf("recent filter returned %d ancient sessions", len(recent))
	}
}

func TestLoadDetail(t *testing.T) {
	p := newTestProvider(t)
	s, err := p.LoadDetail("agy-main-001")
	if err != nil {
		t.Fatal(err)
	}
	if s == nil || len(s.Prompts) != 2 {
		t.Fatalf("detail = %+v", s)
	}
	if s.Prompts[0].Text != "First agy prompt" || s.Prompts[0].TS != "2026-04-15T05:00:00Z" ||
		s.Prompts[1].Text != "Second agy prompt" || s.Prompts[1].TS != "2026-04-15T05:03:00Z" {
		t.Errorf("detail prompts = %+v", s.Prompts)
	}
	missing, err := p.LoadDetail("agy-missing")
	if err != nil {
		t.Fatal(err)
	}
	if missing != nil {
		t.Errorf("missing detail = %+v, want nil", missing)
	}
}

func TestCleanUserInputLegacySettings(t *testing.T) {
	input := "changed setting `Model Selection` from Auto to Gemini 3.5 Flash.\nLegacy prompt"
	if got := cleanUserInput(input); got != "Legacy prompt" {
		t.Errorf("cleanUserInput = %q", got)
	}
}

func TestHomeOverride(t *testing.T) {
	t.Setenv(envHome, "/override/agy")
	if got := home(); got != "/override/agy" {
		t.Errorf("home = %q", got)
	}
}
