package integration

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runBin executes the built binary without a TTY and returns its combined
// output plus exit code.
func runBin(t *testing.T, env []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("exec %v: %v", args, err)
	}
	return string(out), code
}

// TestProviderToggle proves the on/off lifecycle against the real binary:
// a disabled provider disappears from ambient scans entirely and comes back
// after `provider on`.
func TestProviderToggle(t *testing.T) {
	materializeFixtureCwds(t)
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	env := append(fixtureEnv(t), "REUP_CONFIG="+cfgPath)

	// opencode sessions are visible before the toggle.
	out, code := runBin(t, env, "list", "--json", "--all")
	if code != 0 || !strings.Contains(out, `"opencode"`) {
		t.Fatalf("baseline list: exit=%d opencode present=%v\n%s", code, strings.Contains(out, `"opencode"`), out)
	}

	// Turn it off.
	out, code = runBin(t, env, "provider", "off", "opencode")
	if code != 0 || !strings.Contains(out, "opencode disabled") {
		t.Fatalf("provider off: exit=%d out=%q", code, out)
	}

	// provider list shows the state.
	out, code = runBin(t, env, "provider", "list")
	if code != 0 {
		t.Fatalf("provider list: exit=%d\n%s", code, out)
	}
	if !strings.Contains(out, "opencode") || !strings.Contains(out, "off") {
		t.Errorf("provider list missing off row:\n%s", out)
	}

	// Ambient scan skips the provider completely.
	out, code = runBin(t, env, "list", "--json", "--all")
	if code != 0 {
		t.Fatalf("list after off: exit=%d\n%s", code, out)
	}
	if strings.Contains(out, `"opencode"`) {
		t.Errorf("disabled opencode still scanned:\n%s", out)
	}
	if !strings.Contains(out, `"codebuddy"`) {
		t.Errorf("other providers lost by the toggle:\n%s", out)
	}

	// The picker honors it too: --source stays available (explicit override),
	// but a source-scoped ambient error is NOT the same path — just verify
	// the picker's source list via list mode's source filter on kimi only.
	out, code = runBin(t, env, "list", "--json", "--all", "--source", "kimi-code")
	if code != 0 || !strings.Contains(out, `"kimi-code"`) {
		t.Errorf("source list after off: exit=%d\n%s", code, out)
	}

	// Turn it back on — sessions reappear.
	out, code = runBin(t, env, "provider", "on", "opencode")
	if code != 0 || !strings.Contains(out, "opencode enabled") {
		t.Fatalf("provider on: exit=%d out=%q", code, out)
	}
	out, code = runBin(t, env, "list", "--json", "--all")
	if code != 0 || !strings.Contains(out, `"opencode"`) {
		t.Errorf("list after on: exit=%d opencode present=%v\n%s",
			code, strings.Contains(out, `"opencode"`), out)
	}
}

// TestProviderToggleAllDisabled verifies the ambient no-provider error
// points at `provider on` once every detected provider is disabled.
func TestProviderToggleAllDisabled(t *testing.T) {
	materializeFixtureCwds(t)
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	env := append(fixtureEnv(t), "REUP_CONFIG="+cfgPath)

	for _, name := range []string{"codebuddy", "kimi-code", "opencode"} {
		if out, code := runBin(t, env, "provider", "off", name); code != 0 {
			t.Fatalf("provider off %s: exit=%d out=%q", name, code, out)
		}
	}
	out, code := runBin(t, env, "list")
	if code != 2 {
		t.Fatalf("list with everything disabled: exit=%d out=%q", code, out)
	}
	if !strings.Contains(out, "no enabled session providers") ||
		!strings.Contains(out, "provider on") {
		t.Errorf("error should hint at re-enabling, got: %q", out)
	}
}
