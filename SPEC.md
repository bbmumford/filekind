# The single-file SQLite format

This document is the format every helper implements. The Go package in this
repository and the Python package under `python/` both implement it, and both
must reproduce the golden vectors in `testdata/vectors/`. A third
implementation is correct when it reproduces them too.

A file in this format is an ordinary SQLite database. An unencrypted file is
standard `SQLite format 3`, readable by any SQLite library, Python's built-in
`sqlite3` included. Only a kind whose registry row says `encrypted yes` is
encrypted at rest.

## 1. Identity header

Two fields of the 100-byte database header say what a file is:

| Field | Byte offset | Size | Pragma | Holds |
|---|---|---|---|---|
| application_id | 68 | 4 bytes, big-endian | `PRAGMA application_id` | `0x4F5242xx`: the bytes `O`, `R`, `B` and the kind byte `xx` |
| user_version | 60 | 4 bytes, big-endian | `PRAGMA user_version` | the kind's format generation, from 1 |

Engines read both as signed 32-bit integers. Every registered application_id
is below 2^31, so it reads back as the same positive number everywhere.

A file whose application_id is 0 is **unstamped**. A file whose application_id
is set but does not start with `ORB` belongs to **another application**.

## 2. The kinds registry

`kinds.tsv` is the one registry. Its first line is the header
`kind name system generation encrypted values use`, tab-separated, and each
further line is one kind:

| Column | Holds |
|---|---|
| kind | the kind byte as two lower-case hex digits, `01` to `ff` |
| name | the kind's name in lowerCamelCase, unique |
| system | the system or package whose files are of the kind |
| generation | the current format generation, a whole number from 1 |
| encrypted | `yes` when files of the kind hold secrets and are encrypted at rest, else `no` |
| values | `sql`, `base3` or `radix` (section 7) |
| use | what files of the kind are for |

Rules:

- Kind bytes ascend down the file. A byte is never reused: a retired kind keeps
  its row.
- `00` is never a kind, so an unset kind is always refused.
- The bytes fall in ranges: `01`-`0f` platform stores, `10`-`1f` audit files,
  `20`-`3f` training, model and conversion files, `40`-`4f` repository tooling,
  `50`-`5f` package journals. `ff` is `fixture`, the kind of this
  repository's golden vectors and tests; no production reader expects it.
- A new kind is a new row in the next free byte of its range, added by a
  commit to this repository before any file of it is written.
- The Python package carries a byte-identical copy at
  `python/filekind/kinds.tsv`; both test suites fail when the copies differ.

## 3. Reading a file

A reader states what it expects: a kind, the newest generation its code reads,
and optionally the oldest. It reads both header fields **through the engine**
(the pragmas), which reads the WAL as well as the main file, in one statement
(`SELECT a.application_id, v.user_version FROM pragma_application_id AS a,
pragma_user_version AS v`), so both come from one snapshot and a reader racing
a stamp's commit never sees one field without the other; it refuses the file
unless it holds that kind at a generation in that range. The refusals are
tried in this order, so one header always earns one name:

| Order | Refusal | When |
|---|---|---|
| 1 | Unstamped | application_id is 0 |
| 2 | ForeignFile | application_id does not start with `ORB` |
| 3 | UnknownKind | the kind byte has no registry row |
| 4 | WrongKind | the kind is registered but not the one expected |
| 5 | NoGeneration | user_version is below 1 |
| 6 | NewerGeneration | user_version is newer than the newest the reader reads |
| 7 | OlderGeneration | user_version is older than the oldest the reader reads |

An expectation no file could meet (an unregistered kind, a newest below 1, an
oldest above the newest) is refused as InvalidArgument before the file is read.

A cache whose header is refused is rebuilt by its owner; an authoritative file
whose header is refused is not opened, and the refusal is the reason given.

**Reading the bytes directly.** A tool may read the two fields from the first
100 bytes without an engine when the file starts with the plaintext magic
`SQLite format 3\0`; an empty file reads as both fields 0. Anything else is
refused as NotPlainFile and read through the engine with its key. The bytes on
disk show only what has been checkpointed: in WAL mode a header just set sits
in the WAL and the main file still holds the old values (section 4).

Encrypted files, as measured: the Turso engine's `aes256gcm` keeps header bytes
16 to 99 in plaintext (the first 16 hold its own magic), so both fields sit at
their offsets before the key is given; SQLite3 Multiple Ciphers, which go-libsql
uses, encrypts the whole header, so the fields are readable only through the
engine after the key.

