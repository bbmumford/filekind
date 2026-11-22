package filekind

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// SetHeader writes kind's application_id and generation into the header and
// reads both back. It works inside a transaction, where the write commits or
// rolls back with the rest; the value then sits in the WAL until Checkpoint.
//
// It refuses to take over a file: one stamped with another kind, one outside
// the 'ORB' prefix, and one whose user_version some other scheme set while the
// application_id stayed zero. It refuses to lower a generation. Stamping the
// generation a file already holds is a no-op that still reads back.
func SetHeader(ctx context.Context, q Querier, kind Kind, generation int32) error {
	want := Want{Kind: kind, Newest: generation}
	if err := want.valid(); err != nil {
		return err
	}
	h, err := Read(ctx, q)
	if err != nil {
		return err
	}
	refuse := func(err error) error { return &HeaderError{Err: err, Found: h, Want: want} }
	if h != (Header{}) {
		found, ok := h.Kind()
		switch {
		case h.ApplicationID == 0 || !ok:
			return refuse(ErrForeignFile)
		case found != kind:
			if _, registered := Lookup(found); !registered {
				return refuse(ErrUnknownKind)
			}
			return refuse(ErrWrongKind)
		case h.UserVersion > generation:
			return refuse(ErrGenerationBackwards)
		}
	}
	if err := run(ctx, q, "PRAGMA application_id = "+strconv.FormatInt(int64(kind.ApplicationID()), 10)); err != nil {
		return err
	}
	if err := run(ctx, q, "PRAGMA user_version = "+strconv.FormatInt(int64(generation), 10)); err != nil {
		return err
	}
	got, err := Read(ctx, q)
	if err != nil {
		return err
	}
	if got != (Header{ApplicationID: kind.ApplicationID(), UserVersion: generation}) {
		return &HeaderError{Err: ErrStampNotHeld, Found: got, Want: want}
	}
	return nil
}

// Checkpoint moves every WAL frame into the main file and truncates the WAL,
// so the header bytes on disk are the values just set. A database in a
// rollback-journal mode has no WAL and passes. It refuses when a reader or
// writer kept the checkpoint from finishing, and cannot run inside a
// transaction.
func Checkpoint(ctx context.Context, q Querier) error {
	rows, err := q.QueryContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return fmt.Errorf("%w: no result row", ErrCheckpointBusy)
	}
	var busy, log, moved any
	if err := rows.Scan(&busy, &log, &moved); err != nil {
		return err
	}
	b, okB := busy.(int64)
	l, okL := log.(int64)
	m, okM := moved.(int64)
	if !okB || !okL || !okM {
		return fmt.Errorf("%w: result is not three integers", ErrCheckpointBusy)
	}
	if b != 0 || (l >= 0 && l != m) {
		return fmt.Errorf("%w: busy %d, %d of %d frames", ErrCheckpointBusy, b, m, l)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return rows.Close()
}

// Stamp is the writer for a file that is not in a transaction: SetHeader, then
// Checkpoint, then a read-back of both values.
func Stamp(ctx context.Context, q Querier, kind Kind, generation int32) error {
	if err := SetHeader(ctx, q, kind, generation); err != nil {
		return err
	}
	if err := Checkpoint(ctx, q); err != nil {
		return err
	}
	got, err := Read(ctx, q)
	if err != nil {
		return err
	}
	if got != (Header{ApplicationID: kind.ApplicationID(), UserVersion: generation}) {
		return &HeaderError{Err: ErrStampNotHeld, Found: got, Want: Want{Kind: kind, Newest: generation}}
	}
	return nil
}

// Create makes conn's file a new file of kind at generation, once, however
// many writers race to create it. Inside one BEGIN EXCLUSIVE transaction it
// reads the header and the schema: an unstamped file with no schema gets the
// schema statements and the stamp, and a file another writer already created
// is checked to be kind at exactly generation instead. It then checkpoints and
// returns the header the file holds.
//
// An unstamped file that already has a schema is refused with ErrUnstamped:
// taking an existing file into a kind is its own step, never a side effect of
// opening it. conn must not be in a transaction.
func Create(ctx context.Context, conn *sql.Conn, kind Kind, generation int32, schema []string) (Header, error) {
	want := Want{Kind: kind, Newest: generation, Oldest: generation}
	if err := want.valid(); err != nil {
		return Header{}, err
	}
	if conn == nil {
		return Header{}, fmt.Errorf("%w: nil connection", ErrInvalidArgument)
	}
	if err := run(ctx, conn, "BEGIN EXCLUSIVE"); err != nil {
		return Header{}, err
	}
	h, err := createLocked(ctx, conn, want, schema)
	if err != nil {
		return h, errors.Join(err, run(context.WithoutCancel(ctx), conn, "ROLLBACK"))
	}
	if err := run(ctx, conn, "COMMIT"); err != nil {
		return h, errors.Join(err, run(context.WithoutCancel(ctx), conn, "ROLLBACK"))
	}
	if err := Checkpoint(ctx, conn); err != nil {
		return h, err
	}
	return h, nil
}

func createLocked(ctx context.Context, conn *sql.Conn, want Want, schema []string) (Header, error) {
	h, err := Read(ctx, conn)
	if err != nil {
		return Header{}, err
	}
	if h.ApplicationID != 0 {
		return h, h.Expect(want)
	}
	objects, err := schemaObjects(ctx, conn)
	if err != nil {
		return h, err
	}
	if objects > 0 || h.UserVersion != 0 {
		return h, &HeaderError{Err: ErrUnstamped, Found: h, Want: want}
	}
	for _, statement := range schema {
		if err := run(ctx, conn, statement); err != nil {
			return h, err
		}
	}
	if err := SetHeader(ctx, conn, want.Kind, want.Newest); err != nil {
		return h, err
	}
	return Header{ApplicationID: want.Kind.ApplicationID(), UserVersion: want.Newest}, nil
}

// schemaObjects counts the tables, indexes, views and triggers the file holds.
func schemaObjects(ctx context.Context, q Querier) (int, error) {
	rows, err := q.QueryContext(ctx, "SELECT type FROM sqlite_schema")
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return n, rows.Close()
}
