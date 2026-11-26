package filekind

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
)

// damagedCopy writes a stamped file whose third page is overwritten, so its
// header is intact and its b-tree is not.
func damagedCopy(t *testing.T) string {
	t.Helper()
	db, path := newFile(t)
	exec(t, db, "PRAGMA page_size = 1024", "CREATE TABLE t (k INTEGER PRIMARY KEY, v TEXT)")
	for i := 0; i < 40; i++ {
		exec(t, db, "INSERT INTO t (v) VALUES ('"+string(bytes.Repeat([]byte("x"), 200))+"')")
	}
	if err := Stamp(context.Background(), db, Fixture, 1); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 4*1024 {
		t.Fatalf("file of %d bytes is too small to damage a page", len(data))
	}
	copy(data[2*1024:3*1024], bytes.Repeat([]byte{0xff}, 1024))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVerifyRefusesADamagedFileWithAnIntactHeader(t *testing.T) {
	path := damagedCopy(t)
	if h, err := ReadFileHeader(path); err != nil || h != (Header{Fixture.ApplicationID(), 1}) {
		t.Fatalf("the damaged file's header = %+v, %v", h, err)
	}
	db := openForVerify(t, path)
	if _, err := Check(context.Background(), db, Want{Kind: Fixture, Newest: 1}); err != nil {
		t.Fatalf("Check alone passes the damaged file's header: %v", err)
	}
	if _, err := Verify(context.Background(), db, Want{Kind: Fixture, Newest: 1}); err == nil {
		t.Fatal("Verify passed a damaged file")
	}
}

func TestVerifyPassesAnIntactFileAndJudgesItsHeader(t *testing.T) {
	ctx := context.Background()
	_, path := fixture(t, "rows.db")
	db := openForVerify(t, path)
	if h, err := Verify(ctx, db, Want{Kind: Fixture, Newest: 1}); err != nil || h.UserVersion != 1 {
		t.Fatalf("Verify of an intact fixture = %+v, %v", h, err)
	}
	if _, err := Verify(ctx, db, Want{Kind: RunLedger, Newest: 2}); !errors.Is(err, ErrWrongKind) {
		t.Fatalf("Verify of the wrong kind = %v", err)
	}
	if _, err := Verify(ctx, db, Want{Kind: 0xEE, Newest: 1}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Verify of an unregistered expectation = %v", err)
	}
}

// TestVerifyNamesWhatQuickCheckReports: a file whose pages read but whose rows
// break a constraint (a NULL kept under a column the schema now declares NOT
// NULL) makes quick_check answer a row other than "ok", and Verify refuses it
// as ErrNotIntact.
func TestVerifyNamesWhatQuickCheckReports(t *testing.T) {
	ctx := context.Background()
	db, path := newFile(t)
	exec(t, db, "CREATE TABLE t (a TEXT)", "INSERT INTO t VALUES (NULL)", "PRAGMA writable_schema = ON",
		"UPDATE sqlite_schema SET sql = 'CREATE TABLE t (a TEXT NOT NULL)' WHERE name = 't'", "PRAGMA writable_schema = OFF")
	if err := Stamp(ctx, db, Fixture, 1); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openForVerify(t, path)
	if _, err := Verify(ctx, reopened, Want{Kind: Fixture, Newest: 1}); !errors.Is(err, ErrNotIntact) {
		t.Fatalf("Verify of a file quick_check reports on = %v, want ErrNotIntact", err)
	}
}