## 4. Writing the header

**Stamp.** Set application_id, then user_version, then read both back; then
checkpoint with `PRAGMA wal_checkpoint(TRUNCATE)` and read both back again. The
checkpoint is what moves a value just set out of the WAL into the main file, so
that a reader of the bytes (section 3) sees it. A checkpoint result whose
first column (busy) is not 0, or whose second and third columns (frames in the
WAL, frames moved) differ while the second is not -1, is a failed checkpoint
(CheckpointBusy). A rollback-journal database answers `0, -1, -1` and passes.
Setting the fields works inside a transaction and commits or rolls back with
it; the checkpoint runs after the commit.

A stamp never takes a file over:

- a file already of another kind is refused (WrongKind, or UnknownKind when its
  byte has no row);
- a file outside the `ORB` prefix, or one whose application_id is 0 but whose
  user_version another scheme already set, is refused (ForeignFile);
- a generation is never lowered (GenerationBackwards); stamping the generation
  a file already holds changes nothing.

A file's kind never changes. Its generation moves forward only as a format
change made at an update's confirm step, which keeps the old file until then.

**Create.** A new file is created once however many writers race to create it:
inside one `BEGIN EXCLUSIVE` transaction, read the header and count the schema
objects. An unstamped file with no schema gets the schema statements and the
stamp; a file another writer already created is checked to be the kind at
exactly the generation asked instead. After the commit, a file this writer
stamped is checkpointed. An unstamped file that already holds a schema is
refused (Unstamped): taking an existing file into a kind is a step of its own,
never a side effect of opening it.

**Engines.** Send every pragma as a query and drain its rows: the Turso engine
answers pragmas as queries.

## 5. Several writers on one file

A file several processes write at once:

- is in WAL mode, and every connection sets a busy timeout so it waits for the
  write lock instead of failing;
- is created as section 4 says, once, under the exclusive transaction;
- takes every write as a short `BEGIN IMMEDIATE` transaction, which holds the
  write lock from its start, so two writers never both read and then race to
  write;
- lives on a local disk, never on a network or synced folder, whose locking
  SQLite cannot rely on;
- is written by one engine only while it is live: Python's `sqlite3` and the
  Turso engine writing the same live file together has not been measured.

The Python package's `filekind.writer.Writer` does all of this; its test runs
several processes appending at once and checks that no row is lost or torn.

## 6. Canonical row digest

Anything sealed or signed is digested over its **rows**, never over the file's
bytes: two files holding the same rows digest the same however their pages are
laid out.

### The row set

| Field | Holds |
|---|---|
| kind | a registered kind; its application_id separates the digest's domain |
| set | a non-empty label naming which rows of the kind these are |
| schema | an attached database's name, or empty for the main database |
| table | a base table: not a view, a virtual table, or a table whose name starts with `sqlite_` |
| columns | the columns digested, in order, at least one |
| key | the columns the rows are ordered by, at least one, unique over the rows selected |
| where, args | an optional SQL condition and its parameters |

Names must be non-empty UTF-8 without NUL. The table is looked up in the
schema's `sqlite_schema`, comparing names with ASCII case folding as the engine
does; a missing name, a view, a virtual table or an engine table is refused
(NotBaseTable).

### The query

For key columns `k1 … kn` and columns `c1 … cm`, with every name quoted in
double quotes (an inner `"` doubled):

```sql
SELECT typeof("k1"), +"k1", …, typeof("kn"), +"kn",
       typeof("c1"), +"c1", …, typeof("cm"), +"cm"
FROM "schema"."table" [WHERE (where)]
ORDER BY "k1" COLLATE BINARY, …, "kn" COLLATE BINARY
```

The schema is the row set's, or `main` when it names none: the table is always
read qualified, because an unqualified name resolves to a TEMP table of that
name first, and the base-table check reads the same schema's `sqlite_schema`.

`typeof` gives each value's storage class as the engine holds it, and the unary
plus gives the same value with no declared type, so no driver converts it by
its column's declaration (tursogo and modernc SQLite both turn TEXT in a
DATETIME column into a time value; Python's `sqlite3` reads TEXT as bytes for
the digest). The key values are read only to check order; they are not
encoded unless they are also among the columns.

