package sqliteverify

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bbmumford/filekind"
)

// written makes a stamped Fixture file at generation 1 with one row, written
// through a read-write modernc connection that is closed before it returns,
// then renamed to name (modernc reads a '?' in a plain path as its query).
func written(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	plain := filepath.Join(dir, "written.db")
	db, err := sql.Open("sqlite", plain)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	for _, s := range []string{"CREATE TABLE t (k INTEGER PRIMARY KEY, v TEXT)", "INSERT INTO t (v) VALUES ('a')"} {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	if err := filekind.Stamp(ctx, db, filekind.Fixture, 1); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.Rename(plain, path); err != nil {
		t.Fatal(err)
	}
	return path
}

func size(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}

var want = filekind.Want{Kind: filekind.Fixture, Newest: 1}

func TestFileVerifiesAStampedFileAtItsBound(t *testing.T) {
	path := written(t, "a.db")
	h, err := File(context.Background(), path, want, size(t, path))
	if err != nil || h != (filekind.Header{ApplicationID: filekind.Fixture.ApplicationID(), UserVersion: 1}) {
		t.Fatalf("File = %+v, %v", h, err)
	}
	if _, err := File(context.Background(), path, filekind.Want{Kind: filekind.RunLedger, Newest: 1}, size(t, path)); !errors.Is(err, filekind.ErrWrongKind) {
		t.Fatalf("File of the wrong kind = %v, want ErrWrongKind", err)
	}
}

func TestFileRefusesAFileOverItsBoundBeforeOpeningIt(t *testing.T) {
	path := written(t, "a.db")
	if _, err := File(context.Background(), path, want, size(t, path)-1); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("File one byte over its bound = %v, want ErrTooLarge", err)
	}
	for _, bound := range []int64{0, -1} {
		if _, err := Open(path, bound); !errors.Is(err, filekind.ErrInvalidArgument) {
			t.Errorf("Open with bound %d = %v, want ErrInvalidArgument", bound, err)
		}
	}
}

func TestFileRefusesAPathThatIsNotARegularFile(t *testing.T) {
	if _, err := File(context.Background(), t.TempDir(), want, 1<<20); !errors.Is(err, ErrNotRegularFile) {
		t.Fatalf("File of a directory = %v, want ErrNotRegularFile", err)
	}
}

// Open's connection cannot write the file even with query_only turned off:
// the file is opened read-only, so a hot journal or a write never reaches it.
func TestOpenIsReadOnlyBeneathQueryOnly(t *testing.T) {
	path := written(t, "a.db")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := Open(path, size(t, path))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var on int64
	if err := conn.QueryRowContext(ctx, "PRAGMA query_only").Scan(&on); err != nil || on != 1 {
		t.Fatalf("query_only = %d, %v", on, err)
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA trusted_schema").Scan(&on); err != nil || on != 0 {
		t.Fatalf("trusted_schema = %d, %v", on, err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA query_only = 0"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "INSERT INTO t (v) VALUES ('b')"); err == nil {
		t.Fatal("a write with query_only off reached a file Open opened")
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("the file changed under Open's connection")
	}
}

// A path holding the characters an SQLite URI gives meaning to is opened as
// that path, not cut short or decoded into another one.
func TestFileOpensAPathWithURICharacters(t *testing.T) {
	path := written(t, "a%41?b#c.db")
	if _, err := File(context.Background(), path, want, size(t, path)); err != nil {
		t.Fatalf("File of %q = %v", filepath.Base(path), err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "aA")); !os.IsNotExist(err) {
		t.Fatalf("a file was made at the decoded path: %v", err)
	}
}
