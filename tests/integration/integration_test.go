// Package integration runs the built reup binary against the fixture set,
// porting the old shell assertion scenarios (08 unified render, 10 missing
// provider, 12 stale-cwd exec). The TUI scenarios drive a real PTY, so the
// full path — picker → filter → enter → chdir → exec of the mock agent —
// is proven without tmux.
package integration

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuietSugar/reup/internal/textutil"
	"github.com/creack/pty"
)

var (
	repoRoot string
	binPath  string
)

func TestMain(m *testing.M) {
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot, _ = filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", ".."))

	tmp, err := os.MkdirTemp("", "reup-itest-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmp)
	binPath = filepath.Join(tmp, "reup")

	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build failed: %v\n%s", err, out)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func fixtureEnv(t *testing.T) []string {
	t.Helper()
	mockBin := filepath.Join(repoRoot, "tests", "mock-bin")
	env := []string{
		"PATH=" + mockBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TERM=xterm-256color",
		"REUP_CODEBUDDY_HOME=" + filepath.Join(repoRoot, "tests", "fixtures", "codebuddy"),
		"REUP_KIMI_HOME=" + filepath.Join(repoRoot, "tests", "fixtures", "kimi-home"),
		"REUP_KIMI_BIN=kimi",
		"REUP_OPENCODE_DATA=" + filepath.Join(repoRoot, "tests", "fixtures", "opencode-home"),
		"REUP_OPENCODE_BIN=opencode",
		// The dsh provider reads the real ~/.dsh otherwise; integration
		// fixtures pin only the three main providers.
		"REUP_DSH_HOME=" + t.TempDir(),
		// Sentinel pre-burned via XDG redirect so the first-run star message
		// doesn't interleave with exec assertions.
		"XDG_STATE_HOME=" + t.TempDir(),
	}
	return env
}

// materializeFixtureCwds creates the real directories fixture sessions point
// at (the old tmux_use_fixtures did this), so chdir before exec succeeds.
func materializeFixtureCwds(t *testing.T) {
	t.Helper()
	for _, d := range []string{
		"/tmp/reup/fixtures/codebuddy-alpha",
		"/tmp/reup/fixtures/obsidian path with space/vault",
		"/tmp/reup/fixtures/kimi-one",
		"/tmp/reup/fixtures/kimi-two",
		"/tmp/reup/fixtures/kimi-empty",
		"/tmp/reup/fixtures/oc-one",
		"/tmp/reup/fixtures/oc-two",
		"/tmp/reup/fixtures/oc-three",
		"/tmp/reup/fixtures/oc-json",
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// --- 08: unified render ---

func TestUnifiedRender(t *testing.T) {
	// Flat table (--no-group) so the pinned row positions stay stable; the
	// default grouped layout is covered by TestGroupedRender.
	materializeFixtureCwds(t)
	cmd := exec.Command(binPath, "list", "--all", "--no-group")
	cmd.Env = fixtureEnv(t)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("list --all failed: %v", err)
	}
	s := string(out)
	for _, want := range []string{"[cb]", "[kimi]", "[oc]"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q", want)
		}
	}
	// The one session whose working directory is gone must say so instead of
	// passing its stale name off as a live project.
	if !strings.Contains(s, textutil.DirDeletedLabel) {
		t.Errorf("output should flag the deleted working directory: %q", s)
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("unexpectedly short output: %d lines", len(lines))
	}
	// Header(1) + divider(2); first data row must be the most recent fixture:
	// kimi-two at 06:32 beats every other row.
	firstRow := lines[2]
	if !strings.Contains(firstRow, "kimi-two") || !strings.Contains(firstRow, "[kimi]") {
		t.Errorf("top row should be kimi-two with [kimi] badge: %q", firstRow)
	}
	lastRow := lines[len(lines)-1]
	if !strings.Contains(lastRow, "Kimi ISO No-Wire") {
		t.Errorf("last row should be the oldest fixture (kimi ISO no-wire): %q", lastRow)
	}
}

// TestGroupedRender verifies the default `list` output groups rows under
// workspace headers while preserving every session row verbatim.
func TestGroupedRender(t *testing.T) {
	materializeFixtureCwds(t)
	run := func(args ...string) string {
		cmd := exec.Command(binPath, args...)
		cmd.Env = fixtureEnv(t)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%v failed: %v", args, err)
		}
		return string(out)
	}

	grouped := run("list", "--all")
	flat := run("list", "--all", "--no-group")

	if !strings.Contains(grouped, "── ") {
		t.Fatalf("grouped output has no workspace headers:\n%s", grouped)
	}
	// A workspace header must mark the deleted directory, not only the row.
	if !strings.Contains(grouped, textutil.DirDeletedLabel) {
		t.Errorf("grouped output should flag the deleted workspace:\n%s", grouped)
	}
	// Every flat session row must survive verbatim inside the grouped output.
	flatLines := strings.Split(strings.TrimRight(flat, "\n"), "\n")
	if len(flatLines) < 3 {
		t.Fatalf("flat output unexpectedly short: %d lines", len(flatLines))
	}
	for _, row := range flatLines[2:] {
		if row == "" {
			continue
		}
		if !strings.Contains(grouped, row) {
			t.Errorf("grouped output dropped/altered session row: %q", row)
		}
	}
}

