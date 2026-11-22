package filekind

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestStampPutsTheHeaderInTheFile: in WAL mode a header just set sits in the
// WAL, so the engine reads it and the file's bytes do not; Stamp's checkpoint
// is what moves it into the file.
func TestStampPutsTheHeaderInTheFile(t *testing.T) {
	ctx := context.Background()
	db, path := newFile(t)
	exec(t, db, "PRAGMA journal_mode = WAL", "CREATE TABLE t (k INTEGER PRIMARY KEY)")
	if err := Checkpoint(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := SetHeader(ctx, db, RunEvents, 1); err != nil {
		t.Fatal(err)
	}
	if h, err := Read(ctx, db); err != nil || h != (Header{RunEvents.ApplicationID(), 1}) {
		t.Fatalf("engine reads %+v, %v", h, err)
	}
	if h, err := ReadFileHeader(path); err != nil || h != (Header{}) {
		t.Fatalf("before the checkpoint the file holds %+v, %v; the WAL should still hold the stamp", h, err)
	}
	if err := Stamp(ctx, db, RunEvents, 2); err != nil {
		t.Fatal(err)
	}
	if h, err := ReadFileHeader(path); err != nil || h != (Header{RunEvents.ApplicationID(), 2}) {
		t.Fatalf("after Stamp the file holds %+v, %v", h, err)
	}
}

func TestSetHeaderRefusesToTakeOverAFile(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name         string
		app, version int64
		kind         Kind
		generation   int32
		outcome      string
	}{
		{"new file", 0, 0, Fixture, 1, "ok"},
		{"same generation again", int64(Fixture.ApplicationID()), 1, Fixture, 1, "ok"},
		{"next generation", int64(Fixture.ApplicationID()), 1, Fixture, 2, "ok"},
		{"lower generation", int64(Fixture.ApplicationID()), 3, Fixture, 2, "GenerationBackwards"},
		{"another kind", int64(RunLedger.ApplicationID()), 1, Fixture, 1, "WrongKind"},
		{"unregistered kind", 0x4F5242EE, 1, Fixture, 1, "UnknownKind"},
		{"another application", 0x12345678, 0, Fixture, 1, "ForeignFile"},
		{"another scheme's user_version", 0, 5, Fixture, 1, "ForeignFile"},
		{"unregistered kind asked", 0, 0, 0xEE, 1, "InvalidArgument"},
		{"generation zero asked", 0, 0, Fixture, 0, "InvalidArgument"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db, _ := newFile(t)
			stampRaw(t, db, c.app, c.version)
			err := SetHeader(ctx, db, c.kind, c.generation)
			if got := outcomeName(err); got != c.outcome {
				t.Fatalf("SetHeader = %s, want %s", got, c.outcome)
			}
			h, rerr := Read(ctx, db)
			if rerr != nil {
				t.Fatal(rerr)
			}
			if err == nil && h != (Header{c.kind.ApplicationID(), c.generation}) {
				t.Fatalf("stamp did not hold: %+v", h)
			}
			if err != nil && h != (Header{int32(c.app), int32(c.version)}) {
				t.Fatalf("a refused stamp changed the header to %+v", h)
			}
		})
	}
}

func TestSetHeaderRollsBackWithItsTransaction(t *testing.T) {
	ctx := context.Background()
	db, _ := newFile(t)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := SetHeader(ctx, tx, Fixture, 1); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if h, err := Read(ctx, db); err != nil || h != (Header{}) {
		t.Fatalf("after rollback %+v, %v", h, err)
	}
}

// TestCheckpointRefusesWhenAReaderHoldsTheWAL: a reader in a transaction keeps
// a TRUNCATE checkpoint from finishing, and Checkpoint says so instead of
// reporting a header on disk that is not there.
func TestCheckpointRefusesWhenAReaderHoldsTheWAL(t *testing.T) {
	ctx := context.Background()
	writer, path := newFile(t)
	exec(t, writer, "PRAGMA journal_mode = WAL", "CREATE TABLE t (k INTEGER PRIMARY KEY)", "INSERT INTO t VALUES (1)")
	reader := openFile(t, path)
	tx, err := reader.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatal(err)
	}
	exec(t, writer, "INSERT INTO t VALUES (2)")
	if err := Checkpoint(ctx, writer); !errors.Is(err, ErrCheckpointBusy) {
		t.Fatalf("Checkpoint with a reader open = %v, want ErrCheckpointBusy", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := Checkpoint(ctx, writer); err != nil {
		t.Fatalf("Checkpoint after the reader left = %v", err)
	}
}

// TestCreateRunsOnceUnderARace: writers that race to create one file all
// succeed, the schema is created once, and the header is the kind asked.
func TestCreateRunsOnceUnderARace(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "race.db")
	schema := []string{"CREATE TABLE log (k TEXT PRIMARY KEY, v TEXT)"}
	const writers = 8
	var wg sync.WaitGroup
	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(10000)")
			if err != nil {
				errs[i] = err
				return
			}
			defer db.Close()
			conn, err := db.Conn(ctx)
			if err != nil {
				errs[i] = err
				return
			}
			defer conn.Close()
			_, errs[i] = Create(ctx, conn, Fixture, 1, schema)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
	}
	db := openFile(t, path)
	if h, err := Check(ctx, db, Want{Kind: Fixture, Newest: 1}); err != nil {
		t.Fatalf("Check after the race: %+v, %v", h, err)
	}
	if n, err := schemaObjects(ctx, db); err != nil || n != 2 { // the table and its primary-key index
		t.Fatalf("schema objects = %d, %v", n, err)
	}
}

