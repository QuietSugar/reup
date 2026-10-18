package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCustomTipsFallsBackWhenFileIsMissing(t *testing.T) {
	t.Setenv("REUP_TIPS_FILE", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if got := loadCustomTips(); got != "" {
		t.Fatalf("missing tips file loaded unexpected content: %q", got)
	}
}

func TestLoadCustomTipsFromDefaultPath(t *testing.T) {
	t.Setenv("REUP_TIPS_FILE", "")
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	path := filepath.Join(xdg, "reup", "tips.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("  My custom tips\nUse /resume  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadCustomTips(); got != "My custom tips\nUse /resume" {
		t.Fatalf("loaded custom tips = %q", got)
	}
}

func TestLoadCustomTipsUsesEnvironmentOverride(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "custom-tips.txt")
	if err := os.WriteFile(path, []byte("Override tips"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REUP_TIPS_FILE", path)
	if got := loadCustomTips(); got != "Override tips" {
		t.Fatalf("loaded custom tips = %q", got)
	}
}
