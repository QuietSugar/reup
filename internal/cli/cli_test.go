package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuietSugar/reup/internal/config"
	"github.com/QuietSugar/reup/internal/provider"
)

func TestNormalizeSubcommands(t *testing.T) {
	cases := []struct {
		argv    []string
		command string
		args    []string
	}{
		{[]string{}, "", nil},
		{[]string{"list", "--json"}, "list", []string{"--json"}},
		{[]string{"--json"}, "", []string{"--json"}},
		{[]string{"--project", "list", "--json"}, "", []string{"--project", "list", "--json"}},
		{[]string{"provider"}, "provider", nil},
		{[]string{"provider", "off", "opencode"}, "provider", []string{"off", "opencode"}},
		{[]string{"provider", "list"}, "provider", []string{"list"}},
		// Only the first bare subcommand token is the command.
		{[]string{"list", "provider"}, "list", []string{"provider"}},
	}
	for _, tc := range cases {
		cmd, args := normalize(tc.argv)
		if cmd != tc.command {
			t.Errorf("normalize(%v) command = %q, want %q", tc.argv, cmd, tc.command)
		}
		if strings.Join(args, "\x00") != strings.Join(tc.args, "\x00") {
			t.Errorf("normalize(%v) args = %v, want %v", tc.argv, args, tc.args)
		}
	}
}

func setConfigPath(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "reup", "config.json")
	t.Setenv("REUP_CONFIG", p)
	return p
}

// captureStdout swaps os.Stdout for the duration of fn.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	prev := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	fn()
	os.Stdout = prev
	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestProviderCmdOffListOn(t *testing.T) {
	setConfigPath(t)
	registerProviders()

	if code := runProviderCmd([]string{"off", "opencode"}); code != 0 {
		t.Fatalf("provider off exit = %d", code)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Disabled) != 1 || cfg.Disabled[0] != "opencode" {
		t.Fatalf("config after off = %v", cfg.Disabled)
	}

	// Second off is idempotent, list reflects state.
	if code := runProviderCmd([]string{"off", "opencode"}); code != 0 {
		t.Fatalf("repeat off exit = %d", code)
	}
	out := captureStdout(t, func() { runProviderCmd([]string{"list"}) })
	if !strings.Contains(out, "opencode") || !strings.Contains(out, "off") {
		t.Errorf("provider list output missing off row:\n%s", out)
	}

	// The disabled set flows into the registry filter.
	cfg, _ = config.Load()
	provider.SetDisabled(cfg.Disabled)
	for _, p := range provider.Active() {
		if p.Name() == "opencode" {
			t.Error("disabled opencode must not be Active")
		}
	}
	provider.SetDisabled(nil)

	if code := runProviderCmd([]string{"on", "opencode"}); code != 0 {
		t.Fatalf("provider on exit = %d", code)
	}
	cfg, _ = config.Load()
	if len(cfg.Disabled) != 0 {
		t.Fatalf("config after on = %v", cfg.Disabled)
	}
}

func TestProviderCmdErrors(t *testing.T) {
	setConfigPath(t)
	registerProviders()

	if code := runProviderCmd([]string{"off"}); code != 2 {
		t.Errorf("off without name exit = %d", code)
	}
	if code := runProviderCmd([]string{"off", "nope"}); code != 2 {
		t.Errorf("off unknown exit = %d", code)
	}
	if code := runProviderCmd([]string{"repl"}); code != 2 {
		t.Errorf("unknown subcommand exit = %d", code)
	}
	// Malformed config must not be silently overwritten by a mutation.
	p := setConfigPath(t)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runProviderCmd([]string{"off", "opencode"}); code != 2 {
		t.Errorf("off with malformed config exit = %d, want 2", code)
	}
	if got, _ := os.ReadFile(p); string(got) != "{bad" {
		t.Errorf("malformed config was overwritten: %q", got)
	}
}

func TestRunAppliesConfigToRegistry(t *testing.T) {
	setConfigPath(t)
	registerProviders()
	var cfg config.Config
	cfg.Disable("kimi")
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { provider.SetDisabled(nil) })

	// list --json with fixture roots; the point is Run() applying the config
	// (SetDisabled) before scanning, so kimi is never queried. Give every
	// provider a real or empty-but-existing root so nothing errors.
	dir := t.TempDir()
	// codebuddy only needs projects/ to exist — its availability check has no
	// binary requirement, so it stays available with no mocks on PATH.
	if err := os.MkdirAll(filepath.Join(dir, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REUP_CODEBUDDY_HOME", dir)
	t.Setenv("REUP_KIMI_HOME", dir)
	t.Setenv("REUP_KIMI_BIN", "kimi-missing-for-test")
	t.Setenv("REUP_OPENCODE_DATA", dir)
	t.Setenv("REUP_OPENCODE_BIN", "opencode-missing-for-test")
	t.Setenv("XDG_STATE_HOME", dir)

	code := Run([]string{"list", "--json"}, "test")
	if code != 0 {
		t.Fatalf("Run list --json exit = %d", code)
	}
	// Run must have pushed the config into the registry filter.
	if provider.IsEnabled("kimi") {
		t.Error("Run did not apply disabled config to the registry")
	}
}

// reup hidden lists the hidden keys; restore shows one again.
func TestHiddenCmdListAndRestore(t *testing.T) {
	setConfigPath(t)
	cfg := config.Config{}
	cfg.Hide("kimi-code\x00ses_1")
	cfg.Hide("codebuddy\x00ses_2")
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	if code := Run([]string{"hidden"}, "test"); code != 0 {
		t.Fatalf("hidden list exit = %d", code)
	}
	if code := Run([]string{"hidden", "restore", "kimi-code", "ses_1"}, "test"); code != 0 {
		t.Fatalf("hidden restore exit = %d", code)
	}
	got, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Hidden) != 1 || got.Hidden[0] != "codebuddy\x00ses_2" {
		t.Errorf("hidden after restore = %v", got.Hidden)
	}
	// Restoring an entry that is not hidden exits 2.
	if code := Run([]string{"hidden", "restore", "kimi-code", "ses_1"}, "test"); code != 2 {
		t.Errorf("double restore exit = %d, want 2", code)
	}
}
