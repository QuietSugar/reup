// Package sqliteread is a minimal read-only SQLite file scanner. reup
// needs to list rows from opencode's session database without taking on a
// SQLite driver dependency, so this implements just enough of the on-disk
// format for one job: walk a table b-tree and decode records.
//
// Supported: UTF-8 databases, table b-trees (interior + leaf), all serial
// types, overflow page chains, any page size. Rows are reported as
// name → Value maps, with column names parsed from the stored CREATE TABLE
// statement so mild schema drift across app versions stays harmless.
//
// Memory profile: the main database file is read lazily page-by-page through
// a small LRU cache (see maxCachedPages), so file size does not bound RSS,
// and table scans stream one decoded row at a time. The -wal sidecar is the
// one file loaded whole, capped at maxWALBytes.
//
// Committed WAL frames are overlaid on the database image, so a live
// writer's uncheckpointed inserts and deletes are visible (and deleted rows
// are not resurrected). Frames past the last commit, or with a stale salt or
// bad checksum, are ignored — they are an in-progress write, not state.
//
// Not supported (explicitly): indexes, views, WITHOUT ROWID tables, and
// indexes on the queried table are ignored.
package sqliteread

import (
	"container/list"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
)

// Value is one cell value: nil, int64, float64, string, or []byte.
type Value any

// ErrNotFound is returned when the requested table does not exist.
var ErrNotFound = errors.New("sqliteread: table not found")

// maxCachedPages bounds the main-database page cache. The cache exists so
// b-tree interior pages and hot leaves are not re-read from disk; it is not
// a retention store — walk accesses pages transiently, so an evicted page is
// simply re-read on demand. At the maximum page size (64KB) this caps the
// cache at 64MB; the far more common 4KB pages cap it at 4MB.
const maxCachedPages = 1024

// maxWALBytes bounds the -wal sidecar read into memory. A WAL past this size
// is treated as unusable: scanning falls back to the main database's last
// checkpoint snapshot instead of loading gigabytes of frames.
const maxWALBytes = 256 << 20

type db struct {
	f        *os.File // main database file; nil for a WAL-only database
	size     int64    // main file size at open, for page bounds checks
	pageSize int
	usable   int
	// wal holds committed page images from the -wal file, keyed by page
	// number; walPages is the database size after the last commit (0 when no
	// WAL was applied).
	wal      map[int][]byte
	walPages int
	// cache is a small LRU of main-file pages read on demand, so database
	// size does not dictate memory usage.
	cache map[int]*list.Element
	lru   *list.List // element values are pageBuf, oldest at the front
}

type pageBuf struct {
	pgno int
	data []byte
}

// Open opens the database file and overlays its committed WAL frames. The
// main file is read page-by-page through a small cache — never whole into
// memory — so a multi-gigabyte database scans in bounded memory.
func Open(path string) (*DB, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	wal := readWAL(path)
	d, err := openFile(f, st.Size())
	if err == nil {
		d.d.applyWAL(wal)
		return d, nil
	}
	f.Close()
	// A database created in WAL mode stays zero bytes until its first
	// checkpoint: page 1 and everything else live in the WAL.
	if d, ok := openWALOnly(wal); ok {
		return d, nil
	}
	return nil, err
}

// Close releases the database file handle.
func (d *DB) Close() error {
	if d.d.f == nil {
		return nil
	}
	return d.d.f.Close()
}

// readWAL loads the -wal sidecar when it exists and stays within maxWALBytes;
// an absent or oversize WAL reads as empty, which callers treat as "no
// committed overlay" (the fallback documented on maxWALBytes).
func readWAL(path string) []byte {
	st, err := os.Stat(path + "-wal")
	if err != nil || st.Size() > maxWALBytes {
		return nil
	}
	wal, _ := os.ReadFile(path + "-wal")
	return wal
}

// DB is an opened read-only database.
type DB struct {
	d *db
}

func openFile(f *os.File, size int64) (*DB, error) {
	if size < 100 {
		return nil, fmt.Errorf("sqliteread: not a sqlite 3 database")
	}
	hdr := make([]byte, 100)
	if _, err := f.ReadAt(hdr, 0); err != nil {
		return nil, err
	}
	ps, reserved, err := pageParams(hdr)
	if err != nil {
		return nil, err
	}
	d := &db{
		f: f, size: size, pageSize: ps, usable: ps - reserved,
		cache: map[int]*list.Element{}, lru: list.New(),
	}
	return &DB{d: d}, nil
}