Every key column must therefore also be a column, or the row set is refused
(KeyNotCovered): a digest that is signed or sealed binds a row's key only
through the columns, so rows whose keys are renumbered with their order kept
would otherwise digest the same. A row set may state that its key's values are
not part of what the digest binds (`UnsignedKey` in Go, `unsigned_key` in
Python; `"unsignedKey": true` in the vectors), and only then may a key column
be outside the columns.

Two consecutive rows whose keys **tie** are refused (KeyNotUnique), because
rows that tie have no order an engine is bound to. Key values tie when both
are NULL, when both are numbers equal as numbers (INTEGER 1 ties REAL 1.0),
or when both are TEXT, or both BLOB, with equal bytes.

### The stream

All integers are big-endian. A *string* is a 4-byte length then that many
UTF-8 bytes.

```
"filekind rows v1\n"          17 bytes
application_id                4 bytes, the kind's
set, table                    strings
column count, column names    4 bytes, then strings
key count, key names          4 bytes, then strings
values                        each row's columns, in order, row after row
0x05, row count               1 byte, then 8 bytes
```

Each value is a tag and its payload:

| Storage class | Tag | Payload |
|---|---|---|
| NULL | `0x00` | none |
| INTEGER | `0x01` | 8 bytes, two's complement |
| REAL | `0x02` | 8 bytes, IEEE 754 binary64; negative zero is written as positive zero |
| TEXT | `0x03` | 4-byte length, then the UTF-8 bytes |
| BLOB | `0x04` | 4-byte length, then the bytes |

Negative zero becomes positive zero because SQLite reads a stored -0.0 back as
0.0 while the Turso engine reads it back as -0.0 (measured); only the
canonical form digests the same on both. NaN, which SQLite never stores, and
TEXT that is not valid UTF-8 are refused (ValueType, InvalidText). A value
whose type disagrees with the storage class `typeof` reported is refused
(ValueType). The Turso engine refuses to return TEXT that is not UTF-8 at all,
reporting the file corrupt, so there the refusal is the engine's error.

### The digest

The digest is SHA-256 and BLAKE3 (the unkeyed 32-byte hash), each over the
whole stream, and the row count. A file stores a hash as lowercase hex TEXT.

### Worked example

Kind `fixture` (`0x4F5242FF`), set `example`, table `example`, columns `k, v`,
key `k`, rows `(1, 'a')` and `(2, NULL)`:

```
66696c656b696e6420726f77732076310a   "filekind rows v1\n"
4f5242ff                             application_id
00000007 6578616d706c65              set "example"
00000007 6578616d706c65              table "example"
00000002 00000001 6b 00000001 76     columns k, v
00000001 00000001 6b                 key k
01 0000000000000001 03 00000001 61   1, 'a'
01 0000000000000002 00               2, NULL
05 0000000000000002                  end, two rows
```

## 7. Values

The registry's `values` column says how a kind stores values:

- `sql`: SQL integers and reals as usual.
- `base3`: every value is base-3 TEXT or a base-3 BLOB. No column of any table
  holds an INTEGER or REAL value, except a rowid alias (a rowid table's single
  `INTEGER PRIMARY KEY` column), which is the engine's own row number. The
  value check refuses the first table and column that breaks this
  (BinaryNumber). The base-3 forms are the ternary codec's (the `orbitra`
  package's `orbitra.ternary`); the Python package's `filekind.base3` calls it
  and carries no codec of its own.
- `radix`: each row names the radix its numbers are written in, so only the
  kind's own schema says which values must be base 3; the value check leaves
  such a kind to its schema's tests.

A hash or digest is an opaque byte string, not a value, so the base-3 rule
does not reach it: it is stored as lowercase hex TEXT, never as an integer.
SQLite's own internal numbers (row ids, page numbers) are binary in every
file.

## 8. Canonical JSON

A TEXT column that holds JSON holds it in one form, so equal bodies are equal
bytes and digest equal. The form is exactly what Python writes with
`json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False,
allow_nan=False)`:

- object keys sorted (by code point, which is UTF-8 byte order), no whitespace;
- strings in UTF-8; the only escapes are `\"`, `\\`, `\b`, `\f`, `\n`, `\r`,
  `\t` and `\u00xx` (lower-case hex) for every other character below U+0020;
- an integer is its digits (`-0` reads as `0`), at most 4300 of them;
- every other number is the float64 it parses to, written as Python's repr:
  the shortest digits that read back to it, positional when the decimal point
  falls between 10^-4 and 10^16 (a whole number ends `.0`), otherwise
  `d.ddde±XX` with at least two exponent digits;
