"""The header check and the stamp-and-checkpoint writer, over sqlite3."""

from __future__ import annotations

from dataclasses import dataclass

from . import _errors as E
from ._registry import application_id, kind_byte, kind_of, lookup

SQLITE_MAGIC = b"SQLite format 3\x00"


@dataclass(frozen=True)
class Header:
    """application_id (byte offset 68) and user_version (offset 60), each a
    big-endian 32-bit integer read as signed."""

    application_id: int
    user_version: int

    @property
    def kind(self) -> int | None:
        return kind_of(self.application_id)


@dataclass(frozen=True)
class Want:
    """What a reader expects: a kind and the generations its code reads.
    oldest 0 reads every generation from 1 to newest."""

    kind: object
    newest: int
    oldest: int = 0

    def floor(self) -> int:
        return 1 if self.oldest == 0 else self.oldest


def _valid(want: Want) -> int:
    byte = kind_byte(want.kind)
    if isinstance(want.newest, bool) or not isinstance(want.newest, int) or want.newest < 1 \
            or not isinstance(want.oldest, int) or want.oldest < 0 or want.floor() > want.newest:
        raise E.InvalidArgument(f"generations {want.oldest} to {want.newest}")
    return byte


# Both header fields in one statement. One statement reads one snapshot of the
# file, so a reader racing a writer that stamps it reads the header from before
# the stamp's commit or from after it, never one field of each.
HEADER_QUERY = ("SELECT a.application_id, v.user_version "
                "FROM pragma_application_id AS a, pragma_user_version AS v")


def _header_int(field: str, value) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or not -(2 ** 31) <= value < 2 ** 31:
        raise E.UnreadableHeader(f"{field} read as {type(value).__name__}")
    return value


def read_header(conn) -> Header:
    """The header through the engine, which reads the WAL as well as the main
    file, so a value set and not yet checkpointed is already the value read.
    Both fields come from one statement, HEADER_QUERY."""
    rows = conn.execute(HEADER_QUERY).fetchall()
    if len(rows) != 1 or len(rows[0]) != 2:
        raise E.UnreadableHeader("the header query did not return one row of two values")
    return Header(_header_int("application_id", rows[0][0]), _header_int("user_version", rows[0][1]))


def expect(header: Header, want: Want) -> None:
    """Refuse a header unless it holds want's kind at a generation want reads.
    The refusals are tried in a fixed order so one header earns one name."""
    byte = _valid(want)

    def refuse(cls):
        return cls(f"{cls.__doc__.splitlines()[0]} application_id 0x{header.application_id & 0xFFFFFFFF:08x} "
                   f"user_version {header.user_version}", found=header, want=want)

    if header.application_id == 0:
        raise refuse(E.Unstamped)
    found = header.kind
    if found is None:
        raise refuse(E.ForeignFile)
    if lookup(found) is None:
        raise refuse(E.UnknownKind)
    if found != byte:
        raise refuse(E.WrongKind)
    if header.user_version < 1:
        raise refuse(E.NoGeneration)
    if header.user_version > want.newest:
        raise refuse(E.NewerGeneration)
    if header.user_version < want.floor():
        raise refuse(E.OlderGeneration)


def check(conn, want: Want) -> Header:
    """Read the header and refuse it unless it satisfies want; return it."""
    _valid(want)
    header = read_header(conn)
    expect(header, want)
    return header


def set_header(conn, kind, generation: int) -> None:
    """Write kind's application_id and generation and read both back. Works
    inside a transaction; the value sits in the WAL until checkpoint. Refuses
    to take over another kind's or another scheme's file, or to lower a
    generation."""
    want = Want(kind, generation)
    byte = _valid(want)
    header = read_header(conn)

    def refuse(cls, at=header):
        return cls(f"{cls.__doc__.splitlines()[0]} application_id 0x{at.application_id & 0xFFFFFFFF:08x} "
                   f"user_version {at.user_version}", found=at, want=want)

    if header != Header(0, 0):
        found = header.kind
        if header.application_id == 0 or found is None:
            raise refuse(E.ForeignFile)
        if found != byte:
            raise refuse(E.UnknownKind if lookup(found) is None else E.WrongKind)
        if header.user_version > generation:
            raise refuse(E.GenerationBackwards)
    conn.execute(f"PRAGMA application_id = {application_id(byte)}")
    conn.execute(f"PRAGMA user_version = {int(generation)}")
    got = read_header(conn)
    if got != Header(application_id(byte), generation):
        raise refuse(E.StampNotHeld, got)


