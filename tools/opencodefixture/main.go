// Command opencodefixture regenerates the committed test fixture
// tests/fixtures/opencode-home/opencode.db.
//
// Schema mirrors opencode 2.0.x: session_v2 drives session metadata, `session`
// is the v1-compat copy left behind by the migration (and still holds sessions
// deleted under v2), and conversations are dual-projected into session_message
// (durable pipeline) plus the legacy message+part pair (v1-compat layer). The
// fixture exercises: v2-table preference over the legacy copy, dual-write
// dedupe, pre-upgrade sessions that exist only in the legacy pair, archived/
// child skipping, an oversized overflow-page blob, and workspace/project
// identity (incl. the "global" sentinel).
//
// Usage: go run ./tools/opencodefixture <db-path>
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

// Fixed epoch-ms anchors (all 2026-04-15 UTC).
const (
	aaaaCreated  = 1776229200000 // 05:00:00
	aaaaP1       = 1776229200000
	aaaaA1       = 1776229205000 // 05:00:05
	aaaaP2       = 1776229300000 // 05:01:40
	aaaaA2       = 1776229400000 // 05:03:20
	aaaaUpdated  = 1776229500000 // 05:05:00
	bbbbCreated  = 1776228000000 // 04:40:00
	bbbbUpdated  = 1776229000000 // 04:56:40
	bbbbArchived = 1776230000000 // 05:13:20
	ccccCreated  = 1776229600000 // 05:06:40
	ccccUpdated  = 1776229700000 // 05:08:20
	ddddCreated  = 1776234000000 // 06:20:00
	ddddP1       = 1776234000000
	ddddA1       = 1776234050000 // 06:20:50
	ddddUpdated  = 1776234100000 // 06:21:40
	eeeeCreated  = 1776232800000 // 06:00:00
	eeeeP1       = 1776232800000
	eeeeA1       = 1776232900000 // 06:01:40
	eeeeP2       = 1776233000000 // 06:03:20
	eeeeUpdated  = 1776233100000 // 06:05:00
	ffffCreated  = 1776231000000 // 05:30:00
	ffffP1       = 1776231000000
	ffffA1       = 1776231050000 // 05:30:50
	ffffUpdated  = 1776231300000 // 05:35:00
	decoyCreated = 1776236400000 // 07:00:00
	decoyUpdated = 1776236500000 // 07:01:40
)

var ses = map[string]string{
	"aaaa": "ses_aaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	"bbbb": "ses_bbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	"cccc": "ses_cccccccccccccccccccccccccccc",
	"dddd": "ses_dddddddddddddddddddddddddddd",
	"eeee": "ses_eeeeeeeeeeeeeeeeeeeeeeeeeeee",
	"ffff": "ses_ffffffffffffffffffffffffffff",
	"deco": "ses_9999999999999999999999999999",
}

const schema = `
CREATE TABLE session (
  id TEXT PRIMARY KEY, project_id TEXT NOT NULL, workspace_id TEXT,
  parent_id TEXT, slug TEXT NOT NULL, directory TEXT NOT NULL, path TEXT,
  title TEXT NOT NULL, version TEXT NOT NULL, share_url TEXT,
  summary_additions INTEGER, summary_deletions INTEGER, summary_files INTEGER,
  summary_diffs TEXT, metadata TEXT, cost REAL NOT NULL DEFAULT 0,
  tokens_input INTEGER NOT NULL DEFAULT 0, tokens_output INTEGER NOT NULL DEFAULT 0,
  tokens_reasoning INTEGER NOT NULL DEFAULT 0, tokens_cache_read INTEGER NOT NULL DEFAULT 0,
  tokens_cache_write INTEGER NOT NULL DEFAULT 0, revert TEXT, permission TEXT,
  agent TEXT, model TEXT, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL,
  time_compacting INTEGER, time_archived INTEGER
);
CREATE TABLE session_v2 (
  id TEXT PRIMARY KEY, project_id TEXT NOT NULL, workspace_id TEXT,
  parent_id TEXT, fork_session_id TEXT, fork_boundary TEXT,
  slug TEXT NOT NULL, directory TEXT NOT NULL, path TEXT,
  title TEXT, version TEXT NOT NULL, share_url TEXT,
  summary_additions INTEGER, summary_deletions INTEGER, summary_files INTEGER,
  summary_diffs TEXT, metadata TEXT, cost REAL NOT NULL DEFAULT 0,
  tokens_input INTEGER NOT NULL DEFAULT 0, tokens_output INTEGER NOT NULL DEFAULT 0,
  tokens_reasoning INTEGER NOT NULL DEFAULT 0, tokens_cache_read INTEGER NOT NULL DEFAULT 0,
  tokens_cache_write INTEGER NOT NULL DEFAULT 0, revert TEXT, permission TEXT,
  agent TEXT, model TEXT, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL,
  time_compacting INTEGER, time_archived INTEGER,
  time_suspended INTEGER, resume_attempts INTEGER NOT NULL DEFAULT 0,
  time_idle INTEGER, time_viewed INTEGER, idle_outcome TEXT
);
CREATE TABLE session_message (
  id TEXT PRIMARY KEY, session_id TEXT NOT NULL, type TEXT NOT NULL,
  seq INTEGER NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL,
  data TEXT NOT NULL, UNIQUE (session_id, seq)
);
CREATE TABLE message (
  id TEXT PRIMARY KEY, session_id TEXT NOT NULL,
  time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL
);
CREATE TABLE part (
  id TEXT PRIMARY KEY, message_id TEXT NOT NULL, session_id TEXT NOT NULL,
  time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL
);
`