// openWALOnly builds a database image from a WAL whose main file has not been
// checkpointed yet. Returns false when the WAL is unusable.
func openWALOnly(wal []byte) (*DB, bool) {
	ps, ok := walPageSize(wal)
	if !ok {
		return nil, false
	}
	pages, size := parseWAL(wal, ps)
	page1, ok := pages[1]
	if !ok || size == 0 {
		return nil, false
	}
	ps, reserved, err := pageParams(page1)
	if err != nil {
		return nil, false
	}
	return &DB{d: &db{pageSize: ps, usable: ps - reserved, wal: pages, walPages: size}}, true
}

// pageParams validates a database header (or page 1) and extracts the page
// and reserved sizes.
func pageParams(data []byte) (int, int, error) {
	if len(data) < 100 || string(data[:16]) != "SQLite format 3\x00" {
		return 0, 0, fmt.Errorf("sqliteread: not a sqlite 3 database")
	}
	ps := int(binary.BigEndian.Uint16(data[16:18]))
	if ps == 1 {
		ps = 65536
	}
	if ps < 512 || (ps&(ps-1)) != 0 {
		return 0, 0, fmt.Errorf("sqliteread: bad page size %d", ps)
	}
	reserved := int(data[20])
	if reserved >= ps {
		return 0, 0, fmt.Errorf("sqliteread: bad reserved space %d", reserved)
	}
	enc := binary.BigEndian.Uint32(data[56:60])
	if enc != 1 {
		return 0, 0, fmt.Errorf("sqliteread: only utf-8 databases supported (encoding=%d)", enc)
	}
	return ps, reserved, nil
}

const (
	walHeaderSize      = 32
	walFrameHeaderSize = 24
	walVersion         = 3007000
	walMagicLE         = 0x377f0682 // checksum words are little-endian
	walMagicBE         = 0x377f0683 // checksum words are big-endian
)

// applyWAL overlays the committed frames of a -wal file. Silently keeps the
// plain database when the WAL header is unusable or no frame is committed:
// the main file is then a consistent snapshot as of the last checkpoint.
func (d *db) applyWAL(wal []byte) {
	pages, size := parseWAL(wal, d.pageSize)
	if size == 0 {
		return
	}
	d.wal, d.walPages = pages, size
}

// walPageSize returns the page size recorded in a WAL header.
func walPageSize(wal []byte) (int, bool) {
	if len(wal) < walHeaderSize {
		return 0, false
	}
	magic := binary.BigEndian.Uint32(wal[0:4])
	if magic != walMagicLE && magic != walMagicBE {
		return 0, false
	}
	if binary.BigEndian.Uint32(wal[4:8]) != walVersion {
		return 0, false
	}
	ps := int(binary.BigEndian.Uint32(wal[8:12]))
	if ps == 1 {
		ps = 65536
	}
	if ps < 512 || (ps&(ps-1)) != 0 {
		return 0, false
	}
	return ps, true
}

