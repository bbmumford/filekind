"""Writes the golden vectors: the fixture databases this folder holds and
vectors.json, which both helpers' tests must reproduce.

Each expected canonical stream is built here from the literal values the
script inserts, sorted by key in Python, by an encoder of its own; neither
helper's reader is used. BLAKE3 comes from the Python helper's hash, which the
Go tests check against an independent implementation through these same
vectors and blake3.json. The canonical JSON outputs are Python's json.dumps,
the form's definition. Run it as python3 testdata/vectors/make_vectors.py
<new empty directory> and compare or copy what it writes; it never overwrites
a file. Fixture file bytes depend on the SQLite library that writes them; the
vectors do not."""

from __future__ import annotations

import hashlib
import json
import os
import random
import sqlite3
import struct
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "..", "..", "python"))
OUT = HERE

from filekind._blake3 import pure_blake3  # noqa: E402

PREFIX = 0x4F524200
FIXTURE = 0xFF
RUN_EVENTS = 0x22

ITEMS = [
    # k, i, r, t, b, n, d
    ("b", 0, 1.5, "héllo", b"\x00\xff", None, "2026-11-21 10:00:00"),
    ("a", -1, -0.0, "", b"", 7, "not a time"),
    ("d", 2 ** 63 - 1, 1e300, "a\x00b", bytes(range(256)), "x", None),
    ("c", -(2 ** 63), 2.5e-10, "\U0001F600   \"q\" \\", b"\x00", 1.25, "2026-01-01"),
    ("aa", 1, None, None, None, b"\x01", None),
]
PAIRS = [("x", 2, b"\x02"), ("x", 1, b"\x01"), ("w", 10, b""), ("x", -3, None)]
EXAMPLE = [(2, None), (1, "a")]


def schema(conn):
    conn.execute("CREATE TABLE items (k TEXT PRIMARY KEY, i INTEGER, r REAL, t TEXT, b BLOB, n, d DATETIME)")
    conn.execute("CREATE TABLE pairs (a TEXT, b INTEGER, v BLOB, PRIMARY KEY (a, b)) WITHOUT ROWID")
    conn.execute("CREATE TABLE example (k INTEGER PRIMARY KEY, v TEXT)")
    conn.execute("CREATE TABLE dupes (tag TEXT, v INTEGER)")
    conn.execute("CREATE TABLE mixed (k, v TEXT)")
    conn.execute("CREATE TABLE nullkeys (k, v TEXT)")
    conn.execute("CREATE TABLE badtext (k INTEGER PRIMARY KEY, t TEXT)")
    conn.execute("CREATE VIEW itemsView AS SELECT * FROM items")


def fill(conn, reverse=False):
    order = (lambda rows: list(reversed(rows))) if reverse else list
    conn.executemany("INSERT INTO items VALUES (?, ?, ?, ?, ?, ?, ?)", order(ITEMS))
    conn.executemany("INSERT INTO pairs VALUES (?, ?, ?)", order(PAIRS))
    conn.executemany("INSERT INTO example VALUES (?, ?)", order(EXAMPLE))
    conn.executemany("INSERT INTO dupes VALUES (?, ?)", [("p", 1), ("p", 2), ("q", 3)])
    conn.executemany("INSERT INTO mixed VALUES (?, ?)", [(1, "int"), (1.0, "real")])
    conn.executemany("INSERT INTO nullkeys VALUES (?, ?)", [(None, "a"), (None, "b")])
    conn.execute("INSERT INTO badtext VALUES (1, CAST(x'c3' AS TEXT))")


def new_db(name, page_size=None):
    path = os.path.join(OUT, name)
    if os.path.exists(path):
        raise SystemExit(f"{path} exists; write the vectors into a new empty directory")
    conn = sqlite3.connect(path, isolation_level=None)
    if page_size:
        conn.execute(f"PRAGMA page_size = {page_size}")
    conn.execute("PRAGMA journal_mode = DELETE")
    return conn


def stamp(conn, app, version):
    conn.execute(f"PRAGMA application_id = {app}")
    conn.execute(f"PRAGMA user_version = {version}")


