package filekind

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/zeebo/blake3"
)

// streamMagic opens every canonical row stream. A change to the encoding is a
// new magic, never a reinterpretation of this one.
const streamMagic = "filekind rows v1\n"

// Value tags of the canonical row stream (SPEC.md, "Canonical row digest").
const (
	tagNull    byte = 0x00
	tagInteger byte = 0x01
	tagReal    byte = 0x02
	tagText    byte = 0x03
	tagBlob    byte = 0x04
	tagEnd     byte = 0x05
)

// RowSet names the rows a digest covers: Columns of the base table Table, in
// the order given, every row ordered by Key under BINARY collation. Kind and Set
// are the domain the digest is separated by, so the same rows digested for two
// kinds, or as two sets of one kind, never share a digest.
//
// Key must be unique over the rows Where selects, because rows that tie on
// the key have no order an engine is bound to; a tie is refused with
// ErrKeyNotUnique rather than digested in whatever order the engine chose.
// Where is an optional SQL condition with Args as its parameters. Schema names
// an attached database; empty is the main one.
type RowSet struct {
	Kind    Kind
	Set     string
	Schema  string
	Table   string
	Columns []string
	Key     []string
	Where   string
	Args    []any
}

// Digest is a row set's SHA-256 and BLAKE3 over its canonical stream, and the
// number of rows the stream holds. Stored in a file, each hash is lowercase
// hex TEXT.
type Digest struct {
	SHA256 [32]byte
	BLAKE3 [32]byte
	Rows   uint64
}

// SHA256Hex is the SHA-256 as lowercase hex, the form a file stores it in.
func (d Digest) SHA256Hex() string { return hex.EncodeToString(d.SHA256[:]) }

// BLAKE3Hex is the BLAKE3 as lowercase hex, the form a file stores it in.
func (d Digest) BLAKE3Hex() string { return hex.EncodeToString(d.BLAKE3[:]) }

// DigestRows hashes the canonical stream of set's rows. It reads rows through
// q and never the file's bytes, so two files holding the same rows digest the
// same however their pages are laid out.
func DigestRows(ctx context.Context, q Querier, set RowSet) (Digest, error) {
	s := sha256.New()
	b := blake3.New()
	n, err := EncodeRows(ctx, q, set, io.MultiWriter(s, b))
	if err != nil {
		return Digest{}, err
	}
	var d Digest
	copy(d.SHA256[:], s.Sum(nil))
	copy(d.BLAKE3[:], b.Sum(nil))
	d.Rows = n
	return d, nil
}

// EncodeRows writes set's canonical stream to w and returns its row count. A
// refusal can come after part of the stream is written; the partial stream is
// not a digest of anything.
func EncodeRows(ctx context.Context, q Querier, set RowSet, w io.Writer) (uint64, error) {
	if err := set.valid(); err != nil {
		return 0, err
	}
	if err := requireBaseTable(ctx, q, set.Schema, set.Table); err != nil {
		return 0, err
	}
	bw := bufio.NewWriter(w)
	writeStreamHeader(bw, set)

	rows, err := q.QueryContext(ctx, set.query(), set.Args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	width := 2 * (len(set.Key) + len(set.Columns))
	cells := make([]any, width)
	ptrs := make([]any, width)
	for i := range cells {
		ptrs[i] = &cells[i]
	}
	var previous, current []cell
	var count uint64
	for rows.Next() {
		for i := range cells {
			cells[i] = nil
		}
		if err := rows.Scan(ptrs...); err != nil {
			return count, err
		}
		current = current[:0]
		for i := 0; i < width; i += 2 {
			c, err := readCell(cells[i], cells[i+1])
			if err != nil {
				return count, err
			}
			current = append(current, c)
		}
		key := current[:len(set.Key)]
		if count > 0 && sameKey(previous, key) {
			return count, ErrKeyNotUnique
		}
		for _, c := range current[len(set.Key):] {
			c.encode(bw)
		}
		previous = append(previous[:0], key...)
		count++
	}
	if err := rows.Err(); err != nil {
		return count, err
	}
	bw.WriteByte(tagEnd)
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], count)
	bw.Write(n[:])
	if err := bw.Flush(); err != nil {
		return count, err
	}
	return count, rows.Close()
}

