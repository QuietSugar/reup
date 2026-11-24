package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTipsForSourceProviderManagement(t *testing.T) {
	tests := []struct {
		source string
		want   []string
	}{
		{source: "agy", want: []string{"/resume", "F4"}},
		{source: "opencode", want: []string{"ctrl+d", "opencode session delete <id>"}},
		{source: "kimi-code", want: []string{"official archive", "kimi web", "global UI"}},
		{source: "codebuddy", want: []string{"codebuddy project", "purge [path]", "--dry-run"}},
		{source: "dsh", want: []string{"Harness", "archive/delete"}},
		{source: "", want: []string{"provider tab", "delete/archive"}},
	}
	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			tips := tipsForSource(tt.source)
			for _, want := range tt.want {
				if !strings.Contains(tips, want) {
					t.Errorf("tipsForSource(%q) missing %q:\n%s", tt.source, want, tips)
				}
			}
		})
	}
}

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