// parseWAL returns the committed page images and the database size in pages
// after the last commit. Frames past the last commit, and any frame that fails
// salt or checksum validation, are ignored.
func parseWAL(wal []byte, pageSize int) (map[int][]byte, int) {
	ps, ok := walPageSize(wal)
	if !ok || ps != pageSize {
		return nil, 0
	}
	magic := binary.BigEndian.Uint32(wal[0:4])
	little := magic == walMagicLE
	salt1 := binary.BigEndian.Uint32(wal[16:20])
	salt2 := binary.BigEndian.Uint32(wal[20:24])
	s0, s1, ok := walChecksum(wal[:24], 0, 0, little)
	if !ok || s0 != binary.BigEndian.Uint32(wal[24:28]) || s1 != binary.BigEndian.Uint32(wal[28:32]) {
		return nil, 0
	}

	frameSize := walFrameHeaderSize + ps
	committed := map[int][]byte{}
	pending := map[int][]byte{}
	pages := 0
	for off := walHeaderSize; off+frameSize <= len(wal); off += frameSize {
		frame := wal[off : off+frameSize]
		if binary.BigEndian.Uint32(frame[8:12]) != salt1 ||
			binary.BigEndian.Uint32(frame[12:16]) != salt2 {
			break
		}
		// The checksum runs over the first 8 bytes of the frame header and
		// the page image; the last 8 bytes are the value being checked.
		if s0, s1, ok = walChecksum(frame[:8], s0, s1, little); !ok {
			break
		}
		if s0, s1, ok = walChecksum(frame[walFrameHeaderSize:], s0, s1, little); !ok {
			break
		}
		if s0 != binary.BigEndian.Uint32(frame[16:20]) || s1 != binary.BigEndian.Uint32(frame[20:24]) {
			break
		}
		pgno := int(binary.BigEndian.Uint32(frame[0:4]))
		if pgno < 1 {
			break
		}
		pending[pgno] = frame[walFrameHeaderSize:]
		if n := binary.BigEndian.Uint32(frame[4:8]); n != 0 {
			for p, data := range pending {
				committed[p] = data
			}
			pending = map[int][]byte{}
			pages = int(n)
		}
	}
	if pages == 0 {
		return nil, 0 // no commit: the WAL holds only an in-progress write
	}
	return committed, pages
}

// walChecksum is SQLite's WAL checksum, which folds pairs of 32-bit words:
//
//	s0 += x(i) + s1
//	s1 += x(i+1) + s0
//
// The input must contain a whole number of 8-byte blocks.
func walChecksum(b []byte, s0, s1 uint32, little bool) (uint32, uint32, bool) {
	if len(b)%8 != 0 {
		return 0, 0, false
	}
	var order binary.ByteOrder = binary.BigEndian
	if little {
		order = binary.LittleEndian
	}
	for i := 0; i < len(b); i += 8 {
		x0 := order.Uint32(b[i : i+4])
		x1 := order.Uint32(b[i+4 : i+8])
		s0 += x0 + s1
		s1 += x1 + s0
	}
	return s0, s1, true
}

type table struct {
	d       *db
	root    int
	columns []string
	index   map[string]int
}

// Table returns a handle to a user table by name.
func (d *DB) Table(name string) (*Table, error) {
	var rows [][]Value
	err := d.d.withRetry(func() error {
		var err error
		rows, err = d.d.scan(1) // sqlite_master b-tree is rooted at page 1
		return err
	})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if s, _ := row[0].(string); s != "table" {
			continue
		}
		if s, _ := row[1].(string); s != name {
			continue
		}
		if len(row) < 5 {
			continue
		}
		root, ok := row[3].(int64)
		if !ok || root < 1 {
			continue
		}
		ddl, _ := row[4].(string)
		cols := columnNames(ddl)
		if len(cols) == 0 {
			return nil, fmt.Errorf("sqliteread: cannot parse DDL for table %s", name)
		}
		idx := make(map[string]int, len(cols))
		for i, c := range cols {
			if _, dup := idx[c]; !dup {
				idx[c] = i
			}
		}
		return &Table{d: d.d, root: int(root), columns: cols, index: idx}, nil
	}
	return nil, ErrNotFound
}

// Table is a scan handle for one user table.
type Table struct {
	d       *db
	root    int
	columns []string
	index   map[string]int
}

// Columns lists the column names in declaration order.
func (t *Table) Columns() []string {
	return append([]string(nil), t.columns...)
}

// Row is one decoded record keyed by column name; absent columns are nil.
type Row map[string]Value

// Get returns the value for a column (nil when absent).
func (r Row) Get(col string) Value { return r[col] }

// Int returns the column as int64 (false when not an integer).
func (r Row) Int(col string) (int64, bool) {
	switch v := r[col].(type) {
	case int64:
		return v, true
	case float64:
		return int64(v), true
	}
	return 0, false
}

// Str returns the column as string (false when not text).
func (r Row) Str(col string) (string, bool) {
	s, ok := r[col].(string)
	return s, ok
}

