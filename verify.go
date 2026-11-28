package filekind

import (
	"context"
	"errors"
	"fmt"
)

// ErrNotIntact: PRAGMA quick_check did not answer the single row "ok".
var ErrNotIntact = errors.New("filekind: quick_check found the file damaged")

// ErrUnsafeOpen: Verify found the connection it was handed able to write the
// file (query_only off), able to run code the file's own schema carries
// (trusted_schema on), or not reporting either setting. A file from elsewhere
// is read on those two settings; without them its triggers and the functions
// its views and indexes name run with the reader's rights. Standard SQLite
// reports both; the Turso engine reports query_only off and trusted_schema
// with no row, so a file it writes is verified through standard SQLite
// (package sqliteverify).
var ErrUnsafeOpen = errors.New("filekind: the file is not opened query_only with trusted_schema off")

// safeOpen is each setting Verify reads before it reads the file, and the
// value the setting must hold.
var safeOpen = []struct {
	pragma string
	want   int64
}{{"query_only", 1}, {"trusted_schema", 0}}

// Verify checks a file from elsewhere that the caller has already opened
// read-only through standard SQLite, its size bounded first and query_only on
// and trusted_schema off set (SPEC.md, "Verifying a file from elsewhere";
// package sqliteverify makes that open). It reads both settings first and
// refuses with ErrUnsafeOpen, before any page of the file is read, a
// connection that reports query_only off or trusted_schema on, or that answers
// either setting with no row: an engine without the setting cannot be shown
// to hold it. Then PRAGMA quick_check must answer the one row "ok", and the
// header must satisfy want, as Check judges it. A damaged file is refused
// before its header is trusted. The error names how many rows quick_check
// returned, never their text, which can quote the file. An engine that refuses
// to read a damaged page returns its own error instead. The size bound stays
// the caller's: it holds before the open, and Verify is handed an open
// connection. A file the process opens through the Turso engine for its own
// use is judged by Check alone.
func Verify(ctx context.Context, q Querier, want Want) (Header, error) {
	if err := want.valid(); err != nil {
		return Header{}, err
	}
	for _, s := range safeOpen {
		v, reported, err := readSetting(ctx, q, s.pragma)
		if err != nil {
			return Header{}, err
		}
		if !reported {
			return Header{}, fmt.Errorf("%w: the engine reports no %s setting", ErrUnsafeOpen, s.pragma)
		}
		if v != s.want {
			return Header{}, fmt.Errorf("%w: %s is %d", ErrUnsafeOpen, s.pragma, v)
		}
	}
	rows, err := q.QueryContext(ctx, "PRAGMA quick_check")
	if err != nil {
		return Header{}, err
	}
	defer closeOnEarlyReturn(rows)
	var answers []string
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			return Header{}, err
		}
		answers = append(answers, asString(v))
	}
	if err := rows.Err(); err != nil {
		return Header{}, err
	}
	if err := rows.Close(); err != nil {
		return Header{}, err
	}
	if len(answers) != 1 || answers[0] != "ok" {
		return Header{}, fmt.Errorf("%w: quick_check returned %d rows", ErrNotIntact, len(answers))
	}
	return Check(ctx, q, want)
}

// readSetting reads one pragma setting for Verify: its value and whether the
// engine reported one (an engine without the setting answers no row). A value
// that is not a whole number is refused with ErrUnsafeOpen, since the setting
// cannot be judged.
func readSetting(ctx context.Context, q Querier, pragma string) (int64, bool, error) {
	rows, err := q.QueryContext(ctx, "PRAGMA "+pragma)
	if err != nil {
		return 0, false, err
	}
	defer closeOnEarlyReturn(rows)
	if !rows.Next() {
		return 0, false, closeAndErr(rows)
	}
	var v any
	if err := rows.Scan(&v); err != nil {
		return 0, false, err
	}
	n, ok := v.(int64)
	if !ok {
		return 0, false, fmt.Errorf("%w: %s answered %T", ErrUnsafeOpen, pragma, v)
	}
	return n, true, closeAndErr(rows)
}

// closeAndErr is the end of a read that stopped: the rows' iteration error,
// else Close's.
func closeAndErr(rows interface {
	Err() error
	Close() error
}) error {
	if err := rows.Err(); err != nil {
		return err
	}
	return rows.Close()
}
