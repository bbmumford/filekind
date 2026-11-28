// Package sqliteverify opens a file from elsewhere through standard SQLite
// (modernc.org/sqlite) the way filekind.Verify requires, whichever engine
// wrote the file: read-only, with query_only on and trusted_schema off, after
// its size is checked against the caller's bound. A file the Turso engine
// writes is verified here, since that engine cannot set query_only and
// filekind.Verify refuses a connection made through it. It is its own package
// so the filekind package itself links no SQLite engine.
package sqliteverify

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bbmumford/filekind"
	_ "modernc.org/sqlite"
)

// ErrNotRegularFile: the path names a directory, a symbolic link, a device or
// anything else that is not a regular file, so it is not opened.
var ErrNotRegularFile = errors.New("sqliteverify: the path is not a regular file")

// ErrTooLarge: the file is larger than the caller's bound, so it is not
// opened.
var ErrTooLarge = errors.New("sqliteverify: the file is larger than its bound")

// Open checks that path is a regular file of at most maxBytes bytes and opens
// it read-only through standard SQLite with query_only on and trusted_schema
// off, on one connection, so filekind.Verify and then filekind.DigestRows read
// it on the settings both require. The path is not followed through a
// symbolic link. maxBytes below 1 is refused with filekind.ErrInvalidArgument.
// The caller closes the returned handle.
func Open(path string, maxBytes int64) (*sql.DB, error) {
	if maxBytes < 1 {
		return nil, fmt.Errorf("%w: a size bound of %d bytes", filekind.ErrInvalidArgument, maxBytes)
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s", ErrNotRegularFile, fi.Mode().Type())
	}
	if fi.Size() > maxBytes {
		return nil, fmt.Errorf("%w: %d bytes, bound %d", ErrTooLarge, fi.Size(), maxBytes)
	}
	dsn, err := readOnlyDSN(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// File opens path as Open does, runs filekind.Verify on that connection
// against want, and closes it: the header it returns is trusted only when the
// error is nil.
func File(ctx context.Context, path string, want filekind.Want, maxBytes int64) (filekind.Header, error) {
	db, err := Open(path, maxBytes)
	if err != nil {
		return filekind.Header{}, err
	}
	h, err := filekind.Verify(ctx, db, want)
	if cerr := db.Close(); err == nil && cerr != nil {
		return filekind.Header{}, cerr
	}
	return h, err
}

// readOnlyDSN is modernc's data-source name for path: an SQLite URI opened
// with mode=ro, whose _pragma parameters modernc runs on every new connection.
// SQLite decodes %HH in a URI's path and ends the path at '?' or '#', so '%',
// '?' and '#' in the path are escaped; the path is made absolute so a relative
// path never reads as a URI authority.
func readOnlyDSN(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	p = strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23").Replace(p)
	return "file:" + p + "?mode=ro&_pragma=query_only(1)&_pragma=trusted_schema(0)", nil
}
