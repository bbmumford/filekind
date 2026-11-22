package filekind

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

// Querier is what every function here reads and writes through: *sql.DB,
// *sql.Conn and *sql.Tx all satisfy it. Pragmas are sent as queries and their
// rows drained, because the Turso engine answers a pragma as a query that
// returns rows and refuses nothing sent that way.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Header is the two identity fields of a database header: application_id at
// byte offset 68 and user_version at offset 60, each a big-endian 32-bit
// integer that the engine reads as signed.
type Header struct {
	ApplicationID int32
	UserVersion   int32
}

// Kind splits the application_id into its kind byte; ok is false outside the
// 'ORB' prefix.
func (h Header) Kind() (Kind, bool) { return KindOf(h.ApplicationID) }

// Read returns the header through the engine, which reads the WAL as well as
// the main file, so a value set and not yet checkpointed is already the value
// read here.
func Read(ctx context.Context, q Querier) (Header, error) {
	app, err := pragmaInt(ctx, q, "PRAGMA application_id")
	if err != nil {
		return Header{}, err
	}
	version, err := pragmaInt(ctx, q, "PRAGMA user_version")
	if err != nil {
		return Header{}, err
	}
	return Header{ApplicationID: app, UserVersion: version}, nil
}

// Check reads the header and refuses it unless it holds want's kind at a
// generation want reads. It returns the header it read either way, so a caller
// that accepts an older generation knows which one it holds.
func Check(ctx context.Context, q Querier, want Want) (Header, error) {
	if err := want.valid(); err != nil {
		return Header{}, err
	}
	h, err := Read(ctx, q)
	if err != nil {
		return Header{}, err
	}
	return h, h.Expect(want)
}

// Expect judges a header already read, by the same rules Check applies. The
// refusals are tried in a fixed order, so one header always earns one name:
// unstamped, foreign, unknown kind, wrong kind, no generation, newer, older.
func (h Header) Expect(want Want) error {
	if err := want.valid(); err != nil {
		return err
	}
	refuse := func(err error) error { return &HeaderError{Err: err, Found: h, Want: want} }
	if h.ApplicationID == 0 {
		return refuse(ErrUnstamped)
	}
	kind, ok := h.Kind()
	if !ok {
		return refuse(ErrForeignFile)
	}
	if _, ok := Lookup(kind); !ok {
		return refuse(ErrUnknownKind)
	}
	if kind != want.Kind {
		return refuse(ErrWrongKind)
	}
	if h.UserVersion < 1 {
		return refuse(ErrNoGeneration)
	}
	if h.UserVersion > want.Newest {
		return refuse(ErrNewerGeneration)
	}
	if h.UserVersion < want.oldest() {
		return refuse(ErrOlderGeneration)
	}
	return nil
}

// valid refuses a Want no file could satisfy: an unregistered kind, a newest
// generation below 1, or an oldest above the newest.
func (w Want) valid() error {
	if _, ok := Lookup(w.Kind); !ok {
		return fmt.Errorf("%w: expected kind 0x%02x is not registered", ErrInvalidArgument, uint8(w.Kind))
	}
	if w.Newest < 1 || w.Oldest < 0 || w.oldest() > w.Newest {
		return fmt.Errorf("%w: generations %d to %d", ErrInvalidArgument, w.oldest(), w.Newest)
	}
	return nil
}

// sqliteMagic is the first 16 bytes of every plaintext SQLite database file.
var sqliteMagic = []byte("SQLite format 3\x00")

// ReadFileHeader reads the header from the file's first 100 bytes without an
// engine. An empty file is a database not yet written and reads as the zero
// Header. A file that does not start with the plaintext magic is refused with
// ErrNotPlainFile: an encrypted file's header is read through the engine.
//
// It sees only what has been checkpointed into the main file; a WAL database
// whose header was set and not checkpointed still shows the old values here,
// which is why Stamp checkpoints.
func ReadFileHeader(path string) (Header, error) {
	f, err := os.Open(path)
	if err != nil {
		return Header{}, err
	}
	defer f.Close()
	buf := make([]byte, 100)
	n, err := io.ReadFull(f, buf)
	if n == 0 && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)) {
		return Header{}, nil
	}
	if err != nil {
		return Header{}, fmt.Errorf("%w: header shorter than 100 bytes", ErrNotPlainFile)
	}
	if !bytes.Equal(buf[:16], sqliteMagic) {
		return Header{}, ErrNotPlainFile
	}
	return Header{
		ApplicationID: int32(binary.BigEndian.Uint32(buf[68:72])),
		UserVersion:   int32(binary.BigEndian.Uint32(buf[60:64])),
	}, nil
}

// pragmaInt runs a pragma that answers one row of one integer and returns it.
// Anything else, including a value outside 32 bits, is ErrUnreadableHeader.
func pragmaInt(ctx context.Context, q Querier, pragma string) (int32, error) {
	rows, err := q.QueryContext(ctx, pragma)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, err
		}
		return 0, fmt.Errorf("%w: %s returned no row", ErrUnreadableHeader, pragma)
	}
	var v any
	if err := rows.Scan(&v); err != nil {
		return 0, err
	}
	n, ok := v.(int64)
	if !ok || n < math.MinInt32 || n > math.MaxInt32 {
		return 0, fmt.Errorf("%w: %s returned %T", ErrUnreadableHeader, pragma, v)
	}
	if rows.Next() {
		return 0, fmt.Errorf("%w: %s returned more than one row", ErrUnreadableHeader, pragma)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return int32(n), rows.Close()
}

// run sends one statement as a query and drains its rows.
func run(ctx context.Context, q Querier, statement string) error {
	rows, err := q.QueryContext(ctx, statement)
	if err != nil {
		return err
	}
	for rows.Next() {
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	return rows.Close()
}
