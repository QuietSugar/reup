package provider

import (
	"sort"
	"testing"

	"github.com/QuietSugar/reup/internal/session"
)

func mk(last, source, path string) session.Session {
	return session.Session{LastTS: last, Source: source, Path: path}
}

func ids(ss []session.Session) []string {
	var out []string
	for _, s := range ss {
		out = append(out, s.Path)
	}
	return out
}

func TestSortSessionsDescending(t *testing.T) {
	ss := []session.Session{
		mk("2026-04-15T01:00:00Z", "kimi-code", "a"),
		mk("2026-04-15T07:00:00Z", "opencode", "b"),
		mk("2026-04-15T03:00:00Z", "kimi-code", "c"),
	}
	SortSessions(ss, false)
	got := ids(ss)
	want := []string{"b", "c", "a"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("descending order = %v, want %v", got, want)
		}
	}
}

func TestSortSessionsAscendingIsReverse(t *testing.T) {
	ss := []session.Session{
		mk("2026-04-15T01:00:00Z", "kimi-code", "a"),
		mk("2026-04-15T07:00:00Z", "opencode", "b"),
		mk("2026-04-15T03:00:00Z", "kimi-code", "c"),
	}
	SortSessions(ss, true)
	got := ids(ss)
	want := []string{"a", "c", "b"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ascending order = %v, want %v", got, want)
		}
	}
}

func TestSortSessionsStrictWeakOrderingOnTies(t *testing.T) {
	// Fully equal elements must not panic and must keep a stable, valid
	// order in both directions (the comparator must return false for
	// equal pairs — regression guard for the ascending !less bug).
	var ss []session.Session
	for i := 0; i < 50; i++ {
		ss = append(ss, mk("2026-04-15T01:00:00Z", "kimi-code", "same"))
	}
	SortSessions(ss, true)
	SortSessions(ss, false)
	if len(ss) != 50 {
		t.Fatal("sort lost elements")
	}
	// Distinct paths on equal (ts, source): deterministic tiebreak.
	tie := []session.Session{
		mk("2026-04-15T01:00:00Z", "kimi-code", "z"),
		mk("2026-04-15T01:00:00Z", "kimi-code", "a"),
	}
	SortSessions(tie, false)
	if !sort.SliceIsSorted(tie, func(i, j int) bool { return tie[i].Path < tie[j].Path }) {
		t.Errorf("descending tiebreak should order by path asc, got %v", ids(tie))
	}
}

// stubProvider exists only to exercise registry filtering.
type stubProvider struct {
	name  string
	avail bool
}

func (s stubProvider) Name() string      { return s.name }
func (s stubProvider) Badge() string     { return "[" + s.name + "]" }
func (s stubProvider) BadgeANSI() string { return "" }
func (s stubProvider) IsAvailable() bool { return s.avail }
func (s stubProvider) ListSessions(f session.Filters) ([]session.Session, error) {
	return nil, nil
}
func (s stubProvider) LoadDetail(id string) (*session.Session, error) { return nil, nil }

// isolateRegistry snapshots the package-global registry and disabled set and
// restores both after the test (registry state leaks across tests otherwise).
func isolateRegistry(t *testing.T, ps ...stubProvider) {
	t.Helper()
	prevReg, prevDis := registry, disabled
	registry = nil
	disabled = map[string]bool{}
	for _, p := range ps {
		Register(p)
	}
	t.Cleanup(func() {
		registry, disabled = prevReg, prevDis
	})
}

func TestActiveFiltersDisabledProviders(t *testing.T) {
	isolateRegistry(t,
		stubProvider{"alpha", true},
		stubProvider{"beta", true},
		stubProvider{"gamma", false}, // installed in config but storage missing
	)

	got := names(Active())
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("Active with nothing disabled = %v", got)
	}

	SetDisabled([]string{"beta", "ghost"}) // ghost must be tolerated
	if IsEnabled("beta") || IsEnabled("ghost") || !IsEnabled("alpha") {
		t.Fatalf("IsEnabled wrong after SetDisabled")
	}
	if dn := DisabledNames(); len(dn) != 2 || dn[0] != "beta" || dn[1] != "ghost" {
		t.Fatalf("DisabledNames = %v", dn)
	}

	got = names(Active())
	if len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("Active after disable = %v, want [alpha]", got)
	}
	got = AvailableSourceNames()
	if len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("AvailableSourceNames after disable = %v", got)
	}
	// All() still reports everything — `provider list` needs the full set.
	if len(All()) != 3 {
		t.Fatalf("All() = %d providers, want 3", len(All()))
	}
	// Get() ignores the disabled set: explicit --source lookups rely on it.
	if Get("beta") == nil {
		t.Fatal("Get(beta) must still resolve for explicit --source")
	}
}

func names(ps []Provider) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Name())
	}
	return out
}
