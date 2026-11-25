package filekind

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func stampRaw(t *testing.T, db Querier, app int64, version int64) {
	t.Helper()
	exec(t, db, fmt.Sprintf("PRAGMA application_id = %d", app), fmt.Sprintf("PRAGMA user_version = %d", version))
}

// TestCheckNamesEveryRefusal: each header earns the one refusal its fields
// call for, and a header that holds the expected kind at a readable
// generation passes.
func TestCheckNamesEveryRefusal(t *testing.T) {
	ctx := context.Background()
	fixtureID := int64(Fixture.ApplicationID())
	cases := []struct {
		name         string
		app, version int64
		want         Want
		outcome      string
	}{
		{"matching kind and generation", fixtureID, 1, Want{Kind: Fixture, Newest: 1}, "ok"},
		{"older generation than newest", fixtureID, 1, Want{Kind: Fixture, Newest: 3}, "ok"},
		{"generation at newest", fixtureID, 3, Want{Kind: Fixture, Newest: 3}, "ok"},
		{"generation at oldest", fixtureID, 2, Want{Kind: Fixture, Newest: 3, Oldest: 2}, "ok"},
		{"unstamped", 0, 0, Want{Kind: Fixture, Newest: 1}, "Unstamped"},
		{"user_version without kind", 0, 4, Want{Kind: Fixture, Newest: 1}, "Unstamped"},
		{"another application", 0x12345678, 1, Want{Kind: Fixture, Newest: 1}, "ForeignFile"},
		{"negative application_id", -1, 1, Want{Kind: Fixture, Newest: 1}, "ForeignFile"},
		{"unregistered kind byte", 0x4F5242EE, 1, Want{Kind: Fixture, Newest: 1}, "UnknownKind"},
		{"another registered kind", int64(RunLedger.ApplicationID()), 1, Want{Kind: Fixture, Newest: 1}, "WrongKind"},
		{"no generation", fixtureID, 0, Want{Kind: Fixture, Newest: 1}, "NoGeneration"},
		{"negative generation", fixtureID, -2, Want{Kind: Fixture, Newest: 1}, "NoGeneration"},
		{"newer generation", fixtureID, 2, Want{Kind: Fixture, Newest: 1}, "NewerGeneration"},
		{"older than oldest", fixtureID, 1, Want{Kind: Fixture, Newest: 3, Oldest: 2}, "OlderGeneration"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db, _ := newFile(t)
			stampRaw(t, db, c.app, c.version)
			h, err := Check(ctx, db, c.want)
			if got := outcomeName(err); got != c.outcome {
				t.Fatalf("Check = %s, want %s", got, c.outcome)
			}
			if h != (Header{ApplicationID: int32(c.app), UserVersion: int32(c.version)}) {
				t.Fatalf("Check read %+v", h)
			}
			var he *HeaderError
			if err != nil && (!errors.As(err, &he) || he.Found != h || he.Want != c.want) {
				t.Fatalf("refusal %v does not carry the header and the expectation", err)
			}
		})
	}
}

func TestCheckRefusesAnExpectationNoFileMeets(t *testing.T) {
	db, _ := newFile(t)
	for _, w := range []Want{
		{Kind: 0xEE, Newest: 1},
		{Kind: 0, Newest: 1},
		{Kind: Fixture, Newest: 0},
		{Kind: Fixture, Newest: 2, Oldest: 3},
		{Kind: Fixture, Newest: 2, Oldest: -1},
	} {
		if _, err := Check(context.Background(), db, w); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("Check(%+v) = %v, want ErrInvalidArgument", w, err)
		}
	}
}

// stampsBetweenStatements lets another connection commit a whole stamp after
// the first statement a reader sends through it, the moment a racing creator
// can.
type stampsBetweenStatements struct {
	t          *testing.T
	reader     Querier
	writer     *sql.DB
	statements int
}

func (s *stampsBetweenStatements) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if s.statements == 1 {
		exec(s.t, s.writer, "BEGIN IMMEDIATE",
			fmt.Sprintf("PRAGMA application_id = %d", Fixture.ApplicationID()), "PRAGMA user_version = 1", "COMMIT")
	}
	s.statements++
	return s.reader.QueryContext(ctx, query, args...)
}

// A reader racing a creator reads the header from before its commit or from
// after it, never one field of each.
func TestReadNeverReadsHalfAStamp(t *testing.T) {
	ctx := context.Background()
	reader, path := newFile(t)
	writer := openFile(t, path)
	h, err := Read(ctx, &stampsBetweenStatements{t: t, reader: reader, writer: writer})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if h != (Header{}) && h != (Header{ApplicationID: Fixture.ApplicationID(), UserVersion: 1}) {
		t.Fatalf("Read = %+v: one field from before a stamp and one from after it", h)
	}
}

func TestReadFileHeader(t *testing.T) {
	db, path := newFile(t)
	exec(t, db, "CREATE TABLE t (k INTEGER PRIMARY KEY)")
	stampRaw(t, db, int64(AuditExport.ApplicationID()), 7)
	h, err := ReadFileHeader(path)
	if err != nil || h != (Header{ApplicationID: AuditExport.ApplicationID(), UserVersion: 7}) {
		t.Fatalf("ReadFileHeader = %+v, %v", h, err)
	}

	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.db")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if h, err := ReadFileHeader(empty); err != nil || h != (Header{}) {
		t.Fatalf("empty file: %+v, %v", h, err)
	}
	short := filepath.Join(dir, "short.db")
	if err := os.WriteFile(short, []byte("SQLite format 3\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFileHeader(short); !errors.Is(err, ErrNotPlainFile) {
		t.Fatalf("short file: %v", err)
	}
	other := filepath.Join(dir, "other.db")
	if err := os.WriteFile(other, append([]byte("Turso\x00\x02"), make([]byte, 200)...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFileHeader(other); !errors.Is(err, ErrNotPlainFile) {
		t.Fatalf("encrypted magic: %v", err)
	}
}
