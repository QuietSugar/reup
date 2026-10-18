// The `reup provider` subcommand: state table plus on/off toggling.
package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/QuietSugar/reup/internal/config"
	"github.com/QuietSugar/reup/internal/provider"
)

// runProviderCmd implements `reup provider [list | on NAME | off NAME]`.
// State is persisted to the config file; unknown subcommands exit 2.
func runProviderCmd(args []string) int {
	sub := "" // bare `reup provider` — the interactive toggle screen
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}

	switch sub {
	case "":
		// Bare `reup provider` — the plain table. The picker's P modal
		// is the interactive toggle; a second TUI here is duplication.
		cfg, cfgErr := config.Load()
		if cfgErr != nil {
			fmt.Fprintf(os.Stderr, "error: cannot read config: %v\n", cfgErr)
			return 2
		}
		return printProviderList(cfg)

	case "list":
		cfg, err := config.Load()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 2
		}
		return printProviderList(cfg)

	case "on", "off":
		if len(args) == 0 {
			fmt.Fprintf(os.Stderr, "error: provider %s requires a name (choose from %s)\n",
				sub, strings.Join(providerNames(), ", "))
			return 2
		}
		name := args[0]
		if provider.Get(name) == nil {
			fmt.Fprintf(os.Stderr, "error: unknown provider: %q (choose from %s)\n",
				name, strings.Join(providerNames(), ", "))
			return 2
		}
		cfg, err := config.Load()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: refusing to rewrite unreadable config: %v\n", err)
			return 2
		}
		if sub == "off" {
			cfg.Disable(name)
			if err := config.Save(cfg); err != nil {
				fmt.Fprintf(os.Stderr, "error: saving config: %v\n", err)
				return 2
			}
			fmt.Printf("%s disabled — reup will no longer scan or parse it.\n", name)
		} else {
			cfg.Enable(name)
			if err := config.Save(cfg); err != nil {
				fmt.Fprintf(os.Stderr, "error: saving config: %v\n", err)
				return 2
			}
			fmt.Printf("%s enabled.\n", name)
		}
		return 0

	default:
		fmt.Fprintf(os.Stderr,
			"error: unknown provider subcommand: %q (choose from list, on, off)\n", sub)
		return 2
	}
}

// printProviderList writes the plain text state table (also the fallback for
// `reup provider` when stdin is not a TTY).
func printProviderList(cfg config.Config) int {
	fmt.Printf("%-12s %-5s %-10s %s\n", "PROVIDER", "STATE", "STORAGE", "CONFIG")
	for _, p := range provider.All() {
		state, storage := "on", "ok"
		cfgNote := "-"
		if !p.IsAvailable() {
			storage = "missing"
		}
		if cfg.IsDisabled(p.Name()) {
			state = "off"
			cfgNote = "disabled"
		}
		fmt.Printf("%-12s %-5s %-10s %s\n", p.Name(), state, storage, cfgNote)
	}
	return 0
}
