package sqliteread

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	defer db.Close()
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

// A database with more pages than the page cache must scan correctly:
// interior and leaf pages are evicted and re-read mid-walk, overflow chains
// resolve, and every row arrives intact.
func TestScanWithPageEvictions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE big (id INTEGER PRIMARY KEY, body TEXT)"); err != nil {
		t.Fatal(err)
	}
	// 3000 rows of ~2KB fill ~1500 4KB pages, well past maxCachedPages; a
	// few 100KB rows add overflow chains. Body content is distinctive so a
	// stale or swapped page shows up as a wrong row.
	body := strings.Repeat("x", 2048)
	for i := 0; i < 3000; i += 100 {
		end := i + 100
		if end > 3000 {
			end = 3000
		}
		var sb strings.Builder
		sb.WriteString("INSERT INTO big (id, body) VALUES ")
		for j := i; j < end; j++ {
			if j > i {
				sb.WriteString(", ")
			}
			fmt.Fprintf(&sb, "(%d, '%d-%s')", j, j, body)
		}
		if _, err := db.Exec(sb.String()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("INSERT INTO big (id, body) VALUES (9000, 'overflow-" + strings.Repeat("y", 100*1024) + "')"); err != nil {
		t.Fatal(err)
	}

	rd, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()
	tbl, err := rd.Table("big")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	overflowOK := false
	err = tbl.Scan(func(r Row) error {
		// INTEGER PRIMARY KEY columns are rowid aliases: the record stores a
		// NULL there and sqliteread does not substitute the rowid, so track
		// the expected id by scan order (rowid order) instead.
		id := count
		count++
		b, _ := r.Str("body")
		if id < 3000 {
			if b != fmt.Sprintf("%d-%s", id, body) {
				t.Errorf("row %d corrupted: got %d bytes, want the %d-filled body", id, len(b), id)
			}
		} else {
			overflowOK = len(b) == len("overflow-")+100*1024 && strings.HasSuffix(b, strings.Repeat("y", 100*1024))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 3001 {
		t.Errorf("scanned %d rows, want 3001 (cache eviction lost or duplicated rows)", count)
	}
	if !overflowOK {
		t.Errorf("overflow row 9000 missing or corrupted")
	}
}

// A -wal sidecar past the read cap must not be loaded: scanning falls back
// to the main file's checkpoint snapshot. The WAL here holds an
// uncheckpointed insert of 'c'; capped out, only the checkpointed rows a,b
// are visible. Sparse truncate keeps a 256MB "file" cheap.
func TestOversizeWALFallsBackToSnapshot(t *testing.T) {
	db, path := walFixture(t)
	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO session VALUES ('c', 'third')"); err != nil {
		t.Fatal(err)
	}
	// Sanity: with the small WAL in place the overlay applies.
	rows := readTitles(t, path)
	if len(rows) != 3 || rows["c"] != "third" {
		t.Fatalf("small WAL overlay broken: %v", rows)
	}
	if err := os.Truncate(path+"-wal", maxWALBytes+1); err != nil {
		t.Fatal(err)
	}
	rows = readTitles(t, path)
	if _, ok := rows["c"]; ok {
		t.Errorf("oversize WAL was applied; want fallback to checkpoint snapshot: %v", rows)
	}
	if len(rows) != 2 || rows["a"] != "first" || rows["b"] != "second" {
		t.Errorf("checkpoint snapshot rows = %v, want a,b", rows)
	}
}

// A main file truncated after Open simulates a writer checkpointing
// mid-scan: the walk fails, the retry with a cold cache fails too, and the
// error surfaces cleanly instead of panicking.
func TestTruncatedFileFailsCleanly(t *testing.T) {
	db, path := walFixture(t)
	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	rd, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	tbl, err := rd.Table("session")
	if err == nil {
		err = tbl.Scan(func(Row) error { return nil })
	}
	if err == nil {
		t.Fatal("scan of a truncated database succeeded; want a clean error")
	}
}
