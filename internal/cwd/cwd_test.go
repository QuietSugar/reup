package cwd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/QuietSugar/reup/internal/session"
)

func TestResolve(t *testing.T) {
	dir := t.TempDir()

	if got, ok := Resolve(&session.Session{Cwd: dir}); !ok || got != dir {
		t.Errorf("existing cwd: got (%q, %v), want (%q, true)", got, ok, dir)
	}

	gone := filepath.Join(dir, "deleted-project")
	if got, ok := Resolve(&session.Session{Cwd: gone}); ok {
		t.Errorf("missing cwd: got (%q, %v), want a refusal", got, ok)
	} else if got != gone {
		t.Errorf("missing cwd: dir = %q, want %q", got, gone)
	}

	if got, ok := Resolve(&session.Session{}); !ok || got != "" {
		t.Errorf("unknown cwd: got (%q, %v), want (\"\", true)", got, ok)
	}
}

// A stale stored cwd must not make a session look unresumable when the project
// dir can still be recovered from where the session file lives — the marker
// and the refusal have to agree, or the list cries wolf.
func TestResolveRecoversStaleStoredCwd(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real project")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	// Session file parked under an encoded parent dir that decodes to target.
	encoded := filepath.Join(root, "projects", encodeCwd(target))
	if err := os.MkdirAll(encoded, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(encoded, "sess.jsonl")

	s := session.Session{
		Source: "codebuddy",
		Path:   sessionPath,
		Cwd:    filepath.Join(root, "vanished project"), // stale on purpose
	}
	if got, ok := Resolve(&s); !ok || got != target {
		t.Errorf("codebuddy stale cwd: got (%q, %v), want (%q, true)", got, ok, target)
	}
	if Missing(&s) {
		t.Error("a recoverable session must not be flagged as missing")
	}

	// The same staleness with no encoded parent to recover from: now it is gone.
	orphan := session.Session{Source: "kimi-code", Cwd: s.Cwd}
	if _, ok := Resolve(&orphan); ok {
		t.Error("a session with no recovery path must be refused")
	}
	if !Missing(&orphan) {
		t.Error("Missing() must agree with Resolve()")
	}
}

// A session with no recorded cwd at all is NOT treated as deleted: reup
// cannot know where it lived, so it still runs the resume command from the
// current directory. Whether that is enough for kimi is an open question —
// kimi reports "created under a different directory" when its own record of
// the directory disagrees with the process cwd. Verified against real data in
// docs/real-world-testing.md ("Kimi sessions without a recorded cwd").
func TestResolveUnknownCwdIsNotRefused(t *testing.T) {
	s := session.Session{Source: "kimi-code", Cwd: ""}
	got, ok := Resolve(&s)
	if !ok || got != "" {
		t.Errorf("unknown cwd: got (%q, %v), want (\"\", true) - reup cannot know it is gone", got, ok)
	}
	if Missing(&s) {
		t.Error("an unknown cwd must not be reported as a deleted directory")
	}
}
