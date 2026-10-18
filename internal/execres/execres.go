// Package execres runs the provider's resume command as a foreground child
// with inherited stdio, propagating its exit code.
//
// A child (not syscall.Exec) is deliberate: after the agent exits, reup is
// still alive and can print a follow-up (currently the `cd <dir>` hint back
// to the session's directory). While the child runs, SIGINT is swallowed so
// Ctrl+C goes to the agent alone — it owns the foreground.
package execres

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// Run executes path with argv as a foreground child and returns its exit
// code. Non-zero exits and spawn failures are already reported by the child
// itself; Run only echoes unexpected wait errors.
func Run(path string, argv []string) int {
	cmd := exec.Command(path, argv[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// The agent owns the terminal; reup must survive the user's Ctrl+C
	// that is delivered to the whole foreground group.
	ints := make(chan os.Signal, 1)
	signal.Notify(ints, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(ints)
	go func() {
		for range ints { // swallowed: the agent handles Ctrl+C
		}
	}()

	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}
