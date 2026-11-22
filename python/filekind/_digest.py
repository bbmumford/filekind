"""The canonical row digest (SPEC.md, "Canonical row digest"), over sqlite3."""

from __future__ import annotations

import hashlib
import math
import struct
from dataclasses import dataclass, field

from . import _errors as E
from ._blake3 import blake3
from ._registry import application_id, lookup

STREAM_MAGIC = b"filekind rows v1\n"
TAG_NULL, TAG_INTEGER, TAG_REAL, TAG_TEXT, TAG_BLOB, TAG_END = range(6)


@dataclass(frozen=True)
class RowSet:
    """The rows a digest covers: columns of the base table, ordered by key
    under BINARY collation, separated by kind and set. The key must be unique
    over the rows `where` selects; `args` are its parameters; `schema` names an
    attached database, empty for the main one."""

    kind: object
    set: str
    table: str
    columns: tuple
    key: tuple
    where: str = ""
    args: tuple = field(default=())
    schema: str = ""


@dataclass(frozen=True)
class Digest:
    sha256: bytes
    blake3: bytes
    rows: int

    @property
    def sha256_hex(self) -> str:
        return self.sha256.hex()

    @property
    def blake3_hex(self) -> str:
        return self.blake3.hex()


def _valid_name(name) -> bool:
    if not isinstance(name, str) or name == "" or "\x00" in name:
        return False
    try:
        name.encode("utf-8")
    except UnicodeEncodeError:
        return False
    return True


def _check(rs: RowSet) -> None:
    def bad(what):
        return E.InvalidArgument(f"row set {what}")

    if isinstance(rs.kind, bool) or lookup(rs.kind) is None:
        raise bad("kind is not registered")
    if not _valid_name(rs.set):
        raise bad("set label is empty or not UTF-8")
    if rs.schema and not _valid_name(rs.schema):
        raise bad("schema name is not UTF-8")
    if not _valid_name(rs.table):
        raise bad("table name is empty or not UTF-8")
    if isinstance(rs.columns, str) or isinstance(rs.key, str) or not rs.columns or not rs.key:
        raise bad("has no columns or no key")
    if not all(_valid_name(n) for n in tuple(rs.columns) + tuple(rs.key)):
        raise bad("column name is empty or not UTF-8")


def quote_name(name: str) -> str:
    return '"' + name.replace('"', '""') + '"'


def query(rs: RowSet) -> str:
    """The one statement a row set is read through: typeof(x) beside +x for
    every key column and then every column, ordered by the key COLLATE BINARY."""
    parts = []
    for name in tuple(rs.key) + tuple(rs.columns):
        q = quote_name(name)
        parts.append(f"typeof({q}), +{q}")
    source = (quote_name(rs.schema) + "." if rs.schema else "") + quote_name(rs.table)
    text = "SELECT " + ", ".join(parts) + " FROM " + source
    if rs.where:
        text += " WHERE (" + rs.where + ")"
    text += " ORDER BY " + ", ".join(quote_name(n) + " COLLATE BINARY" for n in rs.key)
    return text


def _fold(name: str) -> str:
    return "".join(chr(ord(c) + 32) if "A" <= c <= "Z" else c for c in name)


def _as_text(value) -> str:
    if isinstance(value, bytes):
        return value.decode("utf-8", "replace")
    return value if isinstance(value, str) else ""


def _require_base_table(conn, schema: str, table: str) -> None:
    source = (quote_name(schema) + ".sqlite_schema") if schema else "sqlite_schema"
    found = False
    for kind, name, sql in conn.execute(f"SELECT type, name, sql FROM {source}").fetchall():
        if _fold(_as_text(name)) != _fold(table):
            continue
        text = " ".join(_as_text(sql).split()).upper()
        if _as_text(kind) != "table" or text.startswith("CREATE VIRTUAL") or _fold(table).startswith("sqlite_"):
            raise E.NotBaseTable(table)
        found = True
    if not found:
        raise E.NotBaseTable(table)


