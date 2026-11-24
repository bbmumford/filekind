// Package enginetest runs the Go helper against the golden vectors through the
// Turso engine (tursogo), the engine that writes local single-process files,
// so the helper is shown to read the same headers and digest the same rows on
// it as on modernc SQLite and on Python's sqlite3. It is its own module so the
// filekind module requires no engine.
package enginetest

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bbmumford/filekind"
	_ "turso.tech/database/tursogo"
)

type vectors struct {
	Headers []struct {
		File   string `json:"file"`
		Header struct {
			ApplicationID int32 `json:"applicationId"`
			UserVersion   int32 `json:"userVersion"`
		} `json:"header"`
		Checks []struct {
			Kind    string `json:"kind"`
			Newest  int32  `json:"newest"`
			Oldest  int32  `json:"oldest"`
			Outcome string `json:"outcome"`
		} `json:"checks"`
	} `json:"headers"`
	Digests []struct {
		File   string `json:"file"`
		RowSet struct {
			Kind    string   `json:"kind"`
			Set     string   `json:"set"`
			Table   string   `json:"table"`
			Columns []string `json:"columns"`
			Key     []string `json:"key"`
			Where   string   `json:"where"`
			Args    []int64  `json:"args"`
		} `json:"rowSet"`
		StreamHex string `json:"streamHex"`
		SHA256    string `json:"sha256"`
		BLAKE3    string `json:"blake3"`
		Rows      uint64 `json:"rows"`
		Outcome   string `json:"outcome"`
	} `json:"digests"`
	Values []struct {
		File    string `json:"file"`
		Kind    string `json:"kind"`
		Outcome string `json:"outcome"`
	} `json:"values"`
}

var outcomes = map[string]error{
	"Unstamped": filekind.ErrUnstamped, "ForeignFile": filekind.ErrForeignFile,
	"UnknownKind": filekind.ErrUnknownKind, "WrongKind": filekind.ErrWrongKind,
	"NoGeneration": filekind.ErrNoGeneration, "NewerGeneration": filekind.ErrNewerGeneration,
	"OlderGeneration": filekind.ErrOlderGeneration, "InvalidArgument": filekind.ErrInvalidArgument,
	"NotBaseTable": filekind.ErrNotBaseTable, "KeyNotUnique": filekind.ErrKeyNotUnique,
	"ValueType": filekind.ErrValueType, "InvalidText": filekind.ErrInvalidText,
	"BinaryNumber": filekind.ErrBinaryNumber,
}

