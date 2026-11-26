package filekind

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// openFile opens path through modernc SQLite with one connection, so a pragma
// and the statement after it meet the same connection.
func openFile(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// closeAtEnd closes c when the test ends and fails the test if closing fails.
func closeAtEnd(t *testing.T, c io.Closer) {
	t.Helper()
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
}

// newFile opens a new database file in a fresh temporary directory.
func newFile(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	return openFile(t, path), path
}

// fixture copies a golden-vector file into a temporary directory and opens the
// copy, so no test ever writes a fixture.
func fixture(t *testing.T, name string) (*sql.DB, string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "vectors", name))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return openFile(t, path), path
}

func exec(t *testing.T, db Querier, statements ...string) {
	t.Helper()
	for _, s := range statements {
		if err := run(context.Background(), db, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

// outcomeName names an error by the outcome word the golden vectors use.
func outcomeName(err error) string {
	if err == nil {
		return "ok"
	}
	for name, sentinel := range outcomes {
		if errors.Is(err, sentinel) {
			return name
		}
	}
	return "unnamed: " + err.Error()
}

var outcomes = map[string]error{
	"Unstamped": ErrUnstamped, "ForeignFile": ErrForeignFile, "UnknownKind": ErrUnknownKind,
	"WrongKind": ErrWrongKind, "NoGeneration": ErrNoGeneration, "NewerGeneration": ErrNewerGeneration,
	"OlderGeneration": ErrOlderGeneration, "GenerationBackwards": ErrGenerationBackwards,
	"StampNotHeld": ErrStampNotHeld, "CheckpointBusy": ErrCheckpointBusy,
	"UnreadableHeader": ErrUnreadableHeader, "NotPlainFile": ErrNotPlainFile,
	"InvalidArgument": ErrInvalidArgument, "NotBaseTable": ErrNotBaseTable, "KeyNotCovered": ErrKeyNotCovered, "KeyNotUnique": ErrKeyNotUnique,
	"ValueType": ErrValueType, "InvalidText": ErrInvalidText, "BinaryNumber": ErrBinaryNumber,
	"InvalidJSON": ErrInvalidJSON,
}
