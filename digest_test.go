package filekind

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
)

var exampleSet = RowSet{Kind: Fixture, Set: "example", Table: "example", Columns: []string{"k", "v"}, Key: []string{"k"}}

// exampleStream is SPEC.md's worked example: rows (1, 'a') and (2, NULL).
const exampleStream = "" +
	"66696c656b696e6420726f77732076310a" + // "filekind rows v1\n"
	"4f5242ff" + // application_id of the fixture kind
	"000000076578616d706c65" + // set "example"
	"000000076578616d706c65" + // table "example"
	"00000002" + "000000016b" + "0000000176" + // columns k, v
	"00000001" + "000000016b" + // key k
	"01" + "0000000000000001" + "03" + "00000001" + "61" + // 1, 'a'
	"01" + "0000000000000002" + "00" + // 2, NULL
	"05" + "0000000000000002" // end, two rows

func exampleTable(t *testing.T, db Querier, reverse bool) {
	t.Helper()
	exec(t, db, "CREATE TABLE example (k INTEGER PRIMARY KEY, v TEXT)")
	rows := []string{"INSERT INTO example VALUES (1, 'a')", "INSERT INTO example VALUES (2, NULL)"}
	if reverse {
		rows[0], rows[1] = rows[1], rows[0]
	}
	exec(t, db, rows...)
}

func TestEncodeRowsWritesTheSpecExample(t *testing.T) {
	db, _ := newFile(t)
	exampleTable(t, db, false)
	var buf bytes.Buffer
	n, err := EncodeRows(context.Background(), db, exampleSet, &buf)
	if err != nil || n != 2 {
		t.Fatalf("EncodeRows = %d, %v", n, err)
	}
	if got := hex.EncodeToString(buf.Bytes()); got != exampleStream {
		t.Fatalf("stream\n got %s\nwant %s", got, exampleStream)
	}
	d, err := DigestRows(context.Background(), db, exampleSet)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(buf.Bytes())
	if d.SHA256 != want || d.Rows != 2 || len(d.SHA256Hex()) != 64 || d.SHA256Hex() != strings.ToLower(d.SHA256Hex()) {
		t.Fatalf("digest %+v", d)
	}
}

// TestDigestFollowsTheKeyNotTheStorageOrder: rows whose rowids run against
// their key digest as if stored in key order. A digest of rows in whatever
// order the engine stores them fails here.
func TestDigestFollowsTheKeyNotTheStorageOrder(t *testing.T) {
	ctx := context.Background()
	set := RowSet{Kind: Fixture, Set: "s", Table: "t", Columns: []string{"v"}, Key: []string{"k"}, UnsignedKey: true}
	inOrder, _ := newFile(t)
	exec(t, inOrder, "CREATE TABLE t (k TEXT PRIMARY KEY, v TEXT)",
		"INSERT INTO t VALUES ('a', '1')", "INSERT INTO t VALUES ('b', '2')", "INSERT INTO t VALUES ('c', '3')")
	against, _ := newFile(t)
	exec(t, against, "CREATE TABLE t (k TEXT PRIMARY KEY, v TEXT)",
		"INSERT INTO t VALUES ('c', '3')", "INSERT INTO t VALUES ('a', '1')", "INSERT INTO t VALUES ('b', '2')")
	a, err := DigestRows(ctx, inOrder, set)
	if err != nil {
		t.Fatal(err)
	}
	b, err := DigestRows(ctx, against, set)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("same rows, other storage order: %s vs %s", a.SHA256Hex(), b.SHA256Hex())
	}
}

// TestDigestNeverReadsTheFileBytes: two files with the same rows and
// different bytes (another page size, another insertion order, vacuumed)
// digest the same.
func TestDigestNeverReadsTheFileBytes(t *testing.T) {
	ctx := context.Background()
	one, pathOne := newFile(t)
	exampleTable(t, one, false)
	two, pathTwo := newFile(t)
	exec(t, two, "PRAGMA page_size = 1024")
	exampleTable(t, two, true)
	exec(t, two, "CREATE TABLE unrelated (x TEXT)", "INSERT INTO unrelated VALUES ('padding')", "VACUUM")
	bytesOne, _ := os.ReadFile(pathOne)
	bytesTwo, _ := os.ReadFile(pathTwo)
	if bytes.Equal(bytesOne, bytesTwo) {
		t.Fatal("the two files should differ byte for byte")
	}
	a, err := DigestRows(ctx, one, exampleSet)
	if err != nil {
		t.Fatal(err)
	}
	b, err := DigestRows(ctx, two, exampleSet)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("same rows in two files digest differently")
	}
}