// Scan walks the whole table b-tree in rowid order, calling fn per row.
// Return an error from fn to abort (returned verbatim).
func (t *Table) Scan(fn func(Row) error) error {
	err := t.d.withRetry(func() error {
		return t.d.walk(t.root, func(rec []Value) error {
			r := make(Row, len(t.columns))
			for i, c := range t.columns {
				if i < len(rec) {
					r[c] = rec[i]
				} else {
					r[c] = nil
				}
			}
			return fn(r)
		}, 0)
	})
	var ab *abortError
	if errors.As(err, &ab) {
		return ab.err
	}
	return err
}

// scan walks a table b-tree rooted at the given page, collecting decoded
// records (cell values in column order). Only for small tables; large ones
// stream through walk.
func (d *db) scan(root int) ([][]Value, error) {
	var out [][]Value
	err := d.walk(root, func(rec []Value) error {
		out = append(out, rec)
		return nil
	}, 0)
	return out, err
}

// abortError wraps an error returned by a Scan callback: it aborts the walk
// on purpose and must not be retried.
type abortError struct{ err error }

func (e *abortError) Error() string { return e.err.Error() }

// withRetry runs fn, and on failure flushes the page cache and runs it once
// more. A live writer can checkpoint — truncate or rewrite — the main file
// while a scan is in flight; pages cached from the old image then make the
// b-tree walk fail, and re-reading from the new image is the cheap fix.
// Callback aborts pass through untouched.
func (d *db) withRetry(fn func() error) error {
	err := fn()
	if err == nil || d.f == nil {
		return err
	}
	var ab *abortError
	if errors.As(err, &ab) {
		return err
	}
	d.clearCache()
	return fn()
}

// walk traverses the table b-tree rooted at pgno, invoking fn per decoded
// record in rowid order so a table scan never materializes more than one
// row. An error from fn aborts the walk and is returned wrapped in
// abortError.
func (d *db) walk(pgno int, fn func([]Value) error, depth int) error {
	if depth > 64 {
		return fmt.Errorf("sqliteread: b-tree too deep")
	}
	page, err := d.page(pgno)
	if err != nil {
		return err
	}
	base := 0
	if pgno == 1 {
		base = 100
	}
	if base >= len(page) {
		return fmt.Errorf("sqliteread: page %d truncated", pgno)
	}
	typ := page[base]
	switch typ {
	case 0x0d: // table leaf
		n := int(binary.BigEndian.Uint16(page[base+3 : base+5]))
		hdrEnd := base + 8
		for i := 0; i < n; i++ {
			off := int(binary.BigEndian.Uint16(page[hdrEnd+2*i : hdrEnd+2*i+2]))
			if off <= 0 || off >= len(page) {
				continue
			}
			payload, err := d.cellPayload(page, off)
			if err != nil {
				return err
			}
			rec, err := decodeRecord(payload)
			if err != nil {
				return err
			}
			if err := fn(rec); err != nil {
				return &abortError{err}
			}
		}
		return nil
	case 0x05: // table interior
		n := int(binary.BigEndian.Uint16(page[base+3 : base+5]))
		hdrEnd := base + 12
		for i := 0; i < n; i++ {
			off := int(binary.BigEndian.Uint16(page[hdrEnd+2*i : hdrEnd+2*i+2]))
			if off < 0 || off+4 > len(page) {
				continue
			}
			child := int(binary.BigEndian.Uint32(page[off : off+4]))
			if err := d.walk(child, fn, depth+1); err != nil {
				return err
			}
		}
		right := int(binary.BigEndian.Uint32(page[base+8 : base+12]))
		return d.walk(right, fn, depth+1)
	default:
		return fmt.Errorf("sqliteread: page %d is not a table b-tree page (type %#x)", pgno, typ)
	}
}

