package cwd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncodeCwd(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/Users/me/repo", "-Users-me-repo"},
		{"/Users/me/Mobile Documents/foo", "-Users-me-Mobile-Documents-foo"},
		{"/Users/me/iCloud~md~obsidian/foo", "-Users-me-iCloud-md-obsidian-foo"},
		{"/Users/me/Library/Mobile Documents/iCloud~md~obsidian/Documents/tao",
			"-Users-me-Library-Mobile-Documents-iCloud-md-obsidian-Documents-tao"},
		{"/", "-"},
		{"", "-"},
	}
	for _, c := range cases {
		if got := encodeCwd(c.in); got != c.want {
			t.Errorf("encodeCwd(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// sandbox mirrors the Python test setup: a temp tree with a target dir whose
// name contains spaces and tildes, plus a fake projects root whose encoded
// dir matches the target's absolute path.
type sandbox struct {
	tmpRoot     string
	targetDir   string
	sessionPath string
}

func newSandbox(t *testing.T) sandbox {
	t.Helper()
	tmpRoot := t.TempDir()
	targetDir := filepath.Join(tmpRoot, "path with space~and~tilde")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	encoded := encodeCwd(targetDir)
	projectsRoot := filepath.Join(tmpRoot, "fake-codebuddy-projects")
	sessionDir := filepath.Join(projectsRoot, encoded)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(sessionDir, "aaaa-bbbb-cccc.jsonl")
	if err := os.WriteFile(sessionPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return sandbox{tmpRoot: tmpRoot, targetDir: targetDir, sessionPath: sessionPath}
}

func TestFastPathStoredCwdMatches(t *testing.T) {
	sb := newSandbox(t)
	if got := ResolveExecCwd(sb.sessionPath, sb.targetDir); got != sb.targetDir {
		t.Errorf("fast path = %q, want %q", got, sb.targetDir)
	}
}

func TestSlowPathWalkSuccess(t *testing.T) {
	sb := newSandbox(t)
	got := ResolveExecCwd(sb.sessionPath, "/nonexistent/bogus/path")
	if got != sb.targetDir {
		t.Errorf("slow path = %q, want %q", got, sb.targetDir)
	}
}

func TestLegacyPathNoDashPrefix(t *testing.T) {
	sb := newSandbox(t)
	legacyDir := filepath.Join(sb.tmpRoot, "not-encoded")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(legacyDir, "x.jsonl")
	if err := os.WriteFile(legacyPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ResolveExecCwd(legacyPath, ""); got != "" {
		t.Errorf("legacy path should resolve to empty, got %q", got)
	}
}

func TestWalkMissReturnsEmpty(t *testing.T) {
	sb := newSandbox(t)
	missingEncoded := encodeCwd(filepath.Join(sb.tmpRoot, "no-such-target"))
	projRoot := filepath.Join(sb.tmpRoot, "fake-proj-2", missingEncoded)
	if err := os.MkdirAll(projRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	fakePath := filepath.Join(projRoot, "x.jsonl")
	if err := os.WriteFile(fakePath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ResolveExecCwd(fakePath, ""); got != "" {
		t.Errorf("walk miss should return empty, got %q", got)
	}
}

func TestDepthLimit(t *testing.T) {
	sb := newSandbox(t)
	var segs []string
	for i := 0; i < maxWalkDepth+5; i++ {
		segs = append(segs, fmt.Sprintf("seg%d", i))
	}
	encoded := "-" + strings.Join(segs, "-")
	projRoot := filepath.Join(sb.tmpRoot, "fake-proj-3", encoded)
	if err := os.MkdirAll(projRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	fakePath := filepath.Join(projRoot, "x.jsonl")
	if err := os.WriteFile(fakePath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ResolveExecCwd(fakePath, ""); got != "" {
		t.Errorf("depth-limited walk should return empty, got %q", got)
	}
}

func TestFastPathIgnoredWhenEncodingMismatches(t *testing.T) {
	sb := newSandbox(t)
	otherDir := filepath.Join(sb.tmpRoot, "different-dir")
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}
	got := ResolveExecCwd(sb.sessionPath, otherDir)
	if got != sb.targetDir {
		t.Errorf("mismatched stored cwd should fall to slow path; got %q, want %q", got, sb.targetDir)
	}
}

func TestPermissionDeniedSubtreeIsSkipped(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses chmod — can't simulate permission errors")
	}
	sb := newSandbox(t)
	decoyParent := filepath.Join(sb.tmpRoot, "decoy-perm")
	decoyInner := filepath.Join(decoyParent, "path")
	if err := os.MkdirAll(decoyInner, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(decoyParent, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(decoyParent, 0o755)
	got := ResolveExecCwd(sb.sessionPath, "/nonexistent/force-slow-path")
	if got != sb.targetDir {
		t.Errorf("walk should skip unreadable decoy and still find target; got %q", got)
	}
}