func TestDigestIsSeparatedByKindAndSet(t *testing.T) {
	ctx := context.Background()
	db, _ := newFile(t)
	exampleTable(t, db, false)
	base, err := DigestRows(ctx, db, exampleSet)
	if err != nil {
		t.Fatal(err)
	}
	for _, other := range []RowSet{
		{Kind: RunEvents, Set: "example", Table: "example", Columns: []string{"k", "v"}, Key: []string{"k"}},
		{Kind: Fixture, Set: "example2", Table: "example", Columns: []string{"k", "v"}, Key: []string{"k"}},
		{Kind: Fixture, Set: "example", Table: "example", Columns: []string{"v", "k"}, Key: []string{"k"}},
	} {
		d, err := DigestRows(ctx, db, other)
		if err != nil {
			t.Fatal(err)
		}
		if d.SHA256 == base.SHA256 || d.BLAKE3 == base.BLAKE3 {
			t.Fatalf("row set %+v shares the example's digest", other)
		}
	}
}

// TestDigestTakesTheStorageClassFromTheEngine: a DATETIME-declared TEXT is
// text whatever the driver would make of the declaration, and a stored -0.0
// is +0.0 in the stream.
func TestDigestTakesTheStorageClassFromTheEngine(t *testing.T) {
	db, _ := newFile(t)
	exec(t, db, "CREATE TABLE t (k INTEGER PRIMARY KEY, d DATETIME, r REAL, n)",
		"INSERT INTO t VALUES (1, '2026-11-21 10:00:00', -0.0, -0.0)")
	var buf bytes.Buffer
	set := RowSet{Kind: Fixture, Set: "s", Table: "t", Columns: []string{"d", "r", "n"}, Key: []string{"k"}, UnsignedKey: true}
	if _, err := EncodeRows(context.Background(), db, set, &buf); err != nil {
		t.Fatal(err)
	}
	s := buf.Bytes()
	if !bytes.Contains(s, append([]byte{tagText, 0, 0, 0, 19}, "2026-11-21 10:00:00"...)) {
		t.Fatal("the DATETIME text is not encoded as its text")
	}
	if bytes.Contains(s, []byte{tagReal, 0x80, 0, 0, 0, 0, 0, 0, 0}) {
		t.Fatal("a negative zero reached the stream")
	}
	if !bytes.Contains(s, []byte{tagReal, 0, 0, 0, 0, 0, 0, 0, 0, tagReal}) {
		t.Fatal("the zeros are not encoded as positive REAL zeros")
	}
}

func TestReadCellCanonicalisesNegativeZero(t *testing.T) {
	negative, err := readCell("real", negZero())
	if err != nil {
		t.Fatal(err)
	}
	positive, err := readCell("real", 0.0)
	if err != nil {
		t.Fatal(err)
	}
	var a, b bytes.Buffer
	wa, wb := streamTo(&a), streamTo(&b)
	negative.encode(wa)
	positive.encode(wb)
	if err := errors.Join(wa.flush(), wb.flush()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatalf("-0.0 encodes as %x, +0.0 as %x", a.Bytes(), b.Bytes())
	}
}

func TestReadCellRefusesAMismatch(t *testing.T) {
	cases := []struct {
		class   string
		value   any
		outcome error
	}{
		{"integer", "1", ErrValueType},
		{"real", int64(1), ErrValueType},
		{"real", nan(), ErrValueType},
		{"null", int64(0), ErrValueType},
		{"text", int64(0), ErrValueType},
		{"blob", 1.5, ErrValueType},
		{"date", "x", ErrValueType},
		{"text", string([]byte{0xc3}), ErrInvalidText},
		{"text", []byte{0xff, 0xfe}, ErrInvalidText},
	}
	for _, c := range cases {
		if _, err := readCell(c.class, c.value); !errors.Is(err, c.outcome) {
			t.Errorf("readCell(%q, %#v) = %v, want %v", c.class, c.value, err, c.outcome)
		}
	}
}

