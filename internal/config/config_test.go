package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setPath(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "reup", "config.json")
	t.Setenv("REUP_CONFIG", p)
	return p
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	setPath(t)
	c, err := Load()
	if err != nil {
		t.Fatalf("Load missing file: %v", err)
	}
	if len(c.Disabled) != 0 {
		t.Errorf("expected zero config, got %+v", c)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	p := setPath(t)
	var c Config
	c.Disable("codebuddy")
	c.Disable("opencode")
	c.Disable("opencode") // idempotent
	if err := Save(c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Disabled) != 2 || got.Disabled[0] != "codebuddy" || got.Disabled[1] != "opencode" {
		t.Errorf("round-trip = %+v", got.Disabled)
	}
	if !got.IsDisabled("opencode") || got.IsDisabled("kimi-code") {
		t.Errorf("IsDisabled wrong: %+v", got)
	}
	// Parent dirs were created on demand.
	if _, err := os.Stat(filepath.Dir(p)); err != nil {
		t.Errorf("parent dir: %v", err)
	}
}

func TestEnableRemovesEntry(t *testing.T) {
	var c Config
	c.Disable("codebuddy")
	c.Disable("opencode")
	c.Enable("codebuddy")
	if c.IsDisabled("codebuddy") || !c.IsDisabled("opencode") {
		t.Errorf("after Enable: %+v", c.Disabled)
	}
	c.Enable("not-there") // no-op
	if len(c.Disabled) != 1 {
		t.Errorf("Enable of missing name changed list: %+v", c.Disabled)
	}
}

func TestLoadMalformedErrors(t *testing.T) {
	p := setPath(t)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("expected error for malformed config")
	}
}

func TestPathRespectsXDG(t *testing.T) {
	t.Setenv("REUP_CONFIG", "")
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	want := filepath.Join(xdg, "reup", "config.json")
	if got := Path(); got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

// The resumer-era config migrates once on first run; the legacy directory is
// kept (copy, not move) and an existing new config is never overwritten.
func TestLegacyConfigMigration(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	t.Setenv("REUP_CONFIG", "")

	legacyDir := filepath.Join(base, "resumer")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"hidden":["codebuddy\u0000ses_1"],"disabled":["opencode"]}`
	if err := os.WriteFile(filepath.Join(legacyDir, "config.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "tips.md"), []byte("custom tips"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hidden) != 1 || !cfg.IsHidden("codebuddy\x00ses_1") {
		t.Errorf("hidden keys not migrated: %v", cfg.Hidden)
	}
	if !cfg.IsDisabled("opencode") {
		t.Error("disabled providers not migrated")
	}
	newCfg := filepath.Join(base, "reup", "config.json")
	if data, err := os.ReadFile(newCfg); err != nil || !strings.Contains(string(data), "hidden") {
		t.Errorf("config not copied to the new location: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(base, "reup", "tips.md")); err != nil || string(data) != "custom tips" {
		t.Errorf("tips.md not migrated: %v", err)
	}
	// Legacy directory stays — a copy, not a move.
	if _, err := os.Stat(filepath.Join(legacyDir, "config.json")); err != nil {
		t.Error("legacy config must be preserved")
	}

	// Second run with an existing new config: no overwrite.
	if err := os.WriteFile(newCfg, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg2, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg2.Hidden) != 0 {
		t.Error("existing new config must not be clobbered by migration")
	}
}

// An explicit REUP_CONFIG is a deliberate location (tests, CI): the legacy
// migration must never pull foreign data into it.
func TestNoMigrationWithExplicitConfigPath(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("REUP_CONFIG", cfgPath)

	legacyDir := filepath.Join(base, "resumer")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "config.json"),
		[]byte(`{"hidden":["dsh\u0000ses_9"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hidden) != 0 {
		t.Errorf("foreign data migrated into an explicit path: %v", cfg.Hidden)
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Error("explicit config file must not be created by migration")
	}
}
