"""The base-3 value rule's check: a kind whose registry row says base3 holds
no INTEGER or REAL value outside a rowid alias."""

from __future__ import annotations

from . import _errors as E
from ._digest import _as_text, quote_name
from ._registry import lookup


def _tables(conn):
    for name, sql in conn.execute("SELECT name, sql FROM sqlite_schema WHERE type = 'table' ORDER BY name").fetchall():
        text = " ".join(_as_text(sql).split()).upper()
        if _as_text(name).lower().startswith("sqlite_") or text.startswith("CREATE VIRTUAL"):
            continue
        yield _as_text(name), "WITHOUT ROWID" in text


def check_values(conn, kind) -> None:
    """Raise BinaryNumber naming the first table.column of a base-3 kind's file
    that holds an INTEGER or REAL value. Other kinds pass without a read."""
    entry = None if isinstance(kind, bool) else lookup(kind)
    if entry is None:
        raise E.InvalidArgument(f"kind {kind!r} is not registered")
    if entry.values != "base3":
        return
    for table, without_rowid in _tables(conn):
        info = conn.execute(f"PRAGMA table_info({quote_name(table)})").fetchall()
        keys = sum(1 for row in info if row[5] > 0)
        for _cid, name, declared, _notnull, _default, pk in info:
            if not without_rowid and keys == 1 and pk == 1 and (declared or "").strip().upper() == "INTEGER":
                continue
            q = quote_name(name)
            hit = conn.execute(f"SELECT 1 FROM {quote_name(table)} WHERE typeof({q}) IN ('integer', 'real') LIMIT 1").fetchone()
            if hit is not None:
                raise E.BinaryNumber(f"{table}.{name}")