func TestDigestRefusesKeysThatTie(t *testing.T) {
	ctx := context.Background()
	db, _ := newFile(t)
	exec(t, db, "CREATE TABLE d (k, v TEXT)",
		"INSERT INTO d VALUES ('p', 'one')", "INSERT INTO d VALUES ('p', 'two')",
		"CREATE TABLE m (k, v TEXT)", "INSERT INTO m VALUES (1, 'int')", "INSERT INTO m VALUES (1.0, 'real')",
		"CREATE TABLE n (k, v TEXT)", "INSERT INTO n VALUES (NULL, 'a')", "INSERT INTO n VALUES (NULL, 'b')",
		"CREATE TABLE u (k, v TEXT)", "INSERT INTO u VALUES ('p', 'one')", "INSERT INTO u VALUES ('P', 'two')")
	for _, table := range []string{"d", "m", "n"} {
		set := RowSet{Kind: Fixture, Set: table, Table: table, Columns: []string{"v"}, Key: []string{"k"}, UnsignedKey: true}
		if _, err := DigestRows(ctx, db, set); !errors.Is(err, ErrKeyNotUnique) {
			t.Errorf("table %s: %v, want ErrKeyNotUnique", table, err)
		}
	}
	// 'p' and 'P' are distinct keys under BINARY collation.
	set := RowSet{Kind: Fixture, Set: "u", Table: "u", Columns: []string{"v"}, Key: []string{"k"}, UnsignedKey: true}
	if _, err := DigestRows(ctx, db, set); err != nil {
		t.Errorf("table u: %v", err)
	}
}

func TestDigestRefusesWhatIsNotABaseTable(t *testing.T) {
	ctx := context.Background()
	db, _ := newFile(t)
	exampleTable(t, db, false)
	exec(t, db, "CREATE VIEW v AS SELECT * FROM example")
	for _, table := range []string{"v", "missing", "sqlite_schema"} {
		set := RowSet{Kind: Fixture, Set: "s", Table: table, Columns: []string{"v"}, Key: []string{"k"}, UnsignedKey: true}
		if _, err := DigestRows(ctx, db, set); !errors.Is(err, ErrNotBaseTable) {
			t.Errorf("table %s: %v, want ErrNotBaseTable", table, err)
		}
	}
	// A base table is found whatever the case of its ASCII name.
	upper := exampleSet
	upper.Table = "EXAMPLE"
	if _, err := DigestRows(ctx, db, upper); err != nil {
		t.Errorf("EXAMPLE: %v", err)
	}
}

func TestRowSetRefusesAMalformedSet(t *testing.T) {
	db, _ := newFile(t)
	exampleTable(t, db, false)
	for _, s := range []RowSet{
		{Kind: 0xEE, Set: "s", Table: "example", Columns: []string{"v"}, Key: []string{"k"}},
		{Kind: Fixture, Set: "", Table: "example", Columns: []string{"v"}, Key: []string{"k"}},
		{Kind: Fixture, Set: "s", Table: "", Columns: []string{"v"}, Key: []string{"k"}},
		{Kind: Fixture, Set: "s", Table: "example", Key: []string{"k"}},
		{Kind: Fixture, Set: "s", Table: "example", Columns: []string{"v"}},
		{Kind: Fixture, Set: "s", Table: "example", Columns: []string{"v\x00"}, Key: []string{"k"}},
		{Kind: Fixture, Set: string([]byte{0xff}), Table: "example", Columns: []string{"v"}, Key: []string{"k"}},
	} {
		if _, err := DigestRows(context.Background(), db, s); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("%+v: %v, want ErrInvalidArgument", s, err)
		}
	}
}

func TestDigestWhereSelectsRows(t *testing.T) {
	ctx := context.Background()
	db, _ := newFile(t)
	exampleTable(t, db, false)
	set := exampleSet
	set.Where, set.Args = "k > ?", []any{1}
	d, err := DigestRows(ctx, db, set)
	if err != nil || d.Rows != 1 {
		t.Fatalf("filtered digest = %+v, %v", d, err)
	}
}
