// Package opencode parses OpenCode (sst/opencode) 1.1+ / v2 session
// storage: SQLite at $XDG_DATA_HOME/opencode/opencode.db. Sessions live in
// session_v2 (opencode v2) or session (1.x installs that predate the v2
// schema); both carry id/title/directory/parent_id, workspace_id/project_id
// (native workspace identity) and time_created/time_updated/time_archived
// (epoch ms). Prompts come from the session_message rows ({type:"user",text})
// and the message+part pair that v2 keeps for migrated 1.x dbs — both
// projections are unioned and deduped on (timestamp, text). Subagent/child
// sessions (parent_id) and archived sessions are skipped, matching
// opencode's own session list. Resume: `opencode --session <id>`.
package opencode

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/QuietSugar/reup/internal/brand"
	"github.com/QuietSugar/reup/internal/session"
	"github.com/QuietSugar/reup/internal/sqliteread"
	"github.com/QuietSugar/reup/internal/textutil"
)

var (
	envData = brand.Env("OPENCODE_DATA")
	envBin  = brand.Env("OPENCODE_BIN")
)

func dataRoot() string {
	if v := os.Getenv(envData); v != "" {
		return v
	}
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "opencode")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "opencode")
}

// dbPath is the session database: $XDG_DATA_HOME/opencode/opencode.db,
// empty when it does not exist.
func dbPath() string {
	path := filepath.Join(dataRoot(), "opencode.db")
	if st, err := os.Stat(path); err != nil || !st.Mode().IsRegular() {
		return ""
	}
	return path
}

func opencodeBin() string {
	if bin := os.Getenv(envBin); bin != "" {
		return bin
	}
	return "opencode"
}

func binAvailable() bool {
	_, err := exec.LookPath(opencodeBin())
	return err == nil
}

// Provider implements provider.Provider for OpenCode.
type Provider struct{}

func New() *Provider { return &Provider{} }

func (p *Provider) Name() string      { return "opencode" }
func (p *Provider) Badge() string     { return "oc" }
func (p *Provider) BadgeANSI() string { return "\x1b[34m" } // blue

func (p *Provider) IsAvailable() bool {
	return binAvailable() && dbPath() != ""
}

// msToTS converts epoch milliseconds to RFC3339 UTC (second precision, the
// granularity the shared renderers work at).
func msToTS(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.Unix(ms/1000, 0).UTC().Format(time.RFC3339)
}

// promptAgg accumulates first/last user prompts and assistant-turn counts for
// one session. Timestamps are epoch ms — comparable as ints.
type promptAgg struct {
	firstTS, lastTS int64
	first, last     string
	asst            int
	seenP           map[string]bool // dedupe keys "ts\x00hash" — see promptKey
	seenA           map[string]bool // assistant turn identity (parent user ID, timestamp fallback)
}

// promptKey identifies one (timestamp, text) prompt for dedupe. The text is
// hashed so the dedupe set stays small even when individual prompts are
// huge; a hash collision only skips one dedupe.
func promptKey(ts int64, text string) string {
	h := fnv.New64a()
	h.Write([]byte(text))
	return strconv.FormatInt(ts, 10) + "\x00" + strconv.FormatUint(h.Sum64(), 16)
}

func (a *promptAgg) addPrompt(ts int64, text string) {
	if text == "" {
		return
	}
	key := promptKey(ts, text)
	if a.seenP == nil {
		a.seenP = map[string]bool{}
	}
	if a.seenP[key] {
		return
	}
	a.seenP[key] = true
	if a.first == "" || (a.firstTS != 0 && ts < a.firstTS) || a.firstTS == 0 {
		if a.first == "" || ts < a.firstTS {
			a.firstTS, a.first = ts, text
		}
	}
	if a.last == "" || ts >= a.lastTS {
		a.lastTS, a.last = ts, text
	}
}

func (a *promptAgg) addAssistantTurn(parentID string, ts int64) {
	if a.seenA == nil {
		a.seenA = map[string]bool{}
	}
	key := "parent:" + parentID
	if parentID == "" {
		key = "time:" + strconv.FormatInt(ts, 10)
	}
	if a.seenA[key] {
		return
	}
	a.seenA[key] = true
	a.asst++
}

