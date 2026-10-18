// Package provider defines the Provider interface and the registry that
// merges sessions across providers. Adding a provider means
// one new package plus one entry in the registry slice.
package provider

import (
	"fmt"
	"sort"

	"github.com/QuietSugar/reup/internal/config"
	"github.com/QuietSugar/reup/internal/session"
)

// Provider is the contract each AI-CLI session source implements.
// Implementations must read their env overrides at call time (not init)
// so test harnesses can inject fixture roots per invocation.
type Provider interface {
	Name() string
	Badge() string
	BadgeANSI() string
	IsAvailable() bool
	ListSessions(f session.Filters) ([]session.Session, error)
	LoadDetail(id string) (*session.Session, error)
}

// SessionMutator is an optional capability interface for providers whose
// agent can hide a session through its own official mechanism (kimi's
// restorable archive). Implementations must never edit agent storage files
// directly. Providers without such a mechanism fall back to reup's own
// hidden list — the picker handles that itself, no provider code involved.
type SessionMutator interface {
	// ServerAvailable reports whether the agent's API/CLI endpoint is
	// reachable right now (probe only: file reads, dial, LookPath).
	ServerAvailable() bool
	// CheckSession reports whether the official endpoint can manage this
	// specific session. Some agents scope their API to sessions known to
	// the running instance — a non-nil error is surfaced in the confirm
	// modal and disables it, so a silent API no-op can never masquerade as
	// success.
	CheckSession(id string) error
	// ArchiveSession hides the session via the official API (restorable).
	ArchiveSession(id string) error
}

// MutatorFor returns p's SessionMutator capability, or nil when the provider
// offers no official mutation mechanism. Callers must pass the concrete
// provider instance (not a registry lookup) so test stubs work.
func MutatorFor(p Provider) SessionMutator {
	m, _ := p.(SessionMutator)
	return m
}

// ServerStarter is an optional companion capability for a SessionMutator
// whose agent server can be brought up without user interaction. When
// ServerAvailable is false and the provider implements this, the picker
// offers "start & continue": StartServer launches the server detached (no
// terminal, no browser), waits until it is reachable, and leaves it running.
type ServerStarter interface {
	// StartServer brings the agent's local server up and blocks until it is
	// reachable (or returns an error). It must be idempotent: when the
	// server is already up it returns nil immediately.
	StartServer() error
	// StopServer gracefully shuts down the server that StartServer spawned
	// (official shutdown path, never signals). When StartServer was not the
	// one that brought the server up — or it already exited — this is a
	// no-op. Errors are advisory: the mutation itself has already happened.
	StopServer() error
}

// StarterFor returns p's ServerStarter capability, or nil.
func StarterFor(p Provider) ServerStarter {
	s, _ := p.(ServerStarter)
	return s
}

var registry []Provider

// disabled holds provider names turned off by the user (persisted in the
// config file, applied via SetDisabled). A disabled provider is skipped by
// every ambient path — Active() never yields it, so nothing walks or parses
// its storage. Explicit per-invocation requests (--source NAME) bypass the
// flag on purpose: the config supplies defaults, the command line overrides.
var disabled = map[string]bool{}

// SetDisabled replaces the disabled set from the config file. Unknown names
// are kept: the config outlives registry changes.
func SetDisabled(names []string) {
	disabled = make(map[string]bool, len(names))
	for _, n := range names {
		disabled[n] = true
	}
}

// IsEnabled reports whether name is not in the disabled set.
func IsEnabled(name string) bool { return !disabled[name] }

// DisabledNames returns the disabled provider names, sorted.
func DisabledNames() []string {
	out := make([]string, 0, len(disabled))
	for n := range disabled {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Register appends a provider. Called from cli wiring to avoid import cycles
// and keep this package dependency-free for tests.
func Register(p Provider) {
	registry = append(registry, p)
}

// All returns every registered provider, disabled or not.
func All() []Provider {
	out := make([]Provider, len(registry))
	copy(out, registry)
	return out
}

// Active returns providers whose data sources are present on this machine
// and not disabled by the user.
func Active() []Provider {
	var out []Provider
	for _, p := range registry {
		if !IsEnabled(p.Name()) {
			continue
		}
		if p.IsAvailable() {
			out = append(out, p)
		}
	}
	return out
}

// Get returns the provider with the given name, or nil.
func Get(name string) Provider {
	for _, p := range registry {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

// AvailableSourceNames lists names of active providers.
func AvailableSourceNames() []string {
	var out []string
	for _, p := range Active() {
		out = append(out, p.Name())
	}
	return out
}

// SortSessions orders by (LastTS, Source) descending — string comparison,
// matching the Python registry's sort for output parity — with Path as a
// final tiebreak for deterministic ordering on identical timestamps.
// Equal elements must compare false in both directions (strict weak
// ordering), or sort.SliceStable's behavior is undefined.
func SortSessions(out []session.Session, ascending bool) {
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.LastTS != b.LastTS {
			if ascending {
				return a.LastTS < b.LastTS
			}
			return a.LastTS > b.LastTS
		}
		if a.Source != b.Source {
			if ascending {
				return a.Source < b.Source
			}
			return a.Source > b.Source
		}
		if a.Path != b.Path {
			if ascending {
				return a.Path > b.Path
			}
			return a.Path < b.Path
		}
		return false
	})
}

// HiddenKeys loads the user's hidden-session set ("source\x00sessionID").
// A missing or malformed config yields an empty set: hiding must never break
// listing.
func HiddenKeys() map[string]bool {
	out := map[string]bool{}
	cfg, err := config.Load()
	if err != nil {
		return out
	}
	for _, k := range cfg.Hidden {
		out[k] = true
	}
	return out
}

// FilterHidden drops sessions whose key is in the hidden set. Kept next to
// the registry so every listing path (CLI merge, picker scan) filters
// identically.
func FilterHidden(in []session.Session, hidden map[string]bool) []session.Session {
	if len(hidden) == 0 {
		return in
	}
	out := in[:0]
	for _, s := range in {
		if !hidden[s.Source+"\x00"+s.SessionID] {
			out = append(out, s)
		}
	}
	return out
}

// MergedList collects sessions from active providers (or the single provider
// named in filters), sorted last-activity-descending, limit applied.
func MergedList(f session.Filters) ([]session.Session, error) {
	var out []session.Session
	if f.Source != "" {
		p := Get(f.Source)
		if p == nil {
			return nil, fmt.Errorf("unknown provider: %s", f.Source)
		}
		if !p.IsAvailable() {
			return nil, fmt.Errorf(
				"%s provider not available (binary or session directory missing)", f.Source)
		}
		ss, err := p.ListSessions(f)
		if err != nil {
			return nil, err
		}
		out = append(out, ss...)
	} else {
		for _, p := range Active() {
			ss, err := p.ListSessions(f)
			if err != nil {
				return nil, err
			}
			out = append(out, ss...)
		}
	}
	out = FilterHidden(out, HiddenKeys())
	SortSessions(out, false)
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}