// --- 10: missing provider ---

func TestMissingProvider(t *testing.T) {
	env := fixtureEnv(t)
	for i, e := range env {
		if strings.HasPrefix(e, "REUP_OPENCODE_DATA=") {
			env[i] = "REUP_OPENCODE_DATA=/nonexistent/reup-qa-missing"
		}
	}

	cmd := exec.Command(binPath, "list", "--all")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("merged list must exit 0 with the remaining providers: %v", err)
	}
	if !strings.Contains(string(out), "[cb]") {
		t.Error("output should have [cb] rows")
	}
	if strings.Contains(string(out), "[oc]") {
		t.Error("output must have no [oc] rows")
	}

	cmd2 := exec.Command(binPath, "list", "--source=opencode", "--all")
	cmd2.Env = env
	var stderr strings.Builder
	cmd2.Stderr = &stderr
	err2 := cmd2.Run()
	ee, ok := err2.(*exec.ExitError)
	if !ok || ee.ExitCode() != 2 {
		t.Fatalf("--source=opencode must exit 2 when unavailable, got %v", err2)
	}
	if !strings.Contains(stderr.String(), "opencode provider not available") {
		t.Errorf("stderr should carry provider-specific message: %q", stderr.String())
	}
}

// --- PTY harness for picker scenarios ---

type ptyRun struct {
	cmd  *exec.Cmd
	tty  *os.File
	mu   sync.Mutex
	buf  strings.Builder
	done chan error
}

func startPicker(t *testing.T, env []string, args ...string) *ptyRun {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Env = env
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 55, Cols: 220})
	if err != nil {
		t.Fatal(err)
	}
	r := &ptyRun{cmd: cmd, tty: tty, done: make(chan error, 1)}
	go func() {
		// Act as a minimal terminal emulator: termenv/lipgloss probe the
		// terminal (OSC 11 background color, CSI 6n cursor position) and
		// block rendering until a reply arrives. Real terminals and tmux
		// answer instantly; this harness must too.
		b := make([]byte, 4096)
		for {
			n, err := tty.Read(b)
			if n > 0 {
				chunk := string(b[:n])
				r.mu.Lock()
				r.buf.Write(b[:n])
				r.mu.Unlock()
				if strings.Contains(chunk, "\x1b]11;?") {
					tty.WriteString("\x1b]11;rgb:0000/0000/0000\x07")
				}
				if strings.Contains(chunk, "\x1b[6n") {
					tty.WriteString("\x1b[1;1R")
				}
			}
			if err != nil {
				return
			}
		}
	}()
	go func() { r.done <- cmd.Wait() }()
	t.Cleanup(func() {
		tty.Close()
		cmd.Process.Kill()
	})
	return r
}

