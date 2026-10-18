// Package brand is the single source of truth for the project's identity:
// its name, config directory, and environment-variable prefix. Renaming the
// project means editing this one file (plus go.mod and the import paths).
package brand

import "strings"

const (
	// Name is the binary and project name.
	Name = "reup"
	// Dir is the config directory under $XDG_CONFIG_HOME.
	Dir = "reup"
	// EnvPrefix prefixes every environment variable (REUP_CONFIG, ...).
	EnvPrefix = "REUP_"
)

// Env builds an environment variable name from the brand prefix.
func Env(suffix string) string { return EnvPrefix + strings.ToUpper(suffix) }