func (s RowSet) valid() error {
	bad := func(what string) error { return fmt.Errorf("%w: row set %s", ErrInvalidArgument, what) }
	if _, ok := Lookup(s.Kind); !ok {
		return bad("kind is not registered")
	}
	if !validName(s.Set) {
		return bad("set label is empty or not UTF-8")
	}
	if s.Schema != "" && !validName(s.Schema) {
		return bad("schema name is not UTF-8")
	}
	if !validName(s.Table) {
		return bad("table name is empty or not UTF-8")
	}
	if len(s.Columns) == 0 || len(s.Key) == 0 {
		return bad("has no columns or no key")
	}
	for _, name := range append(append([]string(nil), s.Columns...), s.Key...) {
		if !validName(name) {
			return bad("column name is empty or not UTF-8")
		}
	}
	return nil
}

// validName refuses an empty name, invalid UTF-8 and NUL, the one byte a
// quoted identifier cannot carry.
func validName(name string) bool {
	return name != "" && utf8.ValidString(name) && !strings.ContainsRune(name, 0)
}

func quoteName(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// query is the one statement a row set is read through (SPEC.md, "The query").
// Each value is selected as typeof(x) beside +x: typeof is the storage class
// the engine holds, and the unary plus is the same value with no declared
// type, so no driver converts it by the column's declaration (tursogo and
// modernc both turn TEXT in a DATETIME column into time.Time).
func (s RowSet) query() string {
	var b strings.Builder
	b.WriteString("SELECT ")
	first := true
	for _, name := range append(append([]string(nil), s.Key...), s.Columns...) {
		if !first {
			b.WriteString(", ")
		}
		first = false
		q := quoteName(name)
		b.WriteString("typeof(" + q + "), +" + q)
	}
	b.WriteString(" FROM ")
	if s.Schema != "" {
		b.WriteString(quoteName(s.Schema) + ".")
	}
	b.WriteString(quoteName(s.Table))
	if s.Where != "" {
		b.WriteString(" WHERE (" + s.Where + ")")
	}
	b.WriteString(" ORDER BY ")
	for i, name := range s.Key {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(quoteName(name) + " COLLATE BINARY")
	}
	return b.String()
}

// requireBaseTable refuses a table the schema does not hold as an ordinary
// table: a view (its rows are whatever its stored query says, which an
// untrusted file chooses), a virtual table, an engine's own sqlite_ table, or
// no object at all. Names compare under ASCII case folding, as the engine
// resolves them.
func requireBaseTable(ctx context.Context, q Querier, schema, table string) error {
	source := "sqlite_schema"
	if schema != "" {
		source = quoteName(schema) + ".sqlite_schema"
	}
	rows, err := q.QueryContext(ctx, "SELECT type, name, sql FROM "+source)
	if err != nil {
		return err
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var kind, name, text any
		if err := rows.Scan(&kind, &name, &text); err != nil {
			return err
		}
		if !equalFoldASCII(asString(name), table) {
			continue
		}
		sql := strings.ToUpper(strings.TrimSpace(asString(text)))
		if asString(kind) != "table" || strings.HasPrefix(sql, "CREATE VIRTUAL") ||
			strings.HasPrefix(strings.ToLower(table), "sqlite_") {
			return fmt.Errorf("%w: %s", ErrNotBaseTable, table)
		}
		found = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrNotBaseTable, table)
	}
	return rows.Close()
}

// equalFoldASCII folds A-Z only, as the engine compares identifiers; Unicode
// folding would match names the engine holds apart.
func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	}
	return ""
}

// writeStreamHeader writes the magic and the domain: the kind's
// application_id, the set label, the table, the columns and the key.
func writeStreamHeader(w *bufio.Writer, s RowSet) {
	w.WriteString(streamMagic)
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(s.Kind.ApplicationID()))
	w.Write(n[:])
	writeString(w, s.Set)
	writeString(w, s.Table)
	writeList(w, s.Columns)
	writeList(w, s.Key)
}