func (r *ptyRun) output() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

func (r *ptyRun) waitFor(t *testing.T, substr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(r.output(), substr) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	dump := filepath.Join(os.TempDir(), "reup-itest-dump.txt")
	_ = os.WriteFile(dump, []byte(r.output()), 0o644)
	t.Fatalf("timeout waiting for %q in picker output; full buffer dumped to %s; last screen:\n%s",
		substr, dump, tail(r.output(), 2000))
}

func (r *ptyRun) send(s string) {
	r.tty.WriteString(s)
}

func (r *ptyRun) waitExit(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-r.done:
	case <-time.After(timeout):
		t.Fatal("picker process did not exit in time")
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func waitForFile(t *testing.T, path string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return string(b)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("file %s never appeared", path)
	return ""
}

// --- 12: stale-cwd exec through the real TUI ---

func TestStaleCwdExec(t *testing.T) {
	materializeFixtureCwds(t)
	logPath := filepath.Join(t.TempDir(), "codebuddy-mock.log")
	env := append(fixtureEnv(t), "CODEBUDDY_MOCK_LOG="+logPath)

	r := startPicker(t, env, "--source=codebuddy", "--all")
	r.waitFor(t, "codebuddy", 5*time.Second)

	// Filter down to the stale-cwd fixture, apply, select.
	r.send("/stale cwd regression")
	time.Sleep(400 * time.Millisecond)
	r.send("\r") // apply filter
	time.Sleep(300 * time.Millisecond)
	r.send("\r") // resume selection
	r.waitExit(t, 5*time.Second)

	log := waitForFile(t, logPath, 5*time.Second)
	t.Logf("mock codebuddy log: %s", strings.TrimSpace(log))

	pwdRE := regexp.MustCompile(`pwd=(/private)?/tmp/reup/fixtures/obsidian path with space/vault`)
	if !pwdRE.MatchString(log) {
		t.Errorf("expected walk to real vault path, log: %q", log)
	}
	if !strings.Contains(log, "args=--resume cb222222-2222-4222-8222-222222222222") {
		t.Errorf("expected codebuddy --resume with correct uuid, log: %q", log)
	}
	if strings.Contains(log, "pwd=/bogus/wrong/path") {
		t.Error("pwd ended at stored bogus cwd — stale-cwd fix not applied")
	}
}

// --- picker cancel (port of the 07/09 interaction essentials) ---

func TestPickerCancelLeavesNoLog(t *testing.T) {
	materializeFixtureCwds(t)
	logPath := filepath.Join(t.TempDir(), "codebuddy-mock.log")
	env := append(fixtureEnv(t), "CODEBUDDY_MOCK_LOG="+logPath)

	r := startPicker(t, env, "--all")
	// Wait for any rendered row; the grouped list only shows the most recent
	// page, so the oldest fixtures are off-screen here.
	r.waitFor(t, "kimi-code", 5*time.Second)
	r.send("\x1b") // esc → cancel
	r.waitExit(t, 5*time.Second)

	if _, err := os.Stat(logPath); err == nil {
		t.Error("cancel must not exec the resume binary")
	}
}

// --- sort toggle + source cycle interactions ---

func TestSortToggleAndSourceCycle(t *testing.T) {
	materializeFixtureCwds(t)
	logPath := filepath.Join(t.TempDir(), "codebuddy-mock.log")
	env := append(fixtureEnv(t), "CODEBUDDY_MOCK_LOG="+logPath)

	r := startPicker(t, env, "--all")
	r.waitFor(t, "kimi-code", 5*time.Second)

	// ctrl-s twice (asc → desc again), tab through sources and back to all.
	r.send("\x13") // ctrl+s
	time.Sleep(200 * time.Millisecond)
	r.send("\x13")
	time.Sleep(200 * time.Millisecond)
	r.send("\t") // → codebuddy only
	time.Sleep(200 * time.Millisecond)
	r.waitFor(t, "source: codebuddy", 3*time.Second)
	r.send("\t") // → kimi-code only
	time.Sleep(200 * time.Millisecond)
	r.waitFor(t, "source: kimi-code", 3*time.Second)
	r.send("\t") // → opencode only
	time.Sleep(200 * time.Millisecond)
	r.waitFor(t, "source: opencode", 3*time.Second)
	// Return to all sources.
	r.send("\t")
	time.Sleep(150 * time.Millisecond)
	r.waitFor(t, "source: all", 3*time.Second)

	// Toggling must leave the picker functional and must not exec anything;
	// exec behavior itself is covered by the dedicated tests above.
	r.send("\x1b") // cancel
	r.waitExit(t, 5*time.Second)

	if _, err := os.Stat(logPath); err == nil {
		t.Error("sort/source toggling must not trigger an exec")
	}
}

// --- codebuddy selection end-to-end ---

func TestCodeBuddySelectExec(t *testing.T) {
	materializeFixtureCwds(t)
	logPath := filepath.Join(t.TempDir(), "codebuddy-mock.log")
	env := append(fixtureEnv(t), "CODEBUDDY_MOCK_LOG="+logPath)

	// --project narrows to the alpha session; the vault session (newest
	// codebuddy row) carries a bogus cwd and is covered by TestStaleCwdExec.
	r := startPicker(t, env, "--source=codebuddy", "--all", "--project", "codebuddy-alpha")
	r.waitFor(t, "codebuddy", 5*time.Second)
	r.send("\r")
	r.waitExit(t, 5*time.Second)

	log := waitForFile(t, logPath, 5*time.Second)
	if !strings.Contains(log, "args=--resume cb111111-1111-4111-8111-111111111111") {
		t.Errorf("expected CodeBuddy resume with session ID, log: %q", log)
	}
	// macOS resolves /tmp to /private/tmp, so the mock's pwd may carry the
	// physical prefix — match both, like the other pwd assertions here.
	pwdRE := regexp.MustCompile(`pwd=(/private)?/tmp/reup/fixtures/codebuddy-alpha`)
	if !pwdRE.MatchString(log) {
		t.Errorf("expected exec from the recorded project directory, log: %q", log)
	}
}

// --- kimi-code selection end-to-end ---

func TestKimiSelectExec(t *testing.T) {
	materializeFixtureCwds(t)
	logPath := filepath.Join(t.TempDir(), "kimi-mock.log")
	env := append(fixtureEnv(t), "KIMI_MOCK_LOG="+logPath)

	r := startPicker(t, env, "--source=kimi-code", "--all")
	r.waitFor(t, "kimi-code", 5*time.Second)
	// Top row is the most recent kimi session (kimi-two, 06:32). Select it;
	// exec must chdir into the workDir recorded in session_index.jsonl.
	r.send("\r")
	r.waitExit(t, 5*time.Second)

	log := waitForFile(t, logPath, 5*time.Second)
	if !strings.Contains(log, "args=--session dddd0002-2222-7000-8000-000000000002") {
		t.Errorf("expected kimi --session of most-recent session, log: %q", log)
	}
	pwdRE := regexp.MustCompile(`pwd=(/private)?/tmp/reup/fixtures/kimi-two`)
	if !pwdRE.MatchString(log) {
		t.Errorf("expected exec from the session workDir, log: %q", log)
	}
}

// --- opencode selection end-to-end ---

func TestOpenCodeSelectExec(t *testing.T) {
	materializeFixtureCwds(t)
	logPath := filepath.Join(t.TempDir(), "opencode-mock.log")
	env := append(fixtureEnv(t), "OPENCODE_MOCK_LOG="+logPath)

	r := startPicker(t, env, "--source=opencode", "--all")
	r.waitFor(t, "opencode", 5*time.Second)
	// Top row is ses_ddd (06:21, oc-three) — newest visible opencode session
	// (archived and child fixtures are filtered out).
	r.send("\r")
	r.waitExit(t, 5*time.Second)

	log := waitForFile(t, logPath, 5*time.Second)
	if !strings.Contains(log, "args=--session ses_dddddddddddddddddddddddddddd") {
		t.Errorf("expected opencode --session of most-recent session, log: %q", log)
	}
	pwdRE := regexp.MustCompile(`pwd=(/private)?/tmp/reup/fixtures/oc-three`)
	if !pwdRE.MatchString(log) {
		t.Errorf("expected exec from the session directory, log: %q", log)
	}
}

// After the agent exits, reup prints a `cd <dir>` hint for the session's
// directory (the parent shell never moved). The picker was started from a
// directory outside the session dir, so the hint must appear and point at it.
func TestResumePrintsCdHint(t *testing.T) {
	materializeFixtureCwds(t)
	logPath := filepath.Join(t.TempDir(), "opencode-mock.log")
	env := append(fixtureEnv(t), "OPENCODE_MOCK_LOG="+logPath)

	r := startPicker(t, env, "--source=opencode", "--all")
	r.waitFor(t, "opencode", 5*time.Second)
	r.send("\r")
	r.waitExit(t, 5*time.Second)

	out := r.output()
	if !strings.Contains(out, "cd /tmp/reup/fixtures/oc-three") {
		t.Errorf("expected a cd hint for the session dir, output:\n%s", out)
	}
}

// --- delete via the agent's official CLI (opencode) ---

func TestOpenCodeHideFlow(t *testing.T) {
	materializeFixtureCwds(t)
	logPath := filepath.Join(t.TempDir(), "opencode-mock.log")
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	env := append(fixtureEnv(t),
		"OPENCODE_MOCK_LOG="+logPath,
		"REUP_CONFIG="+cfgPath)

	r := startPicker(t, env, "--source=opencode", "--all")
	r.waitFor(t, "opencode", 5*time.Second)
	// d opens the hide modal for the selected (newest) session: opencode has
	// no official archive, so the hide is reup-side.
	r.send("d")
	r.waitFor(t, "HIDE SESSION", 3*time.Second)
	r.send("\r") // confirm hide
	time.Sleep(700 * time.Millisecond)
	r.send("\x1b")
	r.waitExit(t, 5*time.Second)

	// The hide is a config entry — the agent's own CLI must NOT be invoked.
	if data, err := os.ReadFile(logPath); err == nil &&
		strings.Contains(string(data), "session delete") {
		t.Errorf("hide must not call the agent CLI, log: %q", string(data))
	}
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("config after hide: %v", err)
	}
	if !strings.Contains(string(cfg), "ses_dddddddddddddddddddddddddddd") {
		t.Errorf("config should record the hidden session, got: %s", string(cfg))
	}
}

