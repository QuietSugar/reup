// Package cli is the entry point: flag parsing, dispatch, exit codes, and
// the resume exec flow. Ported from the Python cli.py; the fzf preview hook
// is gone (the native TUI needs no self-reinvocation).
package cli

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/term"

	"github.com/QuietSugar/reup/internal/brand"
	"github.com/QuietSugar/reup/internal/config"
	"github.com/QuietSugar/reup/internal/cwd"
	"github.com/QuietSugar/reup/internal/execres"
	"github.com/QuietSugar/reup/internal/provider"
	"github.com/QuietSugar/reup/internal/provider/codebuddy"
	"github.com/QuietSugar/reup/internal/provider/dsh"
	"github.com/QuietSugar/reup/internal/provider/kimi"
	"github.com/QuietSugar/reup/internal/provider/opencode"
	"github.com/QuietSugar/reup/internal/render"
	"github.com/QuietSugar/reup/internal/session"
	"github.com/QuietSugar/reup/internal/tui"
)

func registerProviders() {
	if len(provider.All()) == 0 {
		provider.Register(codebuddy.New())
		provider.Register(kimi.New())
		provider.Register(opencode.New())
		provider.Register(dsh.New())
	}
}

const usageTemplate = `usage: {{name}} [list] [options]
       {{name}} provider [list | on NAME | off NAME]
       {{name}} hidden [restore SOURCE SESSION_ID]

Unified AI CLI session {{name}}.

  {{name}}              interactive picker across all active providers
  {{name}} list         render merged session list (no interaction)
  {{name}} provider     provider table (state + storage detected); toggle
                       interactively with P inside the picker
  {{name}} hidden       list sessions hidden with the picker's d key;
                       "restore SOURCE SESSION_ID" shows one again

options:
  --source NAME    limit to a single provider (codebuddy | kimi-code | opencode | dsh)
  --days N         only show sessions active in the last N days (default: no limit)
  --date DATE      YYYY-MM-DD — only sessions active on this date
  --all            no time filter
  --project STR    substring match against project name
  --limit N        top N after sort
  --no-group       list mode: flat table, do not group by workspace
  --json           list mode only: emit JSON array of sessions
  --full [N]       list mode only: render detailed boxes for top N (default 5)
  --version        print version

Disabled providers are recorded in ~/.config/{{name}}/config.json (override
with $REUP_CONFIG) and are skipped by ambient scans. An explicit
--source NAME still works on a disabled provider: the flag is a deliberate
per-invocation request.
`

// usageText is the brand-parameterized usage screen.
var usageText = strings.ReplaceAll(usageTemplate, "{{name}}", brand.Name)

// valueFlags take a separate argument (so "list" after them is a value, not
// the subcommand).
var valueFlags = map[string]bool{
	"--source": true, "--days": true, "--date": true,
	"--project": true, "--limit": true,
}

// normalize extracts the optional subcommand ("list" or "provider") and
// rewrites the argparse-style "--full [N]" into flag-friendly "--full=N".
func normalize(argv []string) (command string, out []string) {
	expectValue := false
	for i := 0; i < len(argv); i++ {
		tok := argv[i]
		switch {
		case expectValue:
			expectValue = false
			out = append(out, tok)
		case command == "" && (tok == "list" || tok == "provider" || tok == "hidden"):
			command = tok
		case tok == "--full":
			n := 5
			if i+1 < len(argv) {
				if v, err := strconv.Atoi(argv[i+1]); err == nil {
					n = v
					i++
				}
			}
			out = append(out, fmt.Sprintf("--full=%d", n))
		case valueFlags[tok]:
			expectValue = true
			out = append(out, tok)
		default:
			out = append(out, tok)
		}
	}
	return command, out
}

// Run executes the CLI and returns the process exit code.
func Run(argv []string, version string) int {
	registerProviders()

	command, args := normalize(argv)

	// The provider subcommand has its own mini-parsing (positional args, no
	// flags) and its own config-error policy: mutating commands must not
	// silently proceed on an unreadable config, or Save would clobber it.
	if command == "provider" {
		return runProviderCmd(args)
	}
	if command == "hidden" {
		return runHiddenCmd(args)
	}

	// Every other path only reads the config: degrade to defaults with a
	// warning instead of failing the whole invocation.
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: ignoring unreadable config: %v\n", err)
	}
	provider.SetDisabled(cfg.Disabled)

	fs := flag.NewFlagSet("reup", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usageText) }
	var (
		showVersion = fs.Bool("version", false, "print version")
		source      = fs.String("source", "", "limit to a single provider")
		days        = fs.Int("days", 0, "only show sessions active in the last N days; 0 = no limit")
		date        = fs.String("date", "", "YYYY-MM-DD")
		all         = fs.Bool("all", false, "no time filter")
		project     = fs.String("project", "", "project substring")
		limit       = fs.Int("limit", 0, "top N after sort")
		noGroup     = fs.Bool("no-group", false, "flat table (list mode)")
		jsonOut     = fs.Bool("json", false, "JSON output (list mode)")
		full        = fs.Int("full", -1, "detail boxes for top N (list mode)")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "error: unrecognized argument: %s\n", fs.Arg(0))
		return 2
	}
	if *showVersion {
		fmt.Printf("reup %s\n", version)
		return 0
	}
	if *source != "" && provider.Get(*source) == nil {
		names := provider.AvailableSourceNames()
		sort.Strings(names)
		fmt.Fprintf(os.Stderr,
			"error: argument --source: invalid choice: %q (registered: %s)\n", *source, strings.Join(names, ", "))
		return 2
	}

	filters := session.Filters{
		Days:    *days,
		Date:    *date,
		AllTime: *all,
		Project: *project,
		Limit:   *limit,
		Source:  *source,
	}
	if *all {
		filters.Days = -1
	}

	// Global availability check only when no specific source requested;
	// otherwise MergedList returns a source-specific error with better
	// diagnostics. A disabled provider counts as "handled": point the user
	// at `provider on` rather than claiming nothing is installed.
	if *source == "" && len(provider.AvailableSourceNames()) == 0 {
		msg := "error: no session providers available. " +
			"Install CodeBuddy, Kimi, or OpenCode and ensure their session storage exists."
		if dis := provider.DisabledNames(); len(dis) > 0 {
			msg = fmt.Sprintf(
				"error: no enabled session providers available (disabled: %s). "+
					"Run `reup provider on <name>` to re-enable one.",
				strings.Join(dis, ", "))
		}
		fmt.Fprintln(os.Stderr, msg)
		return 2
	}

	// --no-group is a display hint only; harmless outside list mode, so it is
	// ignored rather than rejected.
	if command != "list" && (*jsonOut || *full >= 0) {
		fmt.Fprintln(os.Stderr,
			"error: --json and --full are only valid with the 'list' subcommand")
		return 2
	}

	if command == "list" {
		return runList(filters, *jsonOut, *full, *noGroup)
	}
	return runPicker(filters)
}