// cellPayload assembles one leaf cell's payload, following the overflow
// chain when the record spills.
func (d *db) cellPayload(page []byte, off int) ([]byte, error) {
	pos := off
	payloadLen, n, err := readVarint(page, pos)
	if err != nil {
		return nil, err
	}
	pos += n
	// rowid (ignored)
	_, n, err = readVarint(page, pos)
	if err != nil {
		return nil, err
	}
	pos += n

	P := int(payloadLen)
	U := d.usable
	X := U - 35
	local := P
	var overflowPgno int
	if P > X {
		M := ((U - 12) * 32 / 255) - 23
		K := M + (P-M)%(U-4)
		if K <= X {
			local = K
		} else {
			local = M
		}
		end := pos + local + 4
		if end > len(page) {
			return nil, fmt.Errorf("sqliteread: cell overruns page")
		}
		overflowPgno = int(binary.BigEndian.Uint32(page[pos+local : end]))
	}
	if pos+local > len(page) {
		return nil, fmt.Errorf("sqliteread: cell overruns page")
	}
	payload := make([]byte, 0, P)
	payload = append(payload, page[pos:pos+local]...)
	for overflowPgno != 0 && len(payload) < P {
		op, err := d.page(overflowPgno)
		if err != nil {
			return nil, err
		}
		next := int(binary.BigEndian.Uint32(op[0:4]))
		chunk := op[4:U]
		need := P - len(payload)
		if need > len(chunk) {
			need = len(chunk)
		}
		payload = append(payload, chunk[:need]...)
		overflowPgno = next
	}
	if len(payload) < P {
		return nil, fmt.Errorf("sqliteread: payload truncated (%d < %d)", len(payload), P)
	}
	return payload[:P], nil
}

func (d *db) page(pgno int) ([]byte, error) {
	if pgno < 1 {
		return nil, fmt.Errorf("sqliteread: bad page number %d", pgno)
	}
	if p, ok := d.wal[pgno]; ok {
		return p, nil
	}
	if d.walPages != 0 && pgno > d.walPages {
		return nil, fmt.Errorf("sqliteread: page %d out of range", pgno)
	}
	start := int64(pgno-1) * int64(d.pageSize)
	if start+int64(d.pageSize) > d.size {
		return nil, fmt.Errorf("sqliteread: page %d out of range", pgno)
	}
	if el, ok := d.cache[pgno]; ok {
		d.lru.MoveToBack(el)
		return el.Value.(pageBuf).data, nil
	}
	data := make([]byte, d.pageSize)
	if _, err := d.f.ReadAt(data, start); err != nil {
		return nil, err
	}
	d.cache[pgno] = d.lru.PushBack(pageBuf{pgno, data})
	for d.lru.Len() > maxCachedPages {
		front := d.lru.Front()
		d.lru.Remove(front)
		delete(d.cache, front.Value.(pageBuf).pgno)
	}
	return data, nil
}

// clearCache drops every cached main-file page. Used when a scan fails and
// is retried: the failure may be a stale cached image of a file a live
// writer just checkpointed.
func (d *db) clearCache() {
	d.cache = map[int]*list.Element{}
	d.lru.Init()
}

func readVarint(b []byte, pos int) (int64, int, error) {
	var v uint64
	for i := 0; i < 9; i++ {
		if pos+i >= len(b) {
			return 0, 0, fmt.Errorf("sqliteread: varint truncated")
		}
		c := b[pos+i]
		if i == 8 {
			v = v<<8 | uint64(c)
			return int64(v), 9, nil
		}
		v = v<<7 | uint64(c&0x7f)
		if c&0x80 == 0 {
			return int64(v), i + 1, nil
		}
	}
	return 0, 0, fmt.Errorf("sqliteread: varint too long")
}

func decodeRecord(payload []byte) ([]Value, error) {
	hdrLen, n, err := readVarint(payload, 0)
	if err != nil {
		return nil, err
	}
	if hdrLen < int64(n) || hdrLen > int64(len(payload)) {
		return nil, fmt.Errorf("sqliteread: bad record header size %d", hdrLen)
	}
	var serials []int64
	pos := n
	for pos < int(hdrLen) {
		st, sn, err := readVarint(payload, pos)
		if err != nil {
			return nil, err
		}
		pos += sn
		serials = append(serials, st)
	}
	body := int(hdrLen)
	out := make([]Value, 0, len(serials))
	for _, st := range serials {
		val, size, err := decodeValue(payload, body, st)
		if err != nil {
			return nil, err
		}
		body += size
		out = append(out, val)
	}
	return out, nil
}

