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
		"d hides a session: kimi via its",
		"official archive, others via a",
		"reup-only hidden list.",
		"",
	}
	switch source {
	case "opencode":
		lines = append(lines,
			"OpenCode: d hides in reup",
			"only (no official archive API).",
			"Real delete: opencode session",
			"delete <sessionID>",
		)
	case "kimi-code":
		lines = append(lines,
			"Kimi: d = official archive",
			"(restorable, hidden in kimi",
			"too). kimi web is started and",
			"stopped automatically.",
		)
	case "codebuddy":
		lines = append(lines,
			"CodeBuddy: d hides in reup",
			"only (no official per-session",
			"delete; cleanup is day-based).",
			"Files stay untouched.",
		)
	default:
		lines = append(lines,
			"Use this Agent's own CLI/TUI",
			"session manager. The exact",
			"steps differ by Agent/version.",
		)
	}
	lines = append(lines,
		"",
		"Hidden list: reup hidden.",
	)
	return strings.Join(lines, "\n")
}