- refused (InvalidJSON): a repeated key, `NaN` or `Infinity`, a number no
  float64 holds, an unpaired surrogate escape, input that is not UTF-8, and
  anything after the one value.

A record whose floats are written as exact base-3 pairs
(`{"decimal": x, "tf": [n, k]}`) is valid content for this form.

## 9. Verifying a file from elsewhere

A verifier of a file it did not write:

1. checks the file's size against its bound before opening it;
2. opens it read-only where the engine has a read-only open, and sets
   `PRAGMA query_only` and `PRAGMA trusted_schema = OFF` where the engine
   supports them (the Turso engine, as measured, refuses `query_only = ON`,
   reports `query_only` off and answers `trusted_schema` with no row, so the
   helpers' Verify refuses a file opened through it and a file from elsewhere
   is verified through an engine that has the setting);
3. runs `PRAGMA quick_check` and requires `ok`;
4. checks the header (section 3);
5. digests base tables only (section 6), and never reads a view.

Steps 1 and 2 belong to the caller, whose engine and data-source name the open
and the pragmas are. The helpers' Verify (Go) and verify (Python) read both
settings of step 2 first and refuse, as UnsafeOpen and before any page of the
file is read, a connection that reports `query_only` off or `trusted_schema`
on or answers either with no row, since an engine without the setting cannot
be shown to hold it. They then do steps 3 and 4: quick_check must answer the one row `ok`,
or the file is refused as NotIntact (an engine that refuses to read a damaged page returns its own
error instead), and then the header is checked. The digest does step 5. The Turso engine accepts `ATTACH`
only when its data-source name carries `experimental=attach`, and runs
triggers, `RAISE(ABORT)` included, without any flag.

## 10. Golden vectors

`testdata/vectors/` holds small fixture databases, `vectors.json` with their
header values, the expected outcome of each check, each row set's stream,
SHA-256, BLAKE3 and row count, the value checks and the canonical JSON cases,
and `blake3.json` with BLAKE3 hashes of the specification's test inputs (bytes
`i mod 251`). `make_vectors.py` writes the fixtures and computes every expected
stream from the literal values it inserts, independently of either helper's
reader; run it into a new empty directory and compare. Base-3 values have no
fixed vectors: their tests compute every expected value by calling the codec.

## 11. Refusals

| Name | Go | Python |
|---|---|---|
| Unstamped | `ErrUnstamped` | `filekind.Unstamped` |
| ForeignFile | `ErrForeignFile` | `filekind.ForeignFile` |
| UnknownKind | `ErrUnknownKind` | `filekind.UnknownKind` |
| WrongKind | `ErrWrongKind` | `filekind.WrongKind` |
| NoGeneration | `ErrNoGeneration` | `filekind.NoGeneration` |
| NewerGeneration | `ErrNewerGeneration` | `filekind.NewerGeneration` |
| OlderGeneration | `ErrOlderGeneration` | `filekind.OlderGeneration` |
| GenerationBackwards | `ErrGenerationBackwards` | `filekind.GenerationBackwards` |
| StampNotHeld | `ErrStampNotHeld` | `filekind.StampNotHeld` |
| CheckpointBusy | `ErrCheckpointBusy` | `filekind.CheckpointBusy` |
| NotIntact | `ErrNotIntact` | `filekind.NotIntact` |
| UnsafeOpen | `ErrUnsafeOpen` | `filekind.UnsafeOpen` |
| UnreadableHeader | `ErrUnreadableHeader` | `filekind.UnreadableHeader` |
| NotPlainFile | `ErrNotPlainFile` | `filekind.NotPlainFile` |
| InvalidArgument | `ErrInvalidArgument` | `filekind.InvalidArgument` |
| NotBaseTable | `ErrNotBaseTable` | `filekind.NotBaseTable` |
| KeyNotCovered | `ErrKeyNotCovered` | `filekind.KeyNotCovered` |
| KeyNotUnique | `ErrKeyNotUnique` | `filekind.KeyNotUnique` |
| ValueType | `ErrValueType` | `filekind.ValueType` |
| InvalidText | `ErrInvalidText` | `filekind.InvalidText` |
| BinaryNumber | `ErrBinaryNumber` | `filekind.BinaryNumber` |
| InvalidJSON | `ErrInvalidJSON` | `filekind.InvalidJSON` |