func runList(filters session.Filters, jsonOut bool, full int, noGroup bool) int {
	sessions, err := provider.MergedList(filters)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	switch {
	case jsonOut:
		fmt.Println(render.JSON(sessions))
	case full >= 0:
		n := full
		if n > len(sessions) {
			n = len(sessions)
		}
		for i := 0; i < n; i++ {
			fmt.Println(render.FullBox(&sessions[i]))
			fmt.Println()
		}
	case noGroup:
		fmt.Println(render.Index(sessions))
	default:
		fmt.Println(render.IndexGrouped(sessions))
	}
	return 0
}

func runPicker(filters session.Filters) int {
	chosen, empty, err := tui.Pick(filters)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	if chosen == nil {
		if empty {
			fmt.Fprintln(os.Stderr, "No sessions found. Try without --project/--date, or check `reup provider list`.")
		}
		return 0
	}
	return execResume(chosen)
}

// stdinIsTerminal reports whether stdin is an interactive terminal. Uses the
// charmbracelet/x/term package already in the dependency graph (bubbletea's
// own terminal layer) — no new module.
func stdinIsTerminal() bool {
	return term.IsTerminal(os.Stdin.Fd())
}

// providerNames lists registered provider names in registry order.
func providerNames() []string {
	var out []string
	for _, p := range provider.All() {
		out = append(out, p.Name())
	}
	return out
}

// exitCannotResume is returned when a session's working directory is gone, so
// the agent CLI would fail on its own with a "created under a different
// directory" error that names neither the real cause nor the fix.
const exitCannotResume = 3

// reportMissingCwd explains the refusal and hands the user the one command
// that unblocks it, ready to paste.
func reportMissingCwd(s *session.Session, dir string) {
	fmt.Fprintf(os.Stderr,
		"error: cannot resume [%s] %s — its working directory has been deleted:\n",
		s.Source, s.SessionID)
	fmt.Fprintf(os.Stderr, "       %s\n", dir)
	fmt.Fprintf(os.Stderr,
		"       the agent CLI cannot start there, so reup did not run the resume command.\n")
	fmt.Fprintf(os.Stderr, "       to resume this session, recreate the directory first:\n")
	fmt.Fprintf(os.Stderr, "         mkdir -p %q\n", dir)
	fmt.Fprintf(os.Stderr, "       then run reup again and select this session.\n")
}

// execResume chdirs into the session's directory and runs the provider's
// resume command as a foreground child. When the agent exits, a `cd <dir>`
// hint is printed if the session directory differs from where reup was
// started — the parent shell never moved, and a child cannot move it.
//
// When the resolved directory does not exist at all, reup refuses instead
// of running: the agent CLI would only report that the session "was created
// under a different directory", which tells the user nothing about the real
// problem or how to fix it.
func execResume(s *session.Session) int {
	origWd, _ := os.Getwd()
	targetCwd, ok := cwd.Resolve(s)
	if !ok {
		reportMissingCwd(s, targetCwd)
		return exitCannotResume
	}
	if targetCwd != "" {
		_ = os.Chdir(targetCwd)
	}

	if len(s.ResumeArgv) == 0 {
		fmt.Fprintln(os.Stderr, "error: session has no resume command")
		return 2
	}
	wd, _ := os.Getwd()
	fmt.Fprintf(os.Stderr, "resuming [%s] %s from %s\n", s.Source, s.SessionID, wd)

	binPath, err := exec.LookPath(s.ResumeArgv[0])
	if err != nil {
		binName := s.ResumeArgv[0]
		installHint := map[string]string{
			"codebuddy": "https://www.codebuddy.ai/docs/cli/reference",
			"kimi":      "https://github.com/MoonshotAI/kimi-code",
			"opencode":  "https://opencode.ai/docs",
		}[binName]
		fmt.Fprintf(os.Stderr, "error: '%s' not found in PATH\n", binName)
		if installHint != "" {
			fmt.Fprintf(os.Stderr, "       install: %s\n", installHint)
		}
		return 127
	}
	code := execres.Run(binPath, s.ResumeArgv)
	if targetCwd != "" && targetCwd != origWd {
		fmt.Fprintf(os.Stderr, "cd %s\n", targetCwd)
	}
	return code
}