func outcome(err error) string {
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

func load(t *testing.T) vectors {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "testdata", "vectors", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func open(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("turso", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// fixture opens a copy of a golden-vector file through tursogo.
func fixture(t *testing.T, name string) *sql.DB {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "testdata", "vectors", name))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return open(t, path)
}

func kind(t *testing.T, name string) filekind.Kind {
	t.Helper()
	e, ok := filekind.ByName(name)
	if !ok {
		t.Fatalf("unregistered kind %q", name)
	}
	return e.Kind
}

func TestTursoHeaders(t *testing.T) {
	ctx := context.Background()
	for _, v := range load(t).Headers {
		if v.File == "empty.db" {
			continue // an empty file is the engine's new database; TestTursoStampAndCreate covers it
		}
		db := fixture(t, v.File)
		want := filekind.Header{ApplicationID: v.Header.ApplicationID, UserVersion: v.Header.UserVersion}
		if h, err := filekind.Read(ctx, db); err != nil || h != want {
			t.Errorf("%s: Read = %+v, %v, want %+v", v.File, h, err, want)
		}
		for _, c := range v.Checks {
			_, err := filekind.Check(ctx, db, filekind.Want{Kind: kind(t, c.Kind), Newest: c.Newest, Oldest: c.Oldest})
			if got := outcome(err); got != c.Outcome {
				t.Errorf("%s: Check(%s) = %s, want %s", v.File, c.Kind, got, c.Outcome)
			}
		}
	}
}

func TestTursoDigests(t *testing.T) {
	ctx := context.Background()
	for i, v := range load(t).Digests {
		db := fixture(t, v.File)
		args := make([]any, len(v.RowSet.Args))
		for j, a := range v.RowSet.Args {
			args[j] = a
		}
		set := filekind.RowSet{Kind: kind(t, v.RowSet.Kind), Set: v.RowSet.Set, Table: v.RowSet.Table,
			Columns: v.RowSet.Columns, Key: v.RowSet.Key, Where: v.RowSet.Where, Args: args}
		label := fmt.Sprintf("digest %d (%s %s)", i, v.File, v.RowSet.Set)
		var buf bytes.Buffer
		_, encErr := filekind.EncodeRows(ctx, db, set, &buf)
		d, err := filekind.DigestRows(ctx, db, set)
		if v.Outcome == "InvalidText" {
			// The Turso engine refuses to return TEXT that is not UTF-8 at
			// all, reporting the file corrupt, so the refusal is the engine's
			// error rather than the helper's.
			for _, e := range []error{err, encErr} {
				if e == nil || !(errors.Is(e, filekind.ErrInvalidText) || strings.Contains(e.Error(), "invalid UTF-8")) {
					t.Errorf("%s: %v, want a refusal of the invalid text", label, e)
				}
			}
			continue
		}
		if v.Outcome != "" {
			if outcome(err) != v.Outcome || outcome(encErr) != v.Outcome {
				t.Errorf("%s: %s / %s, want %s", label, outcome(err), outcome(encErr), v.Outcome)
			}
			continue
		}
		if err != nil || encErr != nil {
			t.Errorf("%s: %v / %v", label, err, encErr)
			continue
		}
		if v.StreamHex != "" && hex.EncodeToString(buf.Bytes()) != v.StreamHex {
			t.Errorf("%s: stream\n got %x\nwant %s", label, buf.Bytes(), v.StreamHex)
		}
		if d.SHA256Hex() != v.SHA256 || d.BLAKE3Hex() != v.BLAKE3 || d.Rows != v.Rows {
			t.Errorf("%s: %s %d, want %s %d", label, d.SHA256Hex(), d.Rows, v.SHA256, v.Rows)
		}
	}
}

func TestTursoValues(t *testing.T) {
	for _, v := range load(t).Values {
		db := fixture(t, v.File)
		if got := outcome(filekind.CheckValues(context.Background(), db, kind(t, v.Kind))); got != v.Outcome {
			t.Errorf("%s: CheckValues = %s, want %s", v.File, got, v.Outcome)
		}
	}
}

// TestTursoStampAndCreate: a tursogo file is WAL from its first write, so the
// header reaches the file's bytes only through the checkpoint.
func TestTursoStampAndCreate(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "new.db")
	db := open(t, path)
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	h, err := filekind.Create(ctx, conn, filekind.Fixture, 1, []string{"CREATE TABLE t (k TEXT PRIMARY KEY, v BLOB)"})
	if err != nil || h != (filekind.Header{ApplicationID: filekind.Fixture.ApplicationID(), UserVersion: 1}) {
		t.Fatalf("Create = %+v, %v", h, err)
	}
	if fh, err := filekind.ReadFileHeader(path); err != nil || fh != h {
		t.Fatalf("file header after Create = %+v, %v", fh, err)
	}
	if _, err := filekind.Create(ctx, conn, filekind.Fixture, 1, nil); err != nil {
		t.Fatalf("second Create = %v", err)
	}
	if err := filekind.SetHeader(ctx, conn, filekind.Fixture, 2); err != nil {
		t.Fatal(err)
	}
	if fh, _ := filekind.ReadFileHeader(path); fh.UserVersion != 1 {
		t.Fatalf("before the checkpoint the file shows generation %d", fh.UserVersion)
	}
	if err := filekind.Stamp(ctx, conn, filekind.Fixture, 3); err != nil {
		t.Fatal(err)
	}
	if fh, err := filekind.ReadFileHeader(path); err != nil || fh.UserVersion != 3 {
		t.Fatalf("after Stamp = %+v, %v", fh, err)
	}
	if err := filekind.Stamp(ctx, conn, filekind.Fixture, 2); !errors.Is(err, filekind.ErrGenerationBackwards) {
		t.Fatalf("lowering the generation = %v", err)
	}
}

// TestTursoVerify: the Turso engine answers quick_check on an intact file with
// the one row "ok", so Verify passes it and judges its header.
func TestTursoVerify(t *testing.T) {
	db := fixture(t, "rows.db")
	if h, err := filekind.Verify(context.Background(), db, filekind.Want{Kind: filekind.Fixture, Newest: 1}); err != nil || h.UserVersion != 1 {
		t.Fatalf("Verify on tursogo = %+v, %v", h, err)
	}
	if _, err := filekind.Verify(context.Background(), db, filekind.Want{Kind: filekind.RunLedger, Newest: 2}); !errors.Is(err, filekind.ErrWrongKind) {
		t.Fatalf("Verify of the wrong kind on tursogo = %v", err)
	}
}
