// Package session defines the provider-agnostic session schema shared by
// providers, renderers, the TUI, and the exec dispatcher.
package session

// Prompt is one user prompt with its timestamp (empty string when unknown).
type Prompt struct {
	TS   string
	Text string
}

// Session is the common schema. String fields use "" for "absent" (the
// Python implementation used None); render/json restores explicit nulls.
type Session struct {
	Source       string
	SessionID    string
	Path         string
	ProjectLabel string
	Cwd          string
	// WorkspaceID is the provider-native workspace/project identifier when the
	// provider exposes one (kimi: the on-disk `wd_<slug>_<hash>` bucket key;
	// opencode: session.workspace_id, else project_id). "" when the provider has
	// none.
	WorkspaceID string
	// WorkspaceRoot is the directory the provider records as the workspace root
	// (kimi workspaces.json `root`). Optional enrichment; "" when unknown.
	WorkspaceRoot string
	FirstTS       string
	LastTS        string
	Title         string
	Subtitle      string
	FirstPrompt   string
	LastPrompt    string
	Prompts       []Prompt
	// AsstCount is a provider-defined estimate of assistant-side activity.
	// Providers may use different counting rules; the value can be inaccurate
	// and is intended only as a rough indication of conversation size, not an
	// exact or cross-provider comparable turn count.
	AsstCount  int
	ResumeArgv []string
}

// Filters narrows what a provider lists. The zero value means "no
// constraints": Days == 0 is the CLI default and imposes no time limit, so
// every session found on disk is listed.
type Filters struct {
	Days    int    // 0 = no time limit (default); N = last N days only
	Date    string // YYYY-MM-DD — single-day overlap mode, overrides Days
	AllTime bool   // explicit "ignore the time window" (same as Days == 0)
	Project string // case-insensitive substring match on the project label
	Limit   int    // >0 caps the merged result after sorting
	Source  string // restrict to one provider name
}