def _cell(cls, value):
    """(tag, payload) for one typeof answer and the value beside it. Text comes
    in as bytes (the digest reads with text_factory=bytes) and must be UTF-8;
    a REAL negative zero becomes positive zero."""
    name = _as_text(cls)
    if name == "null":
        if value is not None:
            raise E.ValueType(f"null holds {type(value).__name__}")
        return (TAG_NULL, None)
    if name == "integer":
        if isinstance(value, bool) or not isinstance(value, int):
            raise E.ValueType(f"integer holds {type(value).__name__}")
        return (TAG_INTEGER, value)
    if name == "real":
        if not isinstance(value, float) or math.isnan(value):
            raise E.ValueType(f"real holds {type(value).__name__} {value!r}")
        return (TAG_REAL, 0.0 if value == 0 else value)
    if name == "text":
        raw = value.encode("utf-8", "surrogatepass") if isinstance(value, str) else value
        if not isinstance(raw, bytes):
            raise E.ValueType(f"text holds {type(value).__name__}")
        try:
            raw.decode("utf-8")
        except UnicodeDecodeError:
            raise E.InvalidText("text value is not valid UTF-8") from None
        return (TAG_TEXT, raw)
    if name == "blob":
        raw = value.encode("utf-8") if isinstance(value, str) else value
        if not isinstance(raw, bytes):
            raise E.ValueType(f"blob holds {type(value).__name__}")
        return (TAG_BLOB, raw)
    raise E.ValueType(f"storage class {name!r}")


def _encode_cell(tag, payload) -> bytes:
    if tag == TAG_NULL:
        return bytes([TAG_NULL])
    if tag == TAG_INTEGER:
        return bytes([TAG_INTEGER]) + struct.pack(">q", payload)
    if tag == TAG_REAL:
        return bytes([TAG_REAL]) + struct.pack(">d", payload)
    return bytes([tag]) + struct.pack(">I", len(payload)) + payload


def _same_value(a, b) -> bool:
    (ta, va), (tb, vb) = a, b
    if ta == TAG_NULL or tb == TAG_NULL:
        return ta == tb
    numeric = (TAG_INTEGER, TAG_REAL)
    if ta in numeric and tb in numeric:
        if ta == TAG_INTEGER and tb == TAG_INTEGER:
            return va == vb
        return float(va) == float(vb)
    return ta == tb and va == vb


def _string(s: str) -> bytes:
    raw = s.encode("utf-8")
    return struct.pack(">I", len(raw)) + raw


def stream_header(rs: RowSet) -> bytes:
    """The magic and the domain: application_id, set, table, columns, key."""
    out = bytearray(STREAM_MAGIC)
    out += struct.pack(">I", application_id(rs.kind))
    out += _string(rs.set) + _string(rs.table)
    for names in (rs.columns, rs.key):
        out += struct.pack(">I", len(names))
        for n in names:
            out += _string(n)
    return bytes(out)


def iter_stream(conn, rs: RowSet):
    """Yield the canonical stream of rs's rows in pieces; the row count is
    the last eight bytes. It reads rows through conn, never the file's bytes."""
    _check(rs)
    _require_base_table(conn, rs.schema, rs.table)
    yield stream_header(rs)
    saved = conn.text_factory
    conn.text_factory = bytes
    try:
        cursor = conn.execute(query(rs), tuple(rs.args))
        nkey = len(rs.key)
        previous = None
        count = 0
        while True:
            batch = cursor.fetchmany(512)
            if not batch:
                break
            out = bytearray()
            for row in batch:
                cells = [_cell(row[i], row[i + 1]) for i in range(0, len(row), 2)]
                key = cells[:nkey]
                if previous is not None and all(_same_value(a, b) for a, b in zip(previous, key)):
                    raise E.KeyNotUnique(rs.table)
                for tag, payload in cells[nkey:]:
                    out += _encode_cell(tag, payload)
                previous = key
                count += 1
            yield bytes(out)
        cursor.close()
    finally:
        conn.text_factory = saved
    yield bytes([TAG_END]) + struct.pack(">Q", count)


def encode_rows(conn, rs: RowSet) -> bytes:
    """The whole canonical stream of rs's rows."""
    return b"".join(iter_stream(conn, rs))


def digest_rows(conn, rs: RowSet) -> Digest:
    """SHA-256 and BLAKE3 of rs's canonical stream, and its row count."""
    s = hashlib.sha256()
    b = blake3()
    tail = b""
    for piece in iter_stream(conn, rs):
        s.update(piece)
        b.update(piece)
        tail = (tail + piece)[-8:]
    return Digest(s.digest(), b.digest(), struct.unpack(">Q", tail)[0])
