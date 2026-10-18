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
// Not supported (explicitly): WAL replay — committed frames still sitting in
// a -wal file are invisible until the writer checkpoints. Indexes, views,
// WITHOUT ROWID tables, and indexes on the queried table are ignored.
package sqliteread

import (
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

type db struct {
	data     []byte
	pageSize int
	usable   int
}

// Open memory-maps-bytes the database file and validates its header.
func Open(path string) (*DB, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return openBytes(data)
}

// DB is an opened read-only database.
type DB struct {
	d *db
}

func openBytes(data []byte) (*DB, error) {
	if len(data) < 100 || string(data[:16]) != "SQLite format 3\x00" {
		return nil, fmt.Errorf("sqliteread: not a sqlite 3 database")
	}
	ps := int(binary.BigEndian.Uint16(data[16:18]))
	if ps == 1 {
		ps = 65536
	}
	if ps < 512 || (ps&(ps-1)) != 0 {
		return nil, fmt.Errorf("sqliteread: bad page size %d", ps)
	}
	reserved := int(data[20])
	if reserved >= ps {
		return nil, fmt.Errorf("sqliteread: bad reserved space %d", reserved)
	}
	enc := binary.BigEndian.Uint32(data[56:60])
	if enc != 1 {
		return nil, fmt.Errorf("sqliteread: only utf-8 databases supported (encoding=%d)", enc)
	}
	return &DB{d: &db{data: data, pageSize: ps, usable: ps - reserved}}, nil
}

type table struct {
	d       *db
	root    int
	columns []string
	index   map[string]int
}

// Table returns a handle to a user table by name.
func (d *DB) Table(name string) (*Table, error) {
	rows, err := d.d.scan(1) // sqlite_master b-tree is rooted at page 1
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
	rows, err := t.d.scan(t.root)
	if err != nil {
		return err
	}
	for _, row := range rows {
		r := make(Row, len(t.columns))
		for i, c := range t.columns {
			if i < len(row) {
				r[c] = row[i]
			} else {
				r[c] = nil
			}
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return nil
}

// scan walks a table b-tree rooted at the given page, returning decoded
// records (cell values in column order).
func (d *db) scan(root int) ([][]Value, error) {
	var out [][]Value
	if err := d.walk(root, &out, 0); err != nil {
		return nil, err
	}
	return out, nil
}

func (d *db) walk(pgno int, out *[][]Value, depth int) error {
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
			*out = append(*out, rec)
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
			if err := d.walk(child, out, depth+1); err != nil {
				return err
			}
		}
		right := int(binary.BigEndian.Uint32(page[base+8 : base+12]))
		return d.walk(right, out, depth+1)
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
	start := (pgno - 1) * d.pageSize
	if start+d.pageSize > len(d.data) {
		return nil, fmt.Errorf("sqliteread: page %d out of range", pgno)
	}
	return d.data[start : start+d.pageSize], nil
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
