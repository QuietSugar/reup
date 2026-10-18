//go:build !unix

package kimi

import "os/exec"

// detach is a no-op where process sessions are unavailable; the child still
// runs with stdio redirected to the log file.
func detach(cmd *exec.Cmd) {}
