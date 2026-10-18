package workspace

import (
	"reflect"
	"testing"

	"github.com/QuietSugar/reup/internal/session"
)

func TestKeyPrefersCwdDirectory(t *testing.T) {
	s := &session.Session{
		Source:        "kimi-code",
		Cwd:           "/home/user/repo/",
		WorkspaceRoot: "/home/user",
		WorkspaceID:   "wd_repo_abc123",
	}
	if got, want := Key(s), "dir:/home/user/repo"; got != want {
		t.Errorf("Key = %q, want %q", got, want)
	}
}

func TestKeyFallsBackToWorkspaceRoot(t *testing.T) {
	s := &session.Session{Source: "kimi-code", WorkspaceRoot: "/home/user/repo", WorkspaceID: "wd_repo_abc123"}
	if got, want := Key(s), "dir:/home/user/repo"; got != want {
		t.Errorf("Key = %q, want %q", got, want)
	}
}

func TestKeyFallsBackToNativeIDPerSource(t *testing.T) {
	s := &session.Session{Source: "kimi-code", WorkspaceID: "wd_repo_abc123"}
	if got, want := Key(s), "ws:kimi-code:wd_repo_abc123"; got != want {
		t.Errorf("Key = %q, want %q", got, want)
	}
	// A native id must not merge across sources.
	other := &session.Session{Source: "opencode", WorkspaceID: "wd_repo_abc123"}
	if Key(s) == Key(other) {
		t.Errorf("same native id in different sources must not share a key: %q", Key(s))
	}
}

func TestKeyUnknownPerSource(t *testing.T) {
	a := &session.Session{Source: "kimi-code"}
	b := &session.Session{Source: "opencode"}
	if Key(a) == Key(b) {
		t.Errorf("directory-less sessions from different sources must not share a key: %q", Key(a))
	}
	// A bare root is not a usable identity either.
	root := &session.Session{Source: "kimi-code", Cwd: "/"}
	if got, want := Key(root), "unknown:kimi-code"; got != want {
		t.Errorf("root cwd Key = %q, want %q", got, want)
	}
}

func TestGroupMergesAcrossProviders(t *testing.T) {
	sessions := []session.Session{
		{Source: "kimi-code", SessionID: "k1", Cwd: "/home/user/repo", WorkspaceID: "wd_repo_x", LastTS: "2026-04-15T10:00:00Z"},
		{Source: "opencode", SessionID: "o1", Cwd: "/home/user/repo", LastTS: "2026-04-15T09:00:00Z"},
		{Source: "opencode", SessionID: "o2", Cwd: "/home/user/other", LastTS: "2026-04-15T08:00:00Z"},
	}
	groups := GroupBy(sessions)
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2: %+v", len(groups), groups)
	}
	if groups[0].Key != "dir:/home/user/repo" {
		t.Errorf("group[0].Key = %q", groups[0].Key)
	}
	if want := []string{"kimi-code", "opencode"}; !reflect.DeepEqual(groups[0].Providers, want) {
		t.Errorf("group[0].Providers = %v, want %v", groups[0].Providers, want)
	}
	if len(groups[0].Sessions) != 2 {
		t.Errorf("group[0] merged %d sessions, want 2", len(groups[0].Sessions))
	}
	if groups[0].WorkspaceID != "wd_repo_x" {
		t.Errorf("group[0].WorkspaceID = %q, want wd_repo_x", groups[0].WorkspaceID)
	}
	if groups[1].Key != "dir:/home/user/other" {
		t.Errorf("group[1].Key = %q", groups[1].Key)
	}
}

// A group is positioned by its newest session, so the workspace of the most
// recent session sorts first even when the caller passes sessions oldest-first.
func TestGroupOrdersByNewestSession(t *testing.T) {
	sessions := []session.Session{
		{Source: "opencode", SessionID: "b-old", Cwd: "/b", LastTS: "2026-04-15T01:00:00Z"},
		{Source: "kimi-code", SessionID: "a-new", Cwd: "/a", LastTS: "2026-04-15T10:00:00Z"},
		{Source: "opencode", SessionID: "b-new", Cwd: "/b", LastTS: "2026-04-15T03:00:00Z"},
	}
	groups := GroupBy(sessions)
	if len(groups) != 2 || groups[0].Dir != "/a" || groups[1].Dir != "/b" {
		t.Fatalf("groups not ordered by newest session (want /a then /b): %+v", groups)
	}
	// Membership keeps the caller's order within each group.
	if got := groups[1].Sessions; len(got) != 2 || got[0].SessionID != "b-old" || got[1].SessionID != "b-new" {
		t.Errorf("session order within group changed: %+v", got)
	}
}

// A group falls back to FirstTS when LastTS is absent, and undated groups sort
// after dated ones.
func TestGroupTimestampFallbackAndUndated(t *testing.T) {
	sessions := []session.Session{
		{Source: "opencode", SessionID: "undated", Cwd: "/none"},
		{Source: "kimi-code", SessionID: "first-only", Cwd: "/first", FirstTS: "2026-04-15T05:00:00Z"},
	}
	groups := GroupBy(sessions)
	if len(groups) != 2 || groups[0].Dir != "/first" || groups[1].Dir != "/none" {
		t.Fatalf("dated group must sort before undated: %+v", groups)
	}
}

func TestGroupLabel(t *testing.T) {
	cases := []struct {
		g    Group
		want string
	}{
		{Group{Dir: "/home/user/repo"}, "repo"},
		{Group{WorkspaceID: "wd_repo_abc"}, "wd_repo_abc"},
		{Group{}, "(no workspace)"},
	}
	for _, c := range cases {
		if got := c.g.Label(); got != c.want {
			t.Errorf("Label(%+v) = %q, want %q", c.g, got, c.want)
		}
	}
}

func TestGroupEmpty(t *testing.T) {
	if got := GroupBy(nil); got != nil {
		t.Errorf("GroupBy(nil) = %+v, want nil", got)
	}
}