func decodeValue(b []byte, off int, serial int64) (Value, int, error) {
	need := func(n int) ([]byte, error) {
		if off+n > len(b) {
			return nil, fmt.Errorf("sqliteread: record body truncated")
		}
		return b[off : off+n], nil
	}
	switch {
	case serial == 0:
		return nil, 0, nil
	case serial >= 1 && serial <= 6:
		sizes := []int{0, 1, 2, 3, 4, 6, 8}
		n := sizes[serial]
		raw, err := need(n)
		if err != nil {
			return nil, 0, err
		}
		var v int64
		if serial == 5 || serial == 6 { // signed big-endian
			v = int64(raw[0])
			for _, c := range raw[1:] {
				v = v<<8 | int64(c)
			}
			// sign-extend from n bytes
			shift := uint(64 - 8*n)
			v = v << shift >> shift
		} else {
			v = int64(int8(raw[0]))
			for _, c := range raw[1:] {
				v = v<<8 | int64(c)
			}
			shift := uint(64 - 8*n)
			v = v << shift >> shift
		}
		return v, n, nil
	case serial == 7:
		raw, err := need(8)
		if err != nil {
			return nil, 0, err
		}
		return math.Float64frombits(binary.BigEndian.Uint64(raw)), 8, nil
	case serial == 8:
		return int64(0), 0, nil
	case serial == 9:
		return int64(1), 0, nil
	case serial >= 12 && serial%2 == 0:
		n := int((serial - 12) / 2)
		raw, err := need(n)
		if err != nil {
			return nil, 0, err
		}
		out := make([]byte, n)
		copy(out, raw)
		return out, n, nil
	case serial >= 13:
		n := int((serial - 13) / 2)
		raw, err := need(n)
		if err != nil {
			return nil, 0, err
		}
		return string(raw), n, nil
	default:
		return nil, 0, fmt.Errorf("sqliteread: reserved serial type %d", serial)
	}
}

// columnNames extracts column names from a CREATE TABLE statement. Table
// constraints (PRIMARY KEY, UNIQUE, ...) are skipped; identifiers may be
// quoted with "…", `…`, […], or plain.
func columnNames(ddl string) []string {
	open := strings.Index(ddl, "(")
	if open < 0 {
		return nil
	}
	depth := 0
	close := -1
	inQuote := byte(0)
	for i := open; i < len(ddl); i++ {
		c := ddl[i]
		if inQuote != 0 {
			if c == inQuote {
				inQuote = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			inQuote = c
		case '[':
			inQuote = ']'
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				close = i
			}
		}
		if close >= 0 {
			break
		}
	}
	if close < 0 {
		return nil
	}
	body := ddl[open+1 : close]

	var cols []string
	for _, def := range splitTop(body) {
		def = strings.TrimSpace(def)
		if def == "" {
			continue
		}
		name, rest := firstIdentifier(def)
		if name == "" {
			continue
		}
		upper := strings.ToUpper(name)
		if upper == "PRIMARY" || upper == "UNIQUE" || upper == "CHECK" ||
			upper == "FOREIGN" || upper == "CONSTRAINT" {
			continue
		}
		_ = rest
		cols = append(cols, name)
	}
	return cols
}

// firstIdentifier pulls the (possibly quoted) identifier off the front.
func firstIdentifier(s string) (string, string) {
	s = strings.TrimLeft(s, " \t\r\n")
	if s == "" {
		return "", ""
	}
	switch s[0] {
	case '"', '\'', '`':
		q := s[0]
		var b strings.Builder
		for i := 1; i < len(s); i++ {
			if s[i] == q {
				if i+1 < len(s) && s[i+1] == q { // escaped quote
					b.WriteByte(q)
					i++
					continue
				}
				return b.String(), s[i+1:]
			}
			b.WriteByte(s[i])
		}
		return b.String(), ""
	case '[':
		if end := strings.Index(s, "]"); end > 0 {
			return s[1:end], s[end+1:]
		}
		return "", ""
	}
	end := 0
	for end < len(s) && !strings.ContainsRune(" \t\r\n(,", rune(s[end])) {
		end++
	}
	return s[:end], s[end:]
}

// splitTop splits on top-level commas (quote- and paren-aware).
func splitTop(s string) []string {
	var parts []string
	depth := 0
	inQuote := byte(0)
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQuote != 0 {
			if c == inQuote {
				inQuote = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			inQuote = c
		case '[':
			inQuote = ']'
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}
