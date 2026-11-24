# filekind

filekind defines one self-describing single-file SQLite format: the identity
header a file carries, the registry of kinds, and the canonical digest of a
file's rows, with one helper in Go and one in Python. Both
implement [SPEC.md](SPEC.md) and pass the same golden vectors.

- **Identity.** `PRAGMA application_id` is `0x4F5242xx`, the bytes `ORB` and a
  kind byte from [kinds.tsv](kinds.tsv); `PRAGMA user_version` is the kind's
  format generation. A reader refuses, each by name, an unstamped file, another
  application's file, an unknown kind, a different kind than expected, and a
  generation outside the range its code reads.
- **Writing.** A stamp sets both fields, reads them back and checkpoints, since
  a value just set sits in the WAL until a checkpoint. A create runs a new
  file's schema and stamp once under an exclusive transaction, however many
  writers race to make it.
- **Digest.** Rows of a base table, ordered by a unique key, encoded value by
  value with their storage classes, separated by kind and set, hashed with
  SHA-256 and BLAKE3. Never the file's bytes.
- **Values.** A kind is `sql`, `base3` (no INTEGER or REAL value outside a rowid
  alias) or `radix`. A canonical JSON form for TEXT columns that hold JSON.

## Go

```sh
go get github.com/bbmumford/filekind
```

Package `filekind` imports only the standard library's `database/sql` and a
BLAKE3 package, so it works over any `database/sql` SQLite driver: tursogo,
go-libsql or modernc. It uses no cgo.

```go
h, err := filekind.Check(ctx, db, filekind.Want{Kind: filekind.AuditSegment, Newest: 1})
if errors.Is(err, filekind.ErrNewerGeneration) {
	// written by a newer release: refuse it, never guess at it
}

err = filekind.Stamp(ctx, db, filekind.AuditSegment, 1)

d, err := filekind.DigestRows(ctx, db, filekind.RowSet{
	Kind: filekind.AuditSegment, Set: "auditLog", Table: "audit_log",
	Columns: []string{"seq", "at", "body"}, Key: []string{"seq"},
})
fmt.Println(d.SHA256Hex(), d.BLAKE3Hex(), d.Rows)
```

`Verify` checks a file from elsewhere (quick_check, then the header) once the
caller has opened it read-only; `Create` makes a new file of a kind on a
`*sql.Conn`; `CheckValues` enforces a
`base3` kind's value rule; `CanonicalJSON` rewrites a JSON document in the
canonical form; `ReadFileHeader` reads the two fields from a plaintext file
without an engine.

## Python

```sh
pip install ./python              # standard library only
pip install './python[fast]'      # the blake3 package's hasher instead of the built-in one
```

```python
import sqlite3, filekind

conn = sqlite3.connect(path, isolation_level=None)
filekind.check(conn, filekind.Want("runLedger", 2))
d = filekind.digest_rows(conn, filekind.RowSet(kind="runLedger", set="runs", table="runs",
                                               columns=("path", "outcome"), key=("path",)))
print(d.sha256_hex, d.blake3_hex, d.rows)
```

`filekind.writer.Writer` is the writer for a file several processes append to
at once: WAL mode, a busy timeout, the create under an exclusive transaction,
and every write a short `BEGIN IMMEDIATE` transaction.

`filekind.base3` stores base-3 values by calling the ternary codec package
`orbitra` (`orbitra.ternary`), imported only when one of its functions is
called; `import filekind` never needs it. Install orbitra from its own checkout
first; the `base3` extra (`pip install './python[base3]'`) then finds it.

## Tests

```sh
go test ./...
cd python/tests && PYTHONPATH=.. python3 -m unittest
```

`enginetest/` is a separate module that runs the Go helper against the golden
vectors through the Turso engine (tursogo); `cd enginetest && go test ./...`.

## Releases

Releases are cut by release-please from conventional commit messages
(`fix:`, `feat:`, and so on); see [AGENTS.md](AGENTS.md).

## Dependencies

- No other package of this estate.
- `github.com/zeebo/blake3`, for the BLAKE3 half of the digest.
- Tests only: `modernc.org/sqlite`; `enginetest` requires
  `turso.tech/database/tursogo` and replaces `github.com/bbmumford/filekind`
  with `../`.

## Used by

`github.com/bbmumford/policypool`, whose identity option checks and stamps a pool's
database file through it.

## Platforms and build

Pure Go, no cgo and no build tags. The module's `go` directive is 1.25.0. The
Python helper needs only the standard library and supports Python 3.9 and
later; the release checks run it on 3.9, 3.12 and 3.13.

## Licence

MIT. Copyright (c) 2026 Brandon Miller-Mumford. The full text is in
[LICENSE](LICENSE).

## Contributing

[AGENTS.md](AGENTS.md) holds the repository's two rules, for people and coding
agents alike: no temporary documents in the repository, and conventional commit
messages that stand on their own. [CLAUDE.md](CLAUDE.md) imports it for Claude
Code.