func writeList(w *bufio.Writer, names []string) {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(names)))
	w.Write(n[:])
	for _, name := range names {
		writeString(w, name)
	}
}

func writeString(w *bufio.Writer, s string) {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(s)))
	w.Write(n[:])
	w.WriteString(s)
}

// cell is one value with the storage class typeof reported for it.
type cell struct {
	tag   byte
	i     int64
	f     float64
	bytes []byte
}

// readCell joins a typeof answer to the value beside it, refusing a Go type
// that disagrees with the class, NaN (which SQLite never stores), and text that
// is not UTF-8. A REAL negative zero becomes positive zero: SQLite reads a
// stored -0.0 back as 0.0 and the Turso engine as -0.0, so only the canonical
// form digests the same on both.
func readCell(class, value any) (cell, error) {
	name := asString(class)
	switch name {
	case "null":
		if value != nil {
			return cell{}, fmt.Errorf("%w: null holds %T", ErrValueType, value)
		}
		return cell{tag: tagNull}, nil
	case "integer":
		i, ok := value.(int64)
		if !ok {
			return cell{}, fmt.Errorf("%w: integer holds %T", ErrValueType, value)
		}
		return cell{tag: tagInteger, i: i}, nil
	case "real":
		f, ok := value.(float64)
		if !ok || math.IsNaN(f) {
			return cell{}, fmt.Errorf("%w: real holds %T %v", ErrValueType, value, value)
		}
		if f == 0 {
			f = 0
		}
		return cell{tag: tagReal, f: f}, nil
	case "text":
		var b []byte
		switch t := value.(type) {
		case string:
			b = []byte(t)
		case []byte:
			b = append([]byte(nil), t...)
		default:
			return cell{}, fmt.Errorf("%w: text holds %T", ErrValueType, value)
		}
		if !utf8.Valid(b) {
			return cell{}, ErrInvalidText
		}
		return cell{tag: tagText, bytes: b}, nil
	case "blob":
		var b []byte
		switch t := value.(type) {
		case []byte:
			b = append([]byte(nil), t...)
		case string:
			b = []byte(t)
		default:
			return cell{}, fmt.Errorf("%w: blob holds %T", ErrValueType, value)
		}
		return cell{tag: tagBlob, bytes: b}, nil
	}
	return cell{}, fmt.Errorf("%w: storage class %q", ErrValueType, name)
}

func (c cell) encode(w *bufio.Writer) {
	w.WriteByte(c.tag)
	var n [8]byte
	switch c.tag {
	case tagInteger:
		binary.BigEndian.PutUint64(n[:], uint64(c.i))
		w.Write(n[:])
	case tagReal:
		binary.BigEndian.PutUint64(n[:], math.Float64bits(c.f))
		w.Write(n[:])
	case tagText, tagBlob:
		binary.BigEndian.PutUint32(n[:4], uint32(len(c.bytes)))
		w.Write(n[:4])
		w.Write(c.bytes)
	}
}

// sameKey reports whether two keys tie under the engine's ordering: NULL ties
// NULL, numbers tie when equal as numbers whichever class holds them, and text
// or blobs tie when their bytes are equal.
func sameKey(a, b []cell) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameValue(a[i], b[i]) {
			return false
		}
	}
	return true
}

func sameValue(a, b cell) bool {
	numeric := func(c cell) bool { return c.tag == tagInteger || c.tag == tagReal }
	switch {
	case a.tag == tagNull || b.tag == tagNull:
		return a.tag == b.tag
	case numeric(a) && numeric(b):
		if a.tag == tagInteger && b.tag == tagInteger {
			return a.i == b.i
		}
		return asFloat(a) == asFloat(b)
	case a.tag != b.tag:
		return false
	}
	return bytes.Equal(a.bytes, b.bytes)
}

func asFloat(c cell) float64 {
	if c.tag == tagInteger {
		return float64(c.i)
	}
	return c.f
}