def signed32(v):
    return v - (1 << 32) if v >= 1 << 31 else v


# The canonical stream, from literal values (SPEC.md, "Canonical row digest").
def s32(s):
    raw = s.encode("utf-8")
    return struct.pack(">I", len(raw)) + raw


def literal_value(cls, v):
    if cls == "null":
        return b"\x00"
    if cls == "integer":
        return b"\x01" + struct.pack(">q", v)
    if cls == "real":
        return b"\x02" + struct.pack(">d", 0.0 if v == 0 else v)
    raw = v.encode("utf-8") if isinstance(v, str) else v
    return (b"\x03" if cls == "text" else b"\x04") + struct.pack(">I", len(raw)) + raw


def stream(kind, set_, table, columns, key, rows):
    out = b"filekind rows v1\n" + struct.pack(">I", PREFIX | kind) + s32(set_) + s32(table)
    for names in (columns, key):
        out += struct.pack(">I", len(names)) + b"".join(s32(n) for n in names)
    for row in rows:
        for cls, v in row:
            out += literal_value(cls, v)
    return out + b"\x05" + struct.pack(">Q", len(rows))


def cls_of(v, declared):
    """The storage class SQLite gives v in a column of the declared type, for
    the values this script inserts."""
    if v is None:
        return "null"
    if declared == "REAL" and isinstance(v, int):
        return "real"
    if isinstance(v, int):
        return "integer"
    if isinstance(v, float):
        return "real"
    if isinstance(v, str):
        return "text"
    return "blob"


ITEM_TYPES = {"i": "INTEGER", "r": "REAL", "t": "TEXT", "b": "BLOB", "n": "", "d": "DATETIME"}
ITEM_COLS = ["i", "r", "t", "b", "n", "d"]


def items_rows(where=None):
    rows = []
    for k, i, r, t, b, n, d in sorted(ITEMS, key=lambda row: row[0].encode("utf-8")):
        if where and not where((k, i, r, t, b, n, d)):
            continue
        values = dict(zip(ITEM_COLS, (i, r, t, b, n, d)))
        rows.append([(cls_of(values[c], ITEM_TYPES[c]), values[c]) for c in ITEM_COLS])
    return rows


def digest_case(file, rowset, rows):
    kind = {"fixture": FIXTURE, "runEvents": RUN_EVENTS}[rowset["kind"]]
    data = stream(kind, rowset["set"], rowset["table"], rowset["columns"], rowset["key"], rows)
    case = {"file": file, "rowSet": rowset, "rows": len(rows),
            "sha256": hashlib.sha256(data).hexdigest(), "blake3": pure_blake3(data).hexdigest()}
    if len(data) <= 4096:
        case["streamHex"] = data.hex()
    return case


