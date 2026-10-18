// Package cwd answers one question for every consumer: where would this
// session's resume command run, and can it run at all?
//
// codebuddy --resume <uuid> derives the project dir from the CURRENT cwd by
// encoding it (/, space, ~ all become -) and looking under
// ~/.codebuddy/projects/<encoded>/. The cwd stored in the JSONL can be stale or
// mismatched against the file's actual location (observed with iCloud and
// Obsidian vault paths), making the resume fail. Defense: derive cwd from the
// session file's encoded parent dir, which is always correct because it is
// where codebuddy stored the file.
//
// The list renderer, the TUI, and the exec dispatcher all go through Resolve,
// so a row flagged as unresumable is exactly a row Enter will refuse — the
// alternative is a marker that cries wolf on sessions that resume fine.
package cwd

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/QuietSugar/reup/internal/session"
)

// maxWalkDepth guards against symlink loops; typical iCloud paths are ~8 deep.
const maxWalkDepth = 15

var cwdReplacer = strings.NewReplacer("/", "-", " ", "-", "~", "-")

// encodeCwd mimics CodeBuddy's cwd → project-dir encoding.
func encodeCwd(path string) string {
	return "-" + cwdReplacer.Replace(strings.TrimLeft(path, "/"))
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// Resolve returns the directory the resume command must run from and reports
// whether the session is resumable at all. ok is false when the recorded
// directory is known but no longer exists — the project was renamed, moved, or
// deleted, or the session data was carried to another machine — and nothing
// else can stand in for it.
//
// An empty dir with ok == true means "cwd unknown": run from wherever reup
// was started.
func Resolve(s *session.Session) (dir string, ok bool) {
	if s.Source == "codebuddy" {
		dir = ResolveExecCwd(s.Path, s.Cwd)
	}
	if dir == "" {
		dir = s.Cwd
	}
	if dir == "" {
		return "", true // cwd unknown — run from wherever reup was started
	}
	if !isDir(dir) {
		return dir, false
	}
	return dir, true
}

// Missing reports whether Resolve would refuse, i.e. whether the session's
// working directory is gone and cannot be recovered. Renderers use it to flag
// such sessions before the user commits to them.
func Missing(s *session.Session) bool {
	_, ok := Resolve(s)
	return !ok
}

// ResolveExecCwd finds a filesystem dir whose encoding matches the session's
// encoded parent dir. Returns "" if no match.
//
// Fast path: storedCwd already encodes to the target (99%+ of sessions).
// Slow path: walk from / matching segment encodings, permission errors
// swallowed, depth limited.
func ResolveExecCwd(sessionPath, storedCwd string) string {
	return resolveExecCwdFrom("/", sessionPath, storedCwd)
}

// resolveExecCwdFrom is the testable form: walks from root instead of /.
func resolveExecCwdFrom(root, sessionPath, storedCwd string) string {
	encoded := filepath.Base(filepath.Dir(sessionPath))
	if !strings.HasPrefix(encoded, "-") {
		return ""
	}
	if storedCwd != "" && isDir(storedCwd) && encodeCwd(storedCwd) == encoded {
		return storedCwd
	}
	target := strings.TrimLeft(encoded, "-")
	return walkEncoded(root, target, 0)
}

func walkEncoded(current, remaining string, depth int) string {
	if depth > maxWalkDepth {
		return ""
	}
	if remaining == "" {
		if isDir(current) {
			return current
		}
		return ""
	}
	entries, err := os.ReadDir(current)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		enc := cwdReplacer.Replace(e.Name())
		if remaining == enc {
			full := filepath.Join(current, e.Name())
			if isDir(full) {
				return full
			}
			return "" // exact-name match that isn't a dir ends this level
		}
		if strings.HasPrefix(remaining, enc+"-") {
			full := filepath.Join(current, e.Name())
			if isDir(full) {
				if hit := walkEncoded(full, remaining[len(enc)+1:], depth+1); hit != "" {
					return hit
				}
			}
		}
	}
	return ""
}