// indexSQLitePrompts walks the message-bearing tables of an opencode db and
// merges both projections at the row level. Baseline 1.18.x keeps two parallel
// projections of the same conversation: the durable pipeline writes
// session_message, while the v1-compat layer keeps writing message+part — and
// messages that predate an upgrade live only in the legacy pair (there is no
// backfill migration). Prompts are therefore unioned across the two families
// and deduped on (timestamp, text). Assistant messages are grouped by parent
// user-message ID so tool-call continuations count as one turn; timestamps are
// the fallback for older rows without parentID. Tolerates missing tables:
// pre-1.1 JSON-era installs have no db.
func indexSQLitePrompts(db *sqliteread.DB) map[string]*promptAgg {
	out := map[string]*promptAgg{}
	agg := func(id string) *promptAgg {
		a, ok := out[id]
		if !ok {
			a = &promptAgg{}
			out[id] = a
		}
		return a
	}

	// Durable projection (1.1+ … 1.18.x, v2): envelope column `type` plus a
	// JSON payload in `data`.
	if tbl, err := db.Table("session_message"); err == nil {
		_ = tbl.Scan(func(r sqliteread.Row) error {
			ses, _ := r.Str("session_id")
			if ses == "" {
				return nil
			}
			var payload struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				ParentID string `json:"parentID"`
			}
			if raw, ok := r.Str("data"); ok && raw != "" {
				_ = json.Unmarshal([]byte(raw), &payload)
			} else {
				return nil
			}
			ts, _ := r.Int("time_created")
			switch payload.Type {
			case "user":
				agg(ses).addPrompt(ts, strings.TrimSpace(payload.Text))
			case "assistant":
				agg(ses).addAssistantTurn(payload.ParentID, ts)
			}
			return nil
		})
	}

	// v1-compat projection: message (role envelope) + part (typed blocks).
	// Legacy prompt timestamps come from message.time_created — part rows do
	// not carry usable prompt times of their own.
	type legacyMsg struct {
		ses  string
		role string
		ts   int64
	}
	msgs := map[string]legacyMsg{}
	if tbl, err := db.Table("message"); err == nil {
		_ = tbl.Scan(func(r sqliteread.Row) error {
			id, _ := r.Str("id")
			ses, _ := r.Str("session_id")
			if id == "" || ses == "" {
				return nil
			}
			var payload struct {
				Role     string `json:"role"`
				ParentID string `json:"parentID"`
			}
			if raw, ok := r.Str("data"); ok && raw != "" {
				_ = json.Unmarshal([]byte(raw), &payload)
			}
			if payload.Role != "user" && payload.Role != "assistant" {
				return nil
			}
			ts, _ := r.Int("time_created")
			msgs[id] = legacyMsg{ses: ses, role: payload.Role, ts: ts}
			if payload.Role == "assistant" {
				agg(ses).addAssistantTurn(payload.ParentID, ts)
			}
			return nil
		})
	}
	if len(msgs) > 0 {
		if tbl, err := db.Table("part"); err == nil {
			_ = tbl.Scan(func(r sqliteread.Row) error {
				mid, _ := r.Str("message_id")
				m, ok := msgs[mid]
				if !ok || m.role != "user" {
					return nil
				}
				var payload struct {
					Type      string `json:"type"`
					Text      string `json:"text"`
					Synthetic bool   `json:"synthetic"`
				}
				if raw, ok := r.Str("data"); ok && raw != "" {
					_ = json.Unmarshal([]byte(raw), &payload)
				}
				if payload.Type == "text" && !payload.Synthetic {
					agg(m.ses).addPrompt(m.ts, strings.TrimSpace(payload.Text))
				}
				return nil
			})
		}
	}
	return out
}

func projectLabel(dir string) string {
	if dir == "" {
		return "(unknown)"
	}
	if base := filepath.Base(strings.TrimRight(dir, "/")); base != "" && base != "/" && base != "." {
		return base
	}
	return "(unknown)"
}

// sessionTable opens the authoritative session table. opencode v2 moved
// sessions to session_v2 and leaves `session` behind as a v1-compat copy that
// no longer receives new rows (and still holds sessions the user deleted
// under v2). Older 1.x databases only have `session`.
func sessionTable(db *sqliteread.DB) (*sqliteread.Table, error) {
	tbl, err := db.Table("session_v2")
	if err == nil {
		return tbl, nil
	}
	if !errors.Is(err, sqliteread.ErrNotFound) {
		return nil, err
	}
	return db.Table("session")
}