def main():
    vectors = {"headers": [], "digests": [], "values": [], "json": []}

    # rows.db: every value class, rows inserted out of key order.
    conn = new_db("rows.db")
    schema(conn)
    fill(conn)
    stamp(conn, PREFIX | FIXTURE, 1)
    conn.close()
    # rows-relaid.db: the same rows in the opposite order, another page size, vacuumed.
    conn = new_db("rows-relaid.db", page_size=1024)
    schema(conn)
    fill(conn, reverse=True)
    stamp(conn, PREFIX | FIXTURE, 1)
    conn.execute("VACUUM")
    conn.close()

    for name, app, version, with_table in (
        ("unstamped.db", 0, 0, True),
        ("foreign.db", 0x12345678, 1, True),
        ("unknown-kind.db", PREFIX | 0xEE, 1, True),
        ("newer.db", PREFIX | FIXTURE, 2, True),
        ("no-generation.db", PREFIX | FIXTURE, 0, True),
    ):
        conn = new_db(name)
        if with_table:
            conn.execute("CREATE TABLE t (k INTEGER PRIMARY KEY)")
        stamp(conn, app, version)
        conn.close()
    open(os.path.join(OUT, "empty.db"), "xb").close()

    conn = new_db("base3-ok.db")
    conn.execute("CREATE TABLE events (id INTEGER PRIMARY KEY, body TEXT NOT NULL, trits BLOB NOT NULL)")
    conn.execute("CREATE TABLE names (name TEXT PRIMARY KEY, value TEXT) WITHOUT ROWID")
    conn.execute("INSERT INTO events VALUES (1, '{\"a\":\"x\"}', x'7979')")
    conn.execute("INSERT INTO names VALUES ('seed', '{\"trits\":\"+-0\",\"decimal\":8}')")
    stamp(conn, PREFIX | RUN_EVENTS, 1)
    conn.close()
    conn = new_db("base3-integer.db")
    conn.execute("CREATE TABLE events (id INTEGER PRIMARY KEY, n BLOB)")
    conn.execute("INSERT INTO events VALUES (1, 5)")
    stamp(conn, PREFIX | RUN_EVENTS, 1)
    conn.close()

    headers = {
        "rows.db": (PREFIX | FIXTURE, 1), "rows-relaid.db": (PREFIX | FIXTURE, 1), "unstamped.db": (0, 0),
        "foreign.db": (0x12345678, 1), "unknown-kind.db": (PREFIX | 0xEE, 1), "newer.db": (PREFIX | FIXTURE, 2),
        "no-generation.db": (PREFIX | FIXTURE, 0), "empty.db": (0, 0),
        "base3-ok.db": (PREFIX | RUN_EVENTS, 1), "base3-integer.db": (PREFIX | RUN_EVENTS, 1),
    }
    checks = {
        "rows.db": [("fixture", 1, 0, "ok"), ("fixture", 1, 1, "ok"), ("fixture", 3, 2, "OlderGeneration"),
                    ("runLedger", 2, 0, "WrongKind"), ("auditSegment", 1, 0, "WrongKind")],
        "rows-relaid.db": [("fixture", 1, 0, "ok")],
        "unstamped.db": [("fixture", 1, 0, "Unstamped")],
        "empty.db": [("fixture", 1, 0, "Unstamped")],
        "foreign.db": [("fixture", 1, 0, "ForeignFile")],
        "unknown-kind.db": [("fixture", 1, 0, "UnknownKind")],
        "newer.db": [("fixture", 1, 0, "NewerGeneration"), ("fixture", 2, 0, "ok"), ("fixture", 2, 2, "ok")],
        "no-generation.db": [("fixture", 1, 0, "NoGeneration")],
        "base3-ok.db": [("runEvents", 1, 0, "ok"), ("fixture", 1, 0, "WrongKind")],
    }
    for file, (app, version) in headers.items():
        vectors["headers"].append({
            "file": file,
            "header": {"applicationId": signed32(app), "userVersion": version},
            "checks": [{"kind": k, "newest": n, "oldest": o, "outcome": out} for k, n, o, out in checks.get(file, [])],
        })

    items = {"kind": "fixture", "set": "items", "table": "items", "columns": ITEM_COLS, "key": ["k"]}
    pairs = {"kind": "fixture", "set": "pairs", "table": "pairs", "columns": ["a", "b", "v"], "key": ["a", "b"]}
    example = {"kind": "fixture", "set": "example", "table": "example", "columns": ["k", "v"], "key": ["k"]}
    pair_rows = [[("text", a), ("integer", b), cls_and(v)] for a, b, v in sorted(PAIRS, key=lambda r: (r[0].encode(), r[1]))]
    example_rows = [[("integer", k), cls_and(v)] for k, v in sorted(EXAMPLE)]
    for file in ("rows.db", "rows-relaid.db"):
        vectors["digests"].append(digest_case(file, items, items_rows()))
        vectors["digests"].append(digest_case(file, pairs, pair_rows))
        vectors["digests"].append(digest_case(file, example, example_rows))
        filtered = dict(items, set="itemsFrom0", where="i >= ?", args=[0])
        vectors["digests"].append(digest_case(file, filtered, items_rows(lambda row: row[1] >= 0)))
    # The same rows, another domain: another set label, another kind.
    vectors["digests"].append(digest_case("rows.db", dict(example, set="example2"), example_rows))
    for rowset, outcome in (
        ({"kind": "fixture", "set": "dupes", "table": "dupes", "columns": ["v"], "key": ["tag"]}, "KeyNotUnique"),
        ({"kind": "fixture", "set": "mixed", "table": "mixed", "columns": ["v"], "key": ["k"]}, "KeyNotUnique"),
        ({"kind": "fixture", "set": "nullkeys", "table": "nullkeys", "columns": ["v"], "key": ["k"]}, "KeyNotUnique"),
        ({"kind": "fixture", "set": "view", "table": "itemsView", "columns": ["i"], "key": ["k"]}, "NotBaseTable"),
        ({"kind": "fixture", "set": "none", "table": "nope", "columns": ["i"], "key": ["k"]}, "NotBaseTable"),
        ({"kind": "fixture", "set": "engine", "table": "sqlite_schema", "columns": ["name"], "key": ["name"]}, "NotBaseTable"),
        ({"kind": "fixture", "set": "badtext", "table": "badtext", "columns": ["t"], "key": ["k"]}, "InvalidText"),
        ({"kind": "fixture", "set": "", "table": "items", "columns": ["i"], "key": ["k"]}, "InvalidArgument"),
        ({"kind": "fixture", "set": "nokey", "table": "items", "columns": ["i"], "key": []}, "InvalidArgument"),
    ):
        vectors["digests"].append({"file": "rows.db", "rowSet": rowset, "outcome": outcome})

    vectors["values"] = [
        {"file": "base3-ok.db", "kind": "runEvents", "outcome": "ok"},
        {"file": "base3-integer.db", "kind": "runEvents", "outcome": "BinaryNumber", "detail": "events.n"},
        {"file": "rows.db", "kind": "fixture", "outcome": "ok"},
    ]
    vectors["json"] = json_vectors()

    with open(os.path.join(OUT, "vectors.json"), "x", encoding="utf-8") as f:
        json.dump(vectors, f, indent=1, ensure_ascii=False, sort_keys=True)
        f.write("\n")


