// The `reup hidden` subcommand: list and restore resumer-side hidden sessions.
package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/QuietSugar/reup/internal/config"
)

// runHiddenCmd implements `reup hidden [restore SOURCE SESSION_ID]`.
// Hidden sessions are a reup-side filter only; the agent's own data is
// untouched. With no arguments it lists the hidden keys; restore shows one
// again.
func runHiddenCmd(args []string) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot read config: %v\n", err)
		return 2
	}
	if len(args) == 0 {
		if len(cfg.Hidden) == 0 {
			fmt.Println("no hidden sessions")
			return 0
		}
		fmt.Println("hidden sessions (restore: reup hidden restore SOURCE SESSION_ID):")
		for _, k := range cfg.Hidden {
			src, id, _ := strings.Cut(k, "\x00")
			fmt.Printf("  %-10s %s\n", src, id)
		}
		return 0
	}
	if len(args) != 3 || args[0] != "restore" {
		fmt.Fprintln(os.Stderr, "usage: reup hidden [restore SOURCE SESSION_ID]")
		return 2
	}
	key := args[1] + "\x00" + args[2]
	if !cfg.IsHidden(key) {
		fmt.Fprintf(os.Stderr, "error: %s %s is not hidden\n", args[1], args[2])
		return 2
	}
	cfg.Unhide(key)
	if err := config.Save(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot save config: %v\n", err)
		return 2
	}
	fmt.Printf("restored: %s %s\n", args[1], args[2])
	return 0
}