func TestCreateChecksAFileItDoesNotMake(t *testing.T) {
	ctx := context.Background()
	db, _ := newFile(t)
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := Create(ctx, conn, Fixture, 1, []string{"CREATE TABLE t (k INTEGER PRIMARY KEY)"}); err != nil {
		t.Fatal(err)
	}
	if h, err := Create(ctx, conn, Fixture, 1, []string{"CREATE TABLE t (k INTEGER PRIMARY KEY)"}); err != nil || h.UserVersion != 1 {
		t.Fatalf("second Create = %+v, %v", h, err)
	}
	if _, err := Create(ctx, conn, Fixture, 2, nil); !errors.Is(err, ErrOlderGeneration) {
		t.Fatalf("Create at a later generation over generation 1 = %v, want ErrOlderGeneration", err)
	}
	if _, err := Create(ctx, conn, RunEvents, 1, nil); !errors.Is(err, ErrWrongKind) {
		t.Fatalf("Create of another kind = %v, want ErrWrongKind", err)
	}

	other, _ := newFile(t)
	exec(t, other, "CREATE TABLE existing (k INTEGER PRIMARY KEY)")
	oc, err := other.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer oc.Close()
	if _, err := Create(ctx, oc, Fixture, 1, nil); !errors.Is(err, ErrUnstamped) {
		t.Fatalf("Create over an unstamped file with a schema = %v, want ErrUnstamped", err)
	}
	if h, err := Read(ctx, oc); err != nil || h != (Header{}) {
		t.Fatalf("a refused Create changed the header to %+v, %v", h, err)
	}
}

// droppingQuerier turns every application_id assignment into a statement that
// does nothing, standing for an engine that accepts the stamp and loses it.
type droppingQuerier struct{ Querier }

func (d droppingQuerier) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if strings.HasPrefix(query, "PRAGMA application_id = ") {
		query = "SELECT 1"
	}
	return d.Querier.QueryContext(ctx, query, args...)
}

func TestSetHeaderRefusesAStampTheEngineDrops(t *testing.T) {
	db, _ := newFile(t)
	err := SetHeader(context.Background(), droppingQuerier{db}, Fixture, 1)
	if !errors.Is(err, ErrStampNotHeld) {
		t.Fatalf("SetHeader through an engine that drops the stamp = %v, want ErrStampNotHeld", err)
	}
}

// TestCreateWaitsForACreatorHoldingTheWriteLock: while another writer holds
// the write lock and is part way through creating the file, Create waits for
// it rather than reading the file as empty, then finds the file made and
// checks it. A Create that read the header before taking the lock would plan
// a second creation and fail when the first commits.
func TestCreateWaitsForACreatorHoldingTheWriteLock(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "held.db")
	dsn := path + "?_pragma=busy_timeout(10000)"
	other, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	holder, err := other.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	exec(t, holder, "BEGIN IMMEDIATE")

	done := make(chan error, 1)
	go func() {
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			done <- err
			return
		}
		defer db.Close()
		conn, err := db.Conn(ctx)
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		_, err = Create(ctx, conn, Fixture, 1, []string{"CREATE TABLE log (k TEXT PRIMARY KEY)"})
		done <- err
	}()
	time.Sleep(300 * time.Millisecond)
	stampRaw(t, holder, int64(Fixture.ApplicationID()), 1)
	exec(t, holder, "CREATE TABLE log (k TEXT PRIMARY KEY)", "COMMIT")
	if err := <-done; err != nil {
		t.Fatalf("Create beside a creator holding the write lock: %v", err)
	}
}
