package sqliteread

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func walFixture(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "live.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, q := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA wal_autocheckpoint=0",
		"CREATE TABLE session (id TEXT PRIMARY KEY, title TEXT)",
		"INSERT INTO session VALUES ('a', 'first')",
		"INSERT INTO session VALUES ('b', 'second')",
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return db, path
}

func readTitles(t *testing.T, path string) map[string]string {
	t.Helper()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	tbl, err := db.Table("session")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	if err := tbl.Scan(func(r Row) error {
		id, _ := r.Str("id")
		title, _ := r.Str("title")
		out[id] = title
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// A live writer's committed frames sit in -wal until checkpoint. They must be
// visible (new sessions) and deletes must win (no resurrected rows).
func TestOpenOverlaysCommittedWAL(t *testing.T) {
	db, path := walFixture(t)

	rows := readTitles(t, path)
	if len(rows) != 2 || rows["a"] != "first" || rows["b"] != "second" {
		t.Fatalf("uncheckpointed inserts missing: %v", rows)
	}

	if _, err := db.Exec("DELETE FROM session WHERE id = 'b'"); err != nil {
		t.Fatal(err)
	}
	rows = readTitles(t, path)
	if _, ok := rows["b"]; ok {
		t.Errorf("deleted row resurrected from the pre-delete image: %v", rows)
	}
	if len(rows) != 1 || rows["a"] != "first" {
		t.Errorf("rows after delete = %v, want only a", rows)
	}
}