def checkpoint(conn) -> None:
    """Move every WAL frame into the main file and truncate the WAL. A
    rollback-journal database has no WAL and passes."""
    if conn.in_transaction:
        raise E.CheckpointBusy("a checkpoint cannot run inside a transaction")
    rows = conn.execute("PRAGMA wal_checkpoint(TRUNCATE)").fetchall()
    if len(rows) != 1 or len(rows[0]) != 3 or not all(isinstance(v, int) for v in rows[0]):
        raise E.CheckpointBusy("wal_checkpoint did not return three integers")
    busy, log, moved = rows[0]
    if busy != 0 or (log >= 0 and log != moved):
        raise E.CheckpointBusy(f"busy {busy}, {moved} of {log} frames")


def stamp(conn, kind, generation: int) -> None:
    """The writer for a connection not in a transaction: set_header, then
    checkpoint, then a read-back of both values."""
    set_header(conn, kind, generation)
    checkpoint(conn)
    got = read_header(conn)
    if got != Header(application_id(kind), generation):
        raise E.StampNotHeld("the header read back after the checkpoint differs", found=got,
                             want=Want(kind, generation))


def _schema_objects(conn) -> int:
    return len(conn.execute("SELECT type FROM sqlite_schema").fetchall())


def create(conn, kind, generation: int, schema=()) -> Header:
    """Make conn's file a new file of kind at generation, once, however many
    writers race to create it. Inside one BEGIN EXCLUSIVE transaction the
    header and schema are read: an unstamped file with no schema gets the schema
    statements and the stamp, and a file another writer already created is
    checked to be kind at exactly generation instead. A file this call stamped
    is then checkpointed. An unstamped file that already has a schema is refused
    with Unstamped. conn must be in autocommit (isolation_level None) and not
    in a transaction."""
    want = Want(kind, generation, generation)
    _valid(want)
    if conn.in_transaction:
        raise E.InvalidArgument("create needs a connection outside a transaction")
    if conn.isolation_level is not None:
        raise E.InvalidArgument("create needs a connection opened with isolation_level=None")
    conn.execute("BEGIN EXCLUSIVE")
    stamped = False
    try:
        header = read_header(conn)
        if header.application_id != 0:
            expect(header, want)
        else:
            if _schema_objects(conn) > 0 or header.user_version != 0:
                raise E.Unstamped("an unstamped file that already holds a schema is not created over",
                                  found=header, want=want)
            for statement in schema:
                conn.execute(statement)
            set_header(conn, kind, generation)
            header = Header(application_id(kind), generation)
            stamped = True
        conn.execute("COMMIT")
    except BaseException:
        if conn.in_transaction:
            conn.execute("ROLLBACK")
        raise
    if stamped:
        checkpoint(conn)
    return header


def read_file_header(path) -> Header:
    """The header from the file's first 100 bytes, without an engine. An empty
    file reads as Header(0, 0); anything without the plaintext magic is
    NotPlainFile. It sees only what has been checkpointed."""
    with open(path, "rb") as f:
        head = f.read(100)
    if len(head) == 0:
        return Header(0, 0)
    if len(head) < 100:
        raise E.NotPlainFile("header shorter than 100 bytes")
    if head[:16] != SQLITE_MAGIC:
        raise E.NotPlainFile("not a plaintext SQLite file")
    return Header(int.from_bytes(head[68:72], "big", signed=True), int.from_bytes(head[60:64], "big", signed=True))


def verify(conn, want: Want) -> Header:
    """Check a file from elsewhere that the caller opened read-only, its size
    bounded first and query_only and trusted_schema=OFF set where supported:
    PRAGMA quick_check must answer the one row "ok", then the header must
    satisfy want. The refusal names how many rows quick_check returned, never
    their text."""
    _valid(want)
    answers = [row[0] for row in conn.execute("PRAGMA quick_check").fetchall()]
    if answers != ["ok"]:
        raise E.NotIntact(f"quick_check returned {len(answers)} rows")
    return check(conn, want)