def cls_and(v):
    return (cls_of(v, ""), v)


def json_vectors():
    out = []
    docs = [
        '{"tf":[75047317648499560,-36],"decimal":0.5}',
        '{ "b" : [1, 2.0, -0, -0.0, 1e2, 1E-7, true, false, null], "a" : {} }',
        '{"z":1,"e":2,"é":3,"￿":4,"\U0001F600":5,"":6}',
        '["\\u0000\\u0001\\u001f\\u007f\\b\\f\\n\\r\\t\\"\\\\\\/    \\ud83d\\ude00"]',
        '[0.1, 1e16, 1e15, 0.0001, 0.00001, 5e-324, 1.7976931348623157e308, 123456789.123, 1e22, 1e-400]',
        '[123456789012345678901234567890, -42, 0]',
        '"plain"',
    ]
    rng = random.Random(3)
    floats = []
    while len(floats) < 400:
        f = struct.unpack(">d", rng.getrandbits(64).to_bytes(8, "big"))[0]
        if f == f and abs(f) != float("inf"):
            floats.append(f)
    for scale in range(-30, 31):
        floats.append(rng.random() * 10.0 ** scale)
    docs.append(json.dumps(floats))
    for doc in docs:
        value = json.loads(doc)
        canon = json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False, allow_nan=False)
        out.append({"input": doc, "canonical": canon})
    for doc in ['{"a":1,"a":2}', "NaN", "[Infinity]", '"\\ud800"', '"\\udc00x"', "1 2", "", '{"a":1e400}',
                "﻿{}", "[1,]", "9" * 4301]:
        out.append({"input": doc, "outcome": "InvalidJSON"})
    out.append({"inputHex": "22ff22", "outcome": "InvalidJSON"})
    return out


if __name__ == "__main__":
    if len(sys.argv) != 2 or not os.path.isdir(sys.argv[1]) or os.listdir(sys.argv[1]):
        raise SystemExit("usage: make_vectors.py <new empty directory>")
    OUT = sys.argv[1]
    main()
