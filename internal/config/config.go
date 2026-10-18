// Package config persists reup's user settings. Today that is exactly one
// thing: which providers are disabled. A disabled provider is excluded from
// every ambient scan — reup neither walks its storage nor parses its
// files — until it is turned back on.
//
// The file lives at $XDG_CONFIG_HOME/reup/config.json (default
// ~/.config/reup/config.json); $REUP_CONFIG overrides the whole path so
// tests and sandboxed runs can redirect it.
package config

import (
	"encoding/json"
	"fmt"
	"github.com/QuietSugar/reup/internal/brand"
	"os"
	"path/filepath"
	"sort"
)

// Config is the on-disk document. Field names are stable API: older reup
// versions must keep reading newer files (unknown fields are ignored by
// json.Unmarshal) and vice versa.
type Config struct {
	// Disabled lists provider names that are turned off. Unknown names are
	// harmless and preserved, so a config written while a provider exists
	// still round-trips after an upgrade/downgrade.
	Disabled []string `json:"disabled,omitempty"`
	// Hidden lists session keys ("source\x00sessionID") the user asked
	// reup to stop showing. Hiding is reup-side only: the session
	// stays on disk and in the agent's own UI.
	Hidden []string `json:"hidden,omitempty"`
}

// envOverride is the explicit config-path override ($REUP_CONFIG).
var envOverride = brand.Env("CONFIG")

// Path returns the config file location, honoring $REUP_CONFIG and
// $XDG_CONFIG_HOME.
func Path() string {
	if p := os.Getenv(envOverride); p != "" {
		return p
	}
	return filepath.Join(baseDir(), brand.Dir, "config.json")
}

// baseDir is the XDG config root (default ~/.config).
func baseDir() string {
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return base
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config")
}

// migrateLegacy copies a resumer-era config into the reup location on the
// first run after the rename: config.json (provider toggles + hidden
// sessions) and tips.md, each only when the counterpart is absent. The old
// directory is kept untouched — this is a copy, not a move.
//
// Deprecation policy: remove this once the installed base has moved
// (not before 2027-01); until then it is cheap and invisible.
func migrateLegacy() {
	if os.Getenv(envOverride) != "" {
		// An explicit config path is a deliberate choice (tests, CI,
		// sandboxes) — legacy coupling would pull foreign data into it.
		return
	}
	newPath := Path()
	if _, err := os.Stat(newPath); err == nil {
		return // nothing to migrate onto an existing config
	}
	oldPath := filepath.Join(baseDir(), "resumer", "config.json")
	data, err := os.ReadFile(oldPath)
	if err != nil {
		return // no legacy config (or unreadable): silently stay default
	}
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return
	}
	if err := os.WriteFile(newPath, data, 0o644); err != nil {
		return
	}
	fmt.Fprintf(os.Stderr, "note: migrated config %s -> %s\n", oldPath, newPath)

	oldTips := filepath.Join(baseDir(), "resumer", "tips.md")
	newTips := filepath.Join(filepath.Dir(newPath), "tips.md")
	if data, err := os.ReadFile(oldTips); err == nil {
		if _, err := os.Stat(newTips); err != nil {
			_ = os.WriteFile(newTips, data, 0o644)
		}
	}
}

// Load reads the config. A missing file yields the zero Config and a nil
// error. A malformed file yields an error — callers decide whether to fail
// hard (mutating commands) or degrade with a warning (read-only paths).
func Load() (Config, error) {
	migrateLegacy()
	var c Config
	data, err := os.ReadFile(Path())
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("%s: %w", Path(), err)
	}
	return c, nil
}

// Save writes the config atomically (temp file + rename) so a crash cannot
// leave a half-written document behind.
func Save(c Config) error {
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(p), ".config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// IsDisabled reports whether name is in the disabled list.
func (c Config) IsDisabled(name string) bool {
	for _, d := range c.Disabled {
		if d == name {
			return true
		}
	}
	return false
}

// Disable adds name to the disabled list (idempotent, order-stable).
func (c *Config) Disable(name string) {
	if c.IsDisabled(name) {
		return
	}
	c.Disabled = append(c.Disabled, name)
	sort.Strings(c.Disabled)
}

// Enable removes name from the disabled list.
func (c *Config) Enable(name string) {
	out := c.Disabled[:0]
	for _, d := range c.Disabled {
		if d != name {
			out = append(out, d)
		}
	}
	c.Disabled = out
}

// IsHidden reports whether the session key ("source\x00sessionID") is hidden.
func (c Config) IsHidden(key string) bool {
	for _, h := range c.Hidden {
		if h == key {
			return true
		}
	}
	return false
}

// Hide adds the session key to the hidden list (idempotent, order-stable).
func (c *Config) Hide(key string) {
	if c.IsHidden(key) {
		return
	}
	c.Hidden = append(c.Hidden, key)
	sort.Strings(c.Hidden)
}

// Unhide removes the session key from the hidden list.
func (c *Config) Unhide(key string) {
	out := c.Hidden[:0]
	for _, h := range c.Hidden {
		if h != key {
			out = append(out, h)
		}
	}
	c.Hidden = out
}
