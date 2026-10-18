package dsh

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/QuietSugar/reup/internal/session"
)

// fakeHome builds a dsh home with fixture session logs and points
// REUP_DSH_HOME at it. Logs are zstd frames (concatenated where the
// harness does that) plus a plain legacy log, exercising every decoder path.
// concatZstd compresses each chunk as its own frame and concatenates them,
// reproducing the harness's restart-appended session artifacts. Chunks are
// newline-terminated first: every harness event line ends with \n, and two
// frames glued without one would merge their boundary lines after decode.
func concatZstd(chunks ...string) []byte {
	var out bytes.Buffer
	for _, chunk := range chunks {
		chunk = strings.TrimRight(chunk, "\n") + "\n"
		zw, err := zstd.NewWriter(&out)
		if err != nil {
			panic(err)
		}
		if _, err := zw.Write([]byte(chunk)); err != nil {
			panic(err)
		}
		if err := zw.Close(); err != nil {
			panic(err)
		}
	}
	return out.Bytes()
}

func fakeHome(t *testing.T, files map[string]string) {
	t.Helper()
	home := t.TempDir()
	for rel, content := range files {
		path := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(path, ".zstd") {
			var buf bytes.Buffer
			zw, err := zstd.NewWriter(&buf)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := zw.Write([]byte(content)); err != nil {
				t.Fatal(err)
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(envHome, home)
}

const headerFmt = `{"type":"session","version":4,"id":%q,"createdAt":%d,"cwd":%q,"isSeeded":false,"delegationDepth":%d,"agentPreset":"standard"}`

func fixtureFiles() map[string]string {
	return map[string]string{
		// Main session, concatenated zstd (two frames, like the harness
		// produces after a restart).
		"sessions/--home-user-working-debug--/session-11111111-1111-4111-8111-111111111111/session.v4.jsonl.zstd": `{"type":"session","version":4,"id":"session-11111111-1111-4111-8111-111111111111","createdAt":1791116742819,"cwd":"/home/user/working/debug","isSeeded":false,"delegationDepth":0,"agentPreset":"standard"}
{"type":"permission/preset","seq":0,"time":1791116742830,"data":{"preset":"workspace-write"}}
{"type":"user/message","seq":1,"time":1791116742900,"data":{"content":[{"type":"text","text":"first prompt line\nsecond line"}]}}
{"type":"assistant/message","seq":2,"time":1791116743000,"data":{"message":{"source":{"model":"deepseek-chat"}}}}
{"type":"session/title","seq":3,"time":1791116743100,"data":{"title":"main session title","source":{"kind":"user"}}}
{"type":"user/message","seq":4,"time":1791116800000,"data":{"content":[{"type":"text","text":"second prompt"}]}}
{"type":"assistant/message","seq":5,"time":1791116801000,"data":{}}
{"type":"turn/end","seq":6,"time":1791116802000,"data":{}}`,

		// Sub-agent session (delegationDepth > 0): must be skipped.
		"sessions/--home-user-working-debug--/session-22222222-2222-4222-8222-222222222222/session.v4.jsonl.zstd": `{"type":"session","version":4,"id":"session-22222222-2222-4222-8222-222222222222","createdAt":1791116800000,"cwd":"/home/user/working/debug","isSeeded":false,"delegationDepth":1,"agentPreset":"standard"}
{"type":"user/message","seq":1,"time":1791116800100,"data":{"content":[{"type":"text","text":"Your parent agent id is session-1111"}]}}`,

		// Header-only session (no prompts): must be skipped.
		"sessions/--home-user-working-debug--/session-33333333-3333-4333-8333-333333333333/session.v4.jsonl.zstd": `{"type":"session","version":4,"id":"session-33333333-3333-4333-8333-333333333333","createdAt":1791116800000,"cwd":"/home/user/working/debug","isSeeded":false,"delegationDepth":0,"agentPreset":"standard"}
{"type":"sandbox/mode","seq":0,"time":1791116800010,"data":{"mode":"workspace-write"}}`,

		// Legacy plain (uncompressed) session.jsonl.
		"sessions/--home-user-working-project-infra-notes--/session-44444444-4444-4444-8444-444444444444/session.jsonl": `{"type":"session","version":4,"id":"session-44444444-4444-4444-8444-444444444444","createdAt":1791200000000,"cwd":"/home/user/working/project/infra-notes","isSeeded":false,"delegationDepth":0,"agentPreset":"standard"}
{"type":"user/message","seq":1,"time":1791200001000,"data":{"content":[{"type":"text","text":"legacy plain prompt"}]}}
{"type":"session/title","seq":2,"time":1791200002000,"data":{"title":"legacy title"}}
{"type":"assistant/message","seq":3,"time":1791200003000,"data":{}}`,
	}
}

// setupHome writes the fixture home and replaces the main session log with a
// genuinely multi-frame artifact: the harness appends frames across restarts,
// and a naive single-frame decode would silently drop the second frame's
// events. Returns the home path.
func setupHome(t *testing.T) string {
	files := fixtureFiles()
	fakeHome(t, files)
	home := os.Getenv(envHome)
	frame1 := `{"type":"session","version":4,"id":"session-11111111-1111-4111-8111-111111111111","createdAt":1791116742819,"cwd":"/home/user/working/debug","isSeeded":false,"delegationDepth":0,"agentPreset":"standard"}
{"type":"user/message","seq":1,"time":1791116742900,"data":{"content":[{"type":"text","text":"first prompt line\nsecond line"}]}}
{"type":"session/title","seq":3,"time":1791116743100,"data":{"title":"main session title","source":{"kind":"user"}}}
{"type":"user/message","seq":4,"time":1791116800000,"data":{"content":[{"type":"text","text":"second prompt"}]}}`
	frame2 := `{"type":"user/message","seq":7,"time":1791116900000,"data":{"content":[{"type":"text","text":"third prompt"}]}}
{"type":"assistant/message","seq":8,"time":1791116901000,"data":{}}`
	path := filepath.Join(home, "sessions", "--home-user-working-debug--",
		"session-11111111-1111-4111-8111-111111111111", "session.v4.jsonl.zstd")
	if err := os.WriteFile(path, concatZstd(frame1, frame2), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestListSessionsParsesDshLogs(t *testing.T) {
	setupHome(t)
	p := New()
	if !p.IsAvailable() {
		t.Fatal("provider should be available with a sessions dir")
	}
	ss, err := p.ListSessions(session.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 2 {
		t.Fatalf("sessions = %d, want 2 (sub-agent and header-only skipped)", len(ss))
	}
	// Newest first by the caller's sort — here just find by id.
	byID := map[string]session.Session{}
	for _, s := range ss {
		byID[s.SessionID] = s
	}
	main, ok := byID["session-11111111-1111-4111-8111-111111111111"]
	if !ok {
		t.Fatal("main session missing")
	}
	if main.Cwd != "/home/user/working/debug" {
		t.Errorf("cwd = %q", main.Cwd)
	}
	if main.Title != "main session title" {
		t.Errorf("title = %q", main.Title)
	}
	if main.FirstPrompt != "first prompt line" {
		t.Errorf("first prompt = %q", main.FirstPrompt)
	}
	if main.LastTS == "" || main.FirstTS == "" {
		t.Error("timestamps must be populated from event times")
	}
	if _, ok := byID["session-22222222-2222-4222-8222-222222222222"]; ok {
		t.Error("sub-agent session must be skipped")
	}
	if _, ok := byID["session-33333333-3333-4333-8333-333333333333"]; ok {
		t.Error("header-only session must be skipped")
	}
	legacy, ok := byID["session-44444444-4444-4444-8444-444444444444"]
	if !ok {
		t.Fatal("legacy plain session missing")
	}
	if legacy.Title != "legacy title" {
		t.Errorf("legacy title = %q", legacy.Title)
	}
}

func TestLoadDetailReturnsPrompts(t *testing.T) {
	setupHome(t)
	d, err := New().LoadDetail("session-11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Prompts) != 3 {
		t.Fatalf("prompts = %d, want 3 (across both zstd frames)", len(d.Prompts))
	}
	if !strings.Contains(d.Prompts[2].Text, "third prompt") {
		t.Errorf("second-frame prompt missing: %+v", d.Prompts[2])
	}
	if _, err := New().LoadDetail("session-missing"); err == nil {
		t.Error("unknown id must error")
	}
}

func TestHomeResolutionOrder(t *testing.T) {
	t.Setenv(envHome, "/override/dsh")
	t.Setenv(envDshHome, "/harness/dsh")
	if got := home(); got != "/override/dsh" {
		t.Errorf("home = %q, want the reup override first", got)
	}
	os.Unsetenv(envHome)
	if got := home(); got != "/harness/dsh" {
		t.Errorf("home = %q, want the harness override second", got)
	}
	os.Unsetenv(envDshHome)
	if !strings.HasSuffix(home(), ".dsh") {
		t.Errorf("home = %q, want ~/.dsh", home())
	}
}
