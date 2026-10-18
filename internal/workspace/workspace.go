// Package workspace derives cross-provider workspace groups from sessions.
//
// A workspace is identified by its working directory so that the same directory
// opened in different providers lands in one group; a provider-native workspace
// id (kimi's `wd_<slug>_<hash>` bucket, opencode's workspace/project id) is only
// a fallback when no directory is known, and never merges across providers.
package workspace

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/QuietSugar/reup/internal/session"
	"github.com/QuietSugar/reup/internal/textutil"
)

// Group is a set of sessions that share one workspace. Sessions keep the
// caller's order (callers sort first); groups are ordered by their most recent
// session, newest first, so the workspace of the newest session always sorts to
// the top.
type Group struct {
	Key         string            // stable identity, see Key
	Dir         string            // canonical directory, "" when unknown
	WorkspaceID string            // native id of the first member that has one
	Sessions    []session.Session // caller's order, unchanged
	Providers   []string          // sorted, unique source names
}

// canonical cleans a directory into a grouping-safe identity. "" and the bare
// filesystem root are treated as "no directory": grouping every rootless
// session under "/" would be misleading.
func canonical(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	c := filepath.Clean(dir)
	if c == "." || c == string(filepath.Separator) {
		return ""
	}
	return c
}

// Key returns the identity used to group s. The working directory wins (that is
// what merges sessions across providers); the provider-recorded root and the
// native id are fallbacks. Unknown identities are namespaced by source so
// unrelated directory-less sessions are not lumped together.
func Key(s *session.Session) string {
	dir := canonical(s.Cwd)
	if dir == "" {
		dir = canonical(s.WorkspaceRoot)
	}
	if dir != "" {
		return "dir:" + dir
	}
	if s.WorkspaceID != "" {
		return "ws:" + s.Source + ":" + s.WorkspaceID
	}
	return "unknown:" + s.Source
}

// Label is the human-facing name for a group: the directory basename, else the
// native workspace id, else "(no workspace)".
func (g *Group) Label() string {
	if g.Dir != "" {
		if base := filepath.Base(g.Dir); base != "" && base != "." && base != string(filepath.Separator) {
			return base
		}
		return g.Dir
	}
	if g.WorkspaceID != "" {
		return g.WorkspaceID
	}
	return "(no workspace)"
}

// GroupBy partitions sessions by Key. A group's order is its newest session's
// timestamp, newest first — independent of the caller's sort direction — so the
// workspace containing the most recent session is always at the top. Sessions
// within a group keep the caller's order.
func GroupBy(sessions []session.Session) []Group {
	var groups []Group
	index := map[string]int{}
	for i := range sessions {
		s := &sessions[i]
		k := Key(s)
		gi, ok := index[k]
		if !ok {
			gi = len(groups)
			index[k] = gi
			groups = append(groups, Group{Key: k, Dir: groupDir(s)})
		}
		g := &groups[gi]
		g.Sessions = append(g.Sessions, *s)
		if g.WorkspaceID == "" && s.WorkspaceID != "" {
			g.WorkspaceID = s.WorkspaceID
		}
	}
	for i := range groups {
		groups[i].Providers = providersOf(groups[i].Sessions)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		ti, oki := groups[i].latest()
		tj, okj := groups[j].latest()
		switch {
		case oki && okj:
			if !ti.Equal(tj) {
				return ti.After(tj)
			}
		case oki != okj:
			return oki // a dated group outranks an undated one
		}
		// Deterministic tiebreak: known directories first, then by path/key.
		di, dj := groups[i].Dir, groups[j].Dir
		if (di == "") != (dj == "") {
			return dj == ""
		}
		if di != dj {
			return di < dj
		}
		return groups[i].Key < groups[j].Key
	})
	return groups
}

// latest returns the most recent session timestamp in the group, preferring
// LastTS and falling back to FirstTS. ok is false when no timestamp parses.
func (g *Group) latest() (time.Time, bool) {
	var best time.Time
	ok := false
	for i := range g.Sessions {
		ts := g.Sessions[i].LastTS
		if ts == "" {
			ts = g.Sessions[i].FirstTS
		}
		t, valid := textutil.ParseISO(ts)
		if !valid {
			continue
		}
		if !ok || t.After(best) {
			best, ok = t, true
		}
	}
	return best, ok
}

// groupDir returns the directory that defines the group, matching Key's
// precedence.
func groupDir(s *session.Session) string {
	if dir := canonical(s.Cwd); dir != "" {
		return dir
	}
	return canonical(s.WorkspaceRoot)
}

func providersOf(sessions []session.Session) []string {
	seen := map[string]bool{}
	var out []string
	for i := range sessions {
		src := sessions[i].Source
		if src == "" || seen[src] {
			continue
		}
		seen[src] = true
		out = append(out, src)
	}
	sort.Strings(out)
	return out
}
