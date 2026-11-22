package filekind

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCheckValuesRefusesABinaryNumberInABase3Kind(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name, schema, insert string
		outcome, detail      string
	}{
		{"text and blobs", "CREATE TABLE e (id INTEGER PRIMARY KEY, body TEXT, trits BLOB)",
			"INSERT INTO e VALUES (1, '{}', x'79')", "ok", ""},
		{"an integer in a blob column", "CREATE TABLE e (id INTEGER PRIMARY KEY, n BLOB)",
			"INSERT INTO e VALUES (1, 5)", "BinaryNumber", "e.n"},
		{"a real in an untyped column", "CREATE TABLE e (id INTEGER PRIMARY KEY, n)",
			"INSERT INTO e VALUES (1, 2.5)", "BinaryNumber", "e.n"},
		{"an integer key of a WITHOUT ROWID table", "CREATE TABLE e (id INTEGER PRIMARY KEY, v TEXT) WITHOUT ROWID",
			"INSERT INTO e VALUES (1, 'x')", "BinaryNumber", "e.id"},
		{"an INT primary key is not a rowid alias", "CREATE TABLE e (id INT PRIMARY KEY, v TEXT)",
			"INSERT INTO e VALUES (1, 'x')", "BinaryNumber", "e.id"},
		{"a composite integer key", "CREATE TABLE e (a INTEGER, b TEXT, PRIMARY KEY (a, b))",
			"INSERT INTO e VALUES (1, 'x')", "BinaryNumber", "e.a"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db, _ := newFile(t)
			exec(t, db, c.schema, c.insert)
			err := CheckValues(ctx, db, RunEvents)
			if got := outcomeName(err); got != c.outcome {
				t.Fatalf("CheckValues = %s (%v), want %s", got, err, c.outcome)
			}
			if c.detail != "" && !strings.HasSuffix(err.Error(), c.detail) {
				t.Fatalf("CheckValues names %v, want %s", err, c.detail)
			}
			if err := CheckValues(ctx, db, Fixture); err != nil {
				t.Fatalf("a standard kind was judged: %v", err)
			}
		})
	}
	db, _ := newFile(t)
	if err := CheckValues(ctx, db, 0xEE); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("unregistered kind: %v", err)
	}
}
