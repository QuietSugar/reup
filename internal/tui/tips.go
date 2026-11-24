package tui

import (
	"os"
	"path/filepath"
	"strings"
)

// customTipsPath resolves the optional user-defined tips file. The environment
// variable takes precedence over the standard per-user config location.
func customTipsPath() string {
	if path := strings.TrimSpace(os.Getenv("REUP_TIPS_FILE")); path != "" {
		if path == "~" || strings.HasPrefix(path, "~"+string(os.PathSeparator)) {
			home, err := os.UserHomeDir()
			if err == nil {
				if path == "~" {
					path = home
				} else {
					path = filepath.Join(home, strings.TrimPrefix(path, "~"+string(os.PathSeparator)))
				}
			}
		}
		return path
	}

	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "reup", "tips.md")
}

// loadCustomTips reads the optional plain-text tips file once when the picker
// starts. An unset, missing, unreadable, or blank file falls back to built-in
// provider tips.
func loadCustomTips() string {
	path := customTipsPath()
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// tipsForSource returns the built-in tips text for a provider. Custom tips
// (tips.md) replace this content in the help popup when present.
func tipsForSource(source string) string {
	lines := []string{
		"Session tips",
		"d: kimi = official archive;",
		"other providers = reup-only hide.",
		"",
	}
	switch source {
	case "agy":
		lines = append(lines,
			"Antigravity CLI: run agy, then",
			"enter /resume to manage sessions.",
			"F4 deletes; no official archive.",
		)
	case "opencode":
		lines = append(lines,
			"OpenCode: TUI ctrl+d deletes.",
			"CLI: opencode session delete <id>",
			"No official archive; d = reup hide.",
		)
	case "kimi-code":
		lines = append(lines,
			"Kimi: d = official archive",
			"(restorable). Run kimi web to",
			"manage all sessions in its global UI.",
		)
	case "codebuddy":
		lines = append(lines,
			"CodeBuddy: no per-session delete.",
			"Project cleanup: codebuddy project",
			"purge [path] (try --dry-run first).",
			"This removes project state, not just",
			"one session.",
		)
	case "dsh":
		lines = append(lines,
			"DeepSeek Harness: use the Harness",
			"UI to archive/delete sessions.",
			"reup detects archives; d = reup hide.",
		)
	default:
		lines = append(lines,
			"Switch to a provider tab for its",
			"official delete/archive instructions.",
		)
	}
	lines = append(lines,
		"",
		"Hidden list: reup hidden.",
	)
	return strings.Join(lines, "\n")
}