var insertSession = "INSERT INTO session VALUES (" + strings.Repeat("?,", 28) + "?)"

var insertSessionV2 = "INSERT INTO session_v2 (id, project_id, workspace_id, parent_id, slug, " +
	"directory, path, title, version, share_url, summary_additions, summary_deletions, " +
	"summary_files, summary_diffs, metadata, cost, tokens_input, tokens_output, " +
	"tokens_reasoning, tokens_cache_read, tokens_cache_write, revert, permission, agent, " +
	"model, time_created, time_updated, time_compacting, time_archived) VALUES (" +
	strings.Repeat("?,", 28) + "?)"

func mustExec(db *sql.DB, q string, args ...any) {
	if _, err := db.Exec(q, args...); err != nil {
		panic(fmt.Sprintf("%s: %v", q[:min(40, len(q))], err))
	}
}

func sessionRow(sid, project, slug, directory, title string, created, updated int64,
	parent *string, archived *int64, diffs *string, workspace *string) []any {
	return []any{sid, project, workspace, parent, slug, directory, nil, title, "1.18.33",
		nil, nil, nil, nil, diffs, nil, 0.0,
		0, 0, 0, 0, 0,
		nil, nil, nil, nil, created, updated, nil, archived}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func main() {
	path := "opencode.db"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	_ = os.Remove(path)
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		panic(err)
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		panic(err)
	}
	if _, err := db.Exec(schema); err != nil {
		panic(err)
	}

	int64Ptr := func(v int64) *int64 { return &v }
	// --- sessions ----------------------------------------------------------
	// both writes a row to the authoritative session_v2 table and to the
	// v1-compat `session` copy, mirroring a post-migration database.
	both := func(row []any) {
		mustExec(db, insertSession, row...)
		mustExec(db, insertSessionV2, row...)
	}

	// ses_aaaa: dual-projection session (durable + legacy rows for the same
	// turns; reup must dedupe them).
	both(sessionRow(ses["aaaa"], "proj-oc", "one",
		"/tmp/reup/fixtures/oc-one", "OpenCode Fixture One",
		aaaaCreated, aaaaUpdated, nil, nil, nil, nil))

	// ses_bbbb: archived → skipped. Carries an oversized diffs blob that
	// spills into overflow pages.
	big := mustJSON(func() []map[string]any {
		out := make([]map[string]any, 400)
		for i := range out {
			out[i] = map[string]any{"file": fmt.Sprintf("big%d.txt", i), "additions": i}
		}
		return out
	}())
	both(sessionRow(ses["bbbb"], "proj-oc", "two",
		"/tmp/reup/fixtures/oc-two", "OpenCode Archived Fixture",
		bbbbCreated, bbbbUpdated, nil, int64Ptr(bbbbArchived), &big, nil))

	// ses_cccc: subagent child of ses_aaaa → skipped (parent_id set).
	parent := ses["aaaa"]
	both(sessionRow(ses["cccc"], "proj-oc", "one",
		"/tmp/reup/fixtures/oc-one", "OpenCode Child Fixture",
		ccccCreated, ccccUpdated, &parent, nil, nil, nil))

	// ses_dddd: durable-projection-only session (newest → integration test
	// target). project_id "global" is a sentinel, not a workspace id.
	both(sessionRow(ses["dddd"], "global", "three",
		"/tmp/reup/fixtures/oc-three", "OpenCode Fixture Three",
		ddddCreated, ddddUpdated, nil, nil, nil, nil))

	// ses_eeee: pre-upgrade session — exists ONLY in the legacy message+part
	// pair (no session_message rows, mirroring the missing backfill). Carries
	// a workspace_id, which takes precedence over project_id.
	wsLegacy := "ws-legacy-eeee"
	both(sessionRow(ses["eeee"], "proj-legacy", "two",
		"/tmp/reup/fixtures/oc-two", "OpenCode Legacy Session",
		eeeeCreated, eeeeUpdated, nil, nil, nil, &wsLegacy))

	// ses_ffff: created after the v2 migration — exists ONLY in session_v2,
	// the way every session opencode>=2.0 writes. reup must list it.
	mustExec(db, insertSessionV2, sessionRow(ses["ffff"], "proj-oc4", "four",
		"/tmp/reup/fixtures/oc-four", "OpenCode Fixture Four",
		ffffCreated, ffffUpdated, nil, nil, nil, nil)...)

	// ses_9999: deleted under v2 — the row survives only in the v1-compat
	// `session` table. opencode's own list does not show it, so reup must not
	// resurrect it.
	mustExec(db, insertSession, sessionRow(ses["deco"], "proj-decoy", "gone",
		"/tmp/reup/fixtures/oc-decoy", "OpenCode Deleted Decoy",
		decoyCreated, decoyUpdated, nil, nil, nil, nil)...)

	// --- session_message (durable projection) ------------------------------
	sm := []struct {
		id, sid, typ string
		seq          int
		ts           int64
		text         string
	}{
		{"msg_aa1", ses["aaaa"], "user", 1, aaaaP1, "opencode fixture one first prompt"},
		{"msg_aa2", ses["aaaa"], "assistant", 2, aaaaA1, ""},
		{"msg_aa3", ses["aaaa"], "user", 3, aaaaP2, "opencode fixture one second prompt"},
		{"msg_aa4", ses["aaaa"], "assistant", 4, aaaaA2, ""},
		{"msg_dd1", ses["dddd"], "user", 1, ddddP1, "opencode fixture three prompt"},
		{"msg_dd2", ses["dddd"], "assistant", 2, ddddA1, ""},
		{"msg_ff1", ses["ffff"], "user", 1, ffffP1, "opencode fixture four v2-only prompt"},
		{"msg_ff2", ses["ffff"], "assistant", 2, ffffA1, ""},
	}
	for _, m := range sm {
		mustExec(db, "INSERT INTO session_message VALUES (?,?,?,?,?,?,?)",
			m.id, m.sid, m.typ, m.seq, m.ts, m.ts+1000,
			mustJSON(map[string]string{"type": m.typ, "text": m.text}))
	}

	// --- legacy message+part ------------------------------------------------
	// Dual-write duplicates of ses_aaaa's turns: same timestamps, so reup
	// must collapse them. msg_j1 additionally carries an assistant text part
	// that must never surface as a prompt.
	type part struct {
		id, typ, text string
		synthetic     bool
	}
	legacy := []struct {
		id, sid, role string
		ts            int64
		parentID      string
		parts         []part
	}{
		{"msg_j3", ses["aaaa"], "user", aaaaP1, "", []part{{"part_j3", "text", "opencode fixture one first prompt", false}}},
		{"msg_j1", ses["aaaa"], "assistant", aaaaA1, "", []part{{"part_j1", "text", "legacy part prompt that must not leak", false}}},
		{"msg_j4", ses["aaaa"], "user", aaaaP2, "", []part{{"part_j4", "text", "opencode fixture one second prompt", false}}},
		{"msg_j2", ses["aaaa"], "assistant", aaaaA2, "", []part{{"part_j2", "text", "fixture one second reply", false}}},
		// ses_eeee: legacy-only conversation, incl. a synthetic part decoy.
		{"msg_e1", ses["eeee"], "user", eeeeP1, "", []part{{"part_e1", "text", "legacy only first prompt", false}}},
		{"msg_e2", ses["eeee"], "assistant", eeeeA1, "msg_e1", []part{{"part_e2", "text", "legacy only reply", false}}},
		// Multiple assistant messages can form one user turn (tool-call
		// continuations); parentID links them back to the same user message.
		{"msg_e2b", ses["eeee"], "assistant", eeeeA1 + 1000, "msg_e1", []part{{"part_e2b", "text", "legacy only tool continuation", false}}},
		{"msg_e3", ses["eeee"], "user", eeeeP2, "", []part{
			{"part_e3", "text", "legacy only second prompt", false},
			{"part_e4", "text", "synthetic legacy prompt that must not leak", true},
		}},
	}
	for _, m := range legacy {
		data := map[string]any{"role": m.role}
		if m.parentID != "" {
			data["parentID"] = m.parentID
		}
		mustExec(db, "INSERT INTO message VALUES (?,?,?,?,?)",
			m.id, m.sid, m.ts, m.ts+1000, mustJSON(data))
		for _, p := range m.parts {
			mustExec(db, "INSERT INTO part VALUES (?,?,?,?,?,?)",
				p.id, m.id, m.sid, m.ts, m.ts+1000,
				mustJSON(map[string]any{"type": p.typ, "text": p.text, "synthetic": p.synthetic}))
		}
	}

	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		panic(err)
	}
	fmt.Println("wrote", path)
}