// readSQLite returns the visible sessions from the database: root sessions
// that are neither archived (time_archived set) nor a child/subagent session
// (parent_id set).
func (p *Provider) readSQLite(path string) ([]session.Session, error) {
	db, err := sqliteread.Open(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	prompts := indexSQLitePrompts(db)
	tbl, err := sessionTable(db)
	if err != nil {
		return nil, err
	}
	var out []session.Session
	err = tbl.Scan(func(r sqliteread.Row) error {
		id, _ := r.Str("id")
		if id == "" {
			return nil
		}
		if parent, _ := r.Str("parent_id"); parent != "" {
			return nil // child/subagent session
		}
		if v := r.Get("time_archived"); v != nil {
			return nil // archived
		}
		title, _ := r.Str("title")
		dir, _ := r.Str("directory")
		// Workspace identity: workspace_id wins; project_id is the fallback,
		// except the literal "global" (opencode's "not a real project" sentinel).
		workspaceID, _ := r.Str("workspace_id")
		if workspaceID == "" {
			if projID, _ := r.Str("project_id"); projID != "" && projID != "global" {
				workspaceID = projID
			}
		}
		created, _ := r.Int("time_created")
		updated, _ := r.Int("time_updated")
		pa := prompts[id]
		if pa == nil {
			pa = &promptAgg{} // session with no user/assistant messages yet
		}

		out = append(out, session.Session{
			Source:       "opencode",
			SessionID:    id,
			Path:         fmt.Sprintf("%s#session/%s", path, id),
			ProjectLabel: projectLabel(dir),
			Cwd:          dir,
			WorkspaceID:  workspaceID,
			FirstTS:      msToTS(created),
			LastTS:       msToTS(updated),
			Title:        strings.TrimSpace(title),
			FirstPrompt:  pa.first,
			LastPrompt:   pa.last,
			AsstCount:    pa.asst,
			ResumeArgv:   []string{"opencode", "--session", id},
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// listRaw returns every visible session from the database. The database is
// the only source: opencode empties storage/session/ when it migrates, so
// nothing else on disk can resurrect a deleted or archived session.
func (p *Provider) listRaw() ([]session.Session, error) {
	path := dbPath()
	if path == "" {
		return nil, nil // no database: no sessions
	}
	ss, err := p.readSQLite(path)
	if err != nil {
		return nil, fmt.Errorf("opencode db %s: %w", path, err)
	}
	return ss, nil
}

func touchesDate(s *session.Session, day time.Time) bool {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	end := start.Add(23*time.Hour + 59*time.Minute + 59*time.Second)
	fs_, okF := textutil.ParseISO(s.FirstTS)
	ls, okL := textutil.ParseISO(s.LastTS)
	if !okF && !okL {
		return false
	}
	if !okF {
		fs_ = ls
	}
	if !okL {
		ls = fs_
	}
	return !(ls.Before(start) || fs_.After(end))
}

// cutoffForFilters: local midnight minus N days. Nil when there is no time
// window at all — --all, --date, or the CLI default of "no limit" (Days == 0)
// — so every parsed session is kept.
func cutoffForFilters(f session.Filters) *time.Time {
	if f.AllTime || f.Date != "" || f.Days == 0 {
		return nil
	}
	days := f.Days
	if days < 0 {
		days = 3
	}
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	cutoff := midnight.AddDate(0, 0, -days)
	return &cutoff
}

func (p *Provider) ListSessions(f session.Filters) ([]session.Session, error) {
	cutoff := cutoffForFilters(f)
	var day *time.Time
	if f.Date != "" {
		if d, err := time.ParseInLocation("2006-01-02", f.Date, time.UTC); err == nil {
			day = &d
		}
	}

	raw, err := p.listRaw()
	if err != nil {
		return nil, err
	}
	var out []session.Session
	for i := range raw {
		s := &raw[i]
		if f.Project != "" &&
			!strings.Contains(strings.ToLower(s.ProjectLabel), strings.ToLower(f.Project)) {
			continue
		}
		if day != nil {
			if !touchesDate(s, *day) {
				continue
			}
		} else if cutoff != nil {
			ls, ok := textutil.ParseISO(s.LastTS)
			if !ok || ls.Before(*cutoff) {
				continue
			}
		}
		out = append(out, *s)
	}
	return out, nil
}

func (p *Provider) LoadDetail(id string) (*session.Session, error) {
	raw, err := p.listRaw()
	if err != nil {
		return nil, err
	}
	for i := range raw {
		if raw[i].SessionID == id {
			return &raw[i], nil
		}
	}
	return nil, nil
}
