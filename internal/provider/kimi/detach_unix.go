//go:build unix

package kimi

import (
	"os/exec"
	"syscall"
)

// detach gives the spawned server its own session so it survives the picker:
// no controlling terminal, no signal propagation from reup, no browser.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