// --- 13: stale cwd — refuse instead of handing the user an opaque error ---

// kimi-four records /tmp/reup/fixtures/kimi-four, which
// materializeFixtureCwds deliberately does not create: the directory is gone,
// exactly like a renamed, moved, or migrated checkout.
func TestSelectRefusesWhenWorkingDirectoryIsGone(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "kimi-mock.log")
	env := append(fixtureEnv(t), "KIMI_MOCK_LOG="+logPath)

	r := startPicker(t, env, "--source=kimi-code", "--project", "kimi-four")
	// The project column must say the directory is gone before the user
	// commits to the row.
	r.waitFor(t, textutil.DirDeletedLabel, 5*time.Second)
	r.send("\r")
	// Enter must not exec, and the console must say the session cannot be
	// resumed and what to do about it.
	r.waitFor(t, "cannot resume", 5*time.Second)
	r.waitFor(t, "has been deleted", 5*time.Second)
	r.waitFor(t, "mkdir -p", 5*time.Second)

	select {
	case err := <-r.done:
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 3 {
			t.Errorf("exit status = %v, want 3 (cannot resume)", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("picker process did not exit after the refusal")
	}

	if b, err := os.ReadFile(logPath); err == nil {
		t.Errorf("the agent CLI must not run when the cwd is gone, log: %q", b)
	}
}
