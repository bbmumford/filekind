"""The Python helper's own checks, case for case with the Go helper's."""

from __future__ import annotations

import hashlib
import os
import sqlite3
import threading
import time
import unittest

import filekind
from filekind._registry import parse

from _support import TempDir, connect, outcome

FIXTURE = filekind.application_id("fixture")
RUN_LEDGER = filekind.application_id("runLedger")


def raw(conn, app, version):
    conn.execute(f"PRAGMA application_id = {app}")
    conn.execute(f"PRAGMA user_version = {version}")


class Registry(unittest.TestCase):
    def test_parses_and_looks_up(self):
        e = filekind.lookup("runLedger")
        self.assertEqual((e.kind, e.generation, e.values), (0x20, 4, "radix"))
        self.assertEqual(filekind.lookup(0x10).name, "auditSegment")
        self.assertIsNone(filekind.lookup(0))
        self.assertEqual(filekind.application_id("runLedger"), 0x4F524220)
        self.assertEqual(filekind.kind_of(0x4F524210), 0x10)
        for app in (0, 0x12345678, 0x4F524142, -1):
            self.assertIsNone(filekind.kind_of(app))

    def test_refuses_a_malformed_registry(self):
        header = "kind\tname\tsystem\tgeneration\tencrypted\tvalues\tuse\n"
        good = "01\ta\tsystem\t1\tno\tsql\tuse\n"
        for text in ("kind\tname\n" + good, header + "0A\ta\tsystem\t1\tno\tsql\tuse\n",
                     header + "00\ta\tsystem\t1\tno\tsql\tuse\n",
                     header + "02\tb\tl\t1\tno\tsql\tu\n" + good,  # descending, names distinct
                     header + good + "01\tb\tl\t1\tno\tsql\tu\n",  # a repeated byte
                     header + good + "02\ta\tl\t1\tno\tsql\tu\n",  # a repeated name
                     header + "01\ta\tl\t0\tno\tsql\tu\n",
                     header + "01\ta\tl\t1\tmaybe\tsql\tu\n", header + "01\ta\tl\t1\tno\tdecimal\tu\n",
                     header + "01\ta\t\t1\tno\tsql\tu\n"):
            with self.assertRaises(filekind.InvalidArgument):
                parse(text)
        self.assertEqual(len(parse(header + good)), 1)


class Header(unittest.TestCase):
    def setUp(self):
        self.tmp = TempDir()

    def conn(self, name="t.db"):
        return connect(self.tmp.file(name))

    def test_check_names_every_refusal(self):
        cases = [
            (FIXTURE, 1, ("fixture", 1, 0), "ok"),
            (FIXTURE, 1, ("fixture", 3, 0), "ok"),
            (FIXTURE, 3, ("fixture", 3, 0), "ok"),
            (FIXTURE, 2, ("fixture", 3, 2), "ok"),
            (0, 0, ("fixture", 1, 0), "Unstamped"),
            (0, 4, ("fixture", 1, 0), "Unstamped"),
            (0x12345678, 1, ("fixture", 1, 0), "ForeignFile"),
            (-1, 1, ("fixture", 1, 0), "ForeignFile"),
            (0x4F5242EE, 1, ("fixture", 1, 0), "UnknownKind"),
            (RUN_LEDGER, 1, ("fixture", 1, 0), "WrongKind"),
            (FIXTURE, 0, ("fixture", 1, 0), "NoGeneration"),
            (FIXTURE, -2, ("fixture", 1, 0), "NoGeneration"),
            (FIXTURE, 2, ("fixture", 1, 0), "NewerGeneration"),
            (FIXTURE, 1, ("fixture", 3, 2), "OlderGeneration"),
        ]
        for i, (app, version, want, expected) in enumerate(cases):
            conn = self.conn(f"c{i}.db")
            raw(conn, app, version)
            got = outcome(lambda: filekind.check(conn, filekind.Want(*want)))
            self.assertEqual(got, expected, (app, version, want))
            self.assertEqual(filekind.read_header(conn), filekind.Header(app, version))

    def test_check_refuses_an_expectation_no_file_meets(self):
        conn = self.conn()
        for want in (filekind.Want(0xEE, 1), filekind.Want(0, 1), filekind.Want("fixture", 0),
                     filekind.Want("fixture", 2, 3), filekind.Want("fixture", 2, -1), filekind.Want(True, 1)):
            with self.assertRaises(filekind.InvalidArgument):
                filekind.check(conn, want)

    def test_read_file_header(self):
        conn = self.conn()
        conn.execute("CREATE TABLE t (k INTEGER PRIMARY KEY)")
        raw(conn, filekind.application_id("auditExport"), 7)
        self.assertEqual(filekind.read_file_header(self.tmp.file("t.db")),
                         filekind.Header(filekind.application_id("auditExport"), 7))
        open(self.tmp.file("empty.db"), "wb").close()
        self.assertEqual(filekind.read_file_header(self.tmp.file("empty.db")), filekind.Header(0, 0))
        with open(self.tmp.file("short.db"), "wb") as f:
            f.write(b"SQLite format 3\x00")
        with self.assertRaises(filekind.NotPlainFile):
            filekind.read_file_header(self.tmp.file("short.db"))
        with open(self.tmp.file("other.db"), "wb") as f:
            f.write(b"Turso\x00\x02" + bytes(200))
        with self.assertRaises(filekind.NotPlainFile):
            filekind.read_file_header(self.tmp.file("other.db"))

    def test_stamp_puts_the_header_in_the_file(self):
        path = self.tmp.file("wal.db")
        conn = connect(path)
        conn.execute("PRAGMA journal_mode = WAL")
        conn.execute("CREATE TABLE t (k INTEGER PRIMARY KEY)")
        filekind.checkpoint(conn)
        filekind.set_header(conn, "runEvents", 1)
        self.assertEqual(filekind.read_header(conn), filekind.Header(filekind.application_id("runEvents"), 1))
        self.assertEqual(filekind.read_file_header(path), filekind.Header(0, 0),
                         "before the checkpoint the WAL should still hold the stamp")
        filekind.stamp(conn, "runEvents", 2)
        self.assertEqual(filekind.read_file_header(path), filekind.Header(filekind.application_id("runEvents"), 2))

    def test_set_header_refuses_to_take_over_a_file(self):
        cases = [
            (0, 0, "fixture", 1, "ok"),
            (FIXTURE, 1, "fixture", 1, "ok"),
            (FIXTURE, 1, "fixture", 2, "ok"),
            (FIXTURE, 3, "fixture", 2, "GenerationBackwards"),
            (RUN_LEDGER, 1, "fixture", 1, "WrongKind"),
            (0x4F5242EE, 1, "fixture", 1, "UnknownKind"),
            (0x12345678, 0, "fixture", 1, "ForeignFile"),
            (0, 5, "fixture", 1, "ForeignFile"),
            (0, 0, 0xEE, 1, "InvalidArgument"),
            (0, 0, "fixture", 0, "InvalidArgument"),
        ]
        for i, (app, version, kind, gen, expected) in enumerate(cases):
            conn = self.conn(f"s{i}.db")
            raw(conn, app, version)
            got = outcome(lambda: filekind.set_header(conn, kind, gen))
            self.assertEqual(got, expected, (app, version, kind, gen))
            h = filekind.read_header(conn)
            if expected == "ok":
                self.assertEqual(h, filekind.Header(filekind.application_id(kind), gen))
            else:
                self.assertEqual(h, filekind.Header(app, version), "a refused stamp changed the header")

    def test_set_header_refuses_a_stamp_the_engine_drops(self):
        conn = self.conn()

        class Dropping:
            """Turns every application_id assignment into a statement that
            does nothing, standing for an engine that loses the stamp."""

            def execute(self, sql, *args):
                if sql.startswith("PRAGMA application_id = "):
                    sql = "SELECT 1"
                return conn.execute(sql, *args)

        with self.assertRaises(filekind.StampNotHeld):
            filekind.set_header(Dropping(), "fixture", 1)

    def test_read_header_never_reads_half_a_stamp(self):
        path = self.tmp.file("torn.db")
        conn = connect(path)
        writer = connect(path)

        class StampsBetweenStatements:
            """Lets another connection commit a whole stamp after the first
            statement the reader sends, the moment a racing creator can."""

            def __init__(self):
                self.statements = 0

            def execute(self, sql, *args):
                if self.statements == 1:
                    writer.execute("BEGIN IMMEDIATE")
                    writer.execute(f"PRAGMA application_id = {filekind.application_id('fixture')}")
                    writer.execute("PRAGMA user_version = 1")
                    writer.execute("COMMIT")
                self.statements += 1
                return conn.execute(sql, *args)

        h = filekind.read_header(StampsBetweenStatements())
        self.assertIn(h, (filekind.Header(0, 0), filekind.Header(filekind.application_id("fixture"), 1)),
                      "the header read holds one field from before a stamp and one from after it")

    def test_set_header_rolls_back_with_its_transaction(self):
        conn = self.conn()
        conn.execute("BEGIN")
        filekind.set_header(conn, "fixture", 1)
        conn.execute("ROLLBACK")
        self.assertEqual(filekind.read_header(conn), filekind.Header(0, 0))

    def test_checkpoint_refuses_when_a_reader_holds_the_wal(self):
        path = self.tmp.file("busy.db")
        writer = connect(path, timeout=0)
        writer.execute("PRAGMA journal_mode = WAL")
        writer.execute("CREATE TABLE t (k INTEGER PRIMARY KEY)")
        writer.execute("INSERT INTO t VALUES (1)")
        reader = connect(path, timeout=0)
        reader.execute("BEGIN")
        reader.execute("SELECT count(*) FROM t").fetchone()
        writer.execute("INSERT INTO t VALUES (2)")
        with self.assertRaises(filekind.CheckpointBusy):
            filekind.checkpoint(writer)
        reader.execute("ROLLBACK")
        filekind.checkpoint(writer)

    def test_create_waits_for_a_creator_holding_the_write_lock(self):
        """While another writer holds the write lock part way through creating
        the file, create waits for it instead of reading the file as empty."""
        for journal in ("delete", "wal"):
            path = self.tmp.file(f"held-{journal}.db")
            holder = connect(path, timeout=10, check_same_thread=False)
            holder.execute(f"PRAGMA journal_mode = {journal}")
            holder.execute("BEGIN IMMEDIATE")
            result = []

            def creator():
                conn = connect(path, timeout=10, check_same_thread=False)
                try:
                    result.append(filekind.create(conn, "fixture", 1, ["CREATE TABLE log (k TEXT PRIMARY KEY)"]))
                except Exception as err:  # the thread's outcome is judged below
                    result.append(err)
                finally:
                    conn.close()

            t = threading.Thread(target=creator)
            t.start()
            time.sleep(0.3)
            raw(holder, FIXTURE, 1)
            holder.execute("CREATE TABLE log (k TEXT PRIMARY KEY)")
            holder.execute("COMMIT")
            t.join(30)
            self.assertEqual(result, [filekind.Header(FIXTURE, 1)], journal)

    def test_create_checks_a_file_it_does_not_make(self):
        conn = self.conn()
        schema = ["CREATE TABLE t (k INTEGER PRIMARY KEY)"]
        self.assertEqual(filekind.create(conn, "fixture", 1, schema), filekind.Header(FIXTURE, 1))
        self.assertEqual(filekind.create(conn, "fixture", 1, schema), filekind.Header(FIXTURE, 1))
        with self.assertRaises(filekind.OlderGeneration):
            filekind.create(conn, "fixture", 2)
        with self.assertRaises(filekind.WrongKind):
            filekind.create(conn, "runEvents", 1)
        other = self.conn("other.db")
        other.execute("CREATE TABLE existing (k INTEGER PRIMARY KEY)")
        with self.assertRaises(filekind.Unstamped):
            filekind.create(other, "fixture", 1)
        self.assertEqual(filekind.read_header(other), filekind.Header(0, 0))
        legacy = sqlite3.connect(self.tmp.file("legacy.db"))
        with self.assertRaises(filekind.InvalidArgument):
            filekind.create(legacy, "fixture", 1)


class Verify(unittest.TestCase):
    def test_refuses_a_damaged_file_with_an_intact_header(self):
        tmp = TempDir()
        path = tmp.file("damaged.db")
        conn = connect(path)
        conn.execute("PRAGMA page_size = 1024")
        conn.execute("CREATE TABLE t (k INTEGER PRIMARY KEY, v TEXT)")
        conn.executemany("INSERT INTO t (v) VALUES (?)", [("x" * 200,)] * 40)
        filekind.stamp(conn, "fixture", 1)
        conn.close()
        with open(path, "r+b") as f:
            f.seek(2 * 1024)
            f.write(b"\xff" * 1024)
        self.assertEqual(filekind.read_file_header(path), filekind.Header(FIXTURE, 1))
        conn = connect(path)
        filekind.check(conn, filekind.Want("fixture", 1))
        with self.assertRaises((filekind.NotIntact, sqlite3.DatabaseError)):
            filekind.verify(conn, filekind.Want("fixture", 1))

    def test_names_what_quick_check_reports(self):
        """A NULL kept under a column the schema now declares NOT NULL: quick_check
        answers a row other than "ok", and verify refuses it as NotIntact."""
        tmp = TempDir()
        path = tmp.file("constraint.db")
        conn = connect(path)
        conn.execute("CREATE TABLE t (a TEXT)")
        conn.execute("INSERT INTO t VALUES (NULL)")
        conn.execute("PRAGMA writable_schema = ON")
        try:
            conn.execute("UPDATE sqlite_schema SET sql = 'CREATE TABLE t (a TEXT NOT NULL)' WHERE name = 't'")
        except sqlite3.OperationalError as err:
            # A SQLite built in defensive mode (Apple's system library) refuses
            # every write to the schema table, so this file cannot be made here.
            self.skipTest(f"this SQLite refuses writable_schema: {err}")
        conn.execute("PRAGMA writable_schema = OFF")
        filekind.stamp(conn, "fixture", 1)
        conn.close()
        with self.assertRaises(filekind.NotIntact):
            filekind.verify(connect(path), filekind.Want("fixture", 1))

    def test_passes_an_intact_file_and_judges_its_header(self):
        tmp = TempDir()
        conn = connect(tmp.fixture("rows.db"))
        self.assertEqual(filekind.verify(conn, filekind.Want("fixture", 1)).user_version, 1)
        with self.assertRaises(filekind.WrongKind):
            filekind.verify(conn, filekind.Want("runLedger", 2))
        with self.assertRaises(filekind.InvalidArgument):
            filekind.verify(conn, filekind.Want(0xEE, 1))


EXAMPLE = filekind.RowSet(kind="fixture", set="example", table="example", columns=("k", "v"), key=("k",))
EXAMPLE_STREAM = (
    "66696c656b696e6420726f77732076310a" "4f5242ff" "000000076578616d706c65" "000000076578616d706c65"
    "00000002" "000000016b" "0000000176" "00000001" "000000016b"
    "01" "0000000000000001" "03" "00000001" "61" "01" "0000000000000002" "00" "05" "0000000000000002"
)


def example_table(conn, reverse=False):
    conn.execute("CREATE TABLE example (k INTEGER PRIMARY KEY, v TEXT)")
    rows = [(1, "a"), (2, None)]
    conn.executemany("INSERT INTO example VALUES (?, ?)", list(reversed(rows)) if reverse else rows)


class Digest(unittest.TestCase):
    def setUp(self):
        self.tmp = TempDir()

    def test_spec_example(self):
        conn = connect(self.tmp.file("e.db"))
        example_table(conn)
        stream = filekind.encode_rows(conn, EXAMPLE)
        self.assertEqual(stream.hex(), EXAMPLE_STREAM)
        d = filekind.digest_rows(conn, EXAMPLE)
        self.assertEqual((d.sha256, d.rows), (hashlib.sha256(stream).digest(), 2))
        self.assertEqual(d.sha256_hex, d.sha256_hex.lower())

    def test_follows_the_key_not_the_storage_order(self):
        rs = filekind.RowSet(kind="fixture", set="s", table="t", columns=("v",), key=("k",))
        digests = []
        for name, rows in (("in.db", "abc"), ("against.db", "cab")):
            conn = connect(self.tmp.file(name))
            conn.execute("CREATE TABLE t (k TEXT PRIMARY KEY, v TEXT)")
            conn.executemany("INSERT INTO t VALUES (?, ?)", [(k, str("abc".index(k))) for k in rows])
            digests.append(filekind.digest_rows(conn, rs))
        self.assertEqual(digests[0], digests[1])

    def test_never_reads_the_file_bytes(self):
        one = connect(self.tmp.file("one.db"))
        example_table(one)
        two = connect(self.tmp.file("two.db"))
        two.execute("PRAGMA page_size = 1024")
        example_table(two, reverse=True)
        two.execute("CREATE TABLE unrelated (x TEXT)")
        two.execute("INSERT INTO unrelated VALUES ('padding')")
        two.execute("VACUUM")
        with open(self.tmp.file("one.db"), "rb") as f1, open(self.tmp.file("two.db"), "rb") as f2:
            self.assertNotEqual(f1.read(), f2.read())
        self.assertEqual(filekind.digest_rows(one, EXAMPLE), filekind.digest_rows(two, EXAMPLE))

    def test_separated_by_kind_and_set(self):
        conn = connect(self.tmp.file("e.db"))
        example_table(conn)
        base = filekind.digest_rows(conn, EXAMPLE)
        for other in (
            filekind.RowSet(kind="runEvents", set="example", table="example", columns=("k", "v"), key=("k",)),
            filekind.RowSet(kind="fixture", set="example2", table="example", columns=("k", "v"), key=("k",)),
            filekind.RowSet(kind="fixture", set="example", table="example", columns=("v", "k"), key=("k",)),
        ):
            d = filekind.digest_rows(conn, other)
            self.assertNotEqual(d.sha256, base.sha256)
            self.assertNotEqual(d.blake3, base.blake3)

    def test_takes_the_storage_class_from_the_engine(self):
        conn = connect(self.tmp.file("c.db"))
        conn.execute("CREATE TABLE t (k INTEGER PRIMARY KEY, d DATETIME, r REAL, n)")
        conn.execute("INSERT INTO t VALUES (1, '2026-11-21 10:00:00', -0.0, -0.0)")
        rs = filekind.RowSet(kind="fixture", set="s", table="t", columns=("d", "r", "n"), key=("k",))
        s = filekind.encode_rows(conn, rs)
        self.assertIn(b"\x03\x00\x00\x00\x13" + b"2026-11-21 10:00:00", s)
        self.assertNotIn(b"\x02\x80" + bytes(7), s)
        self.assertIn(b"\x02" + bytes(8) + b"\x02" + bytes(8), s)

    def test_cell_canonicalises_negative_zero_and_refuses_mismatches(self):
        from filekind._digest import _cell, _encode_cell

        self.assertEqual(_encode_cell(*_cell(b"real", -0.0)), _encode_cell(*_cell(b"real", 0.0)))
        for cls, value, err in ((b"integer", "1", filekind.ValueType), (b"real", 1, filekind.ValueType),
                                (b"real", float("nan"), filekind.ValueType), (b"null", 0, filekind.ValueType),
                                (b"text", 0, filekind.ValueType), (b"blob", 1.5, filekind.ValueType),
                                (b"date", b"x", filekind.ValueType), (b"integer", True, filekind.ValueType),
                                (b"text", b"\xc3", filekind.InvalidText), (b"text", b"\xff\xfe", filekind.InvalidText)):
            with self.assertRaises(err, msg=(cls, value)):
                _cell(cls, value)

    def test_refuses_keys_that_tie(self):
        conn = connect(self.tmp.file("k.db"))
        for table, rows in (("d", [("p", "one"), ("p", "two")]), ("m", [(1, "int"), (1.0, "real")]),
                            ("n", [(None, "a"), (None, "b")]), ("u", [("p", "one"), ("P", "two")])):
            conn.execute(f"CREATE TABLE {table} (k, v TEXT)")
            conn.executemany(f"INSERT INTO {table} VALUES (?, ?)", rows)
        for table in ("d", "m", "n"):
            with self.assertRaises(filekind.KeyNotUnique, msg=table):
                filekind.digest_rows(conn, filekind.RowSet(kind="fixture", set=table, table=table, columns=("v",), key=("k",)))
        filekind.digest_rows(conn, filekind.RowSet(kind="fixture", set="u", table="u", columns=("v",), key=("k",)))

    def test_refuses_what_is_not_a_base_table(self):
        conn = connect(self.tmp.file("b.db"))
        example_table(conn)
        conn.execute("CREATE VIEW v AS SELECT * FROM example")
        for table in ("v", "missing", "sqlite_schema"):
            with self.assertRaises(filekind.NotBaseTable, msg=table):
                filekind.digest_rows(conn, filekind.RowSet(kind="fixture", set="s", table=table, columns=("v",), key=("k",)))
        filekind.digest_rows(conn, filekind.RowSet(kind="fixture", set="example", table="EXAMPLE", columns=("k", "v"), key=("k",)))

    def test_refuses_a_malformed_set(self):
        conn = connect(self.tmp.file("m.db"))
        example_table(conn)
        for rs in (
            filekind.RowSet(kind=0xEE, set="s", table="example", columns=("v",), key=("k",)),
            filekind.RowSet(kind="fixture", set="", table="example", columns=("v",), key=("k",)),
            filekind.RowSet(kind="fixture", set="s", table="", columns=("v",), key=("k",)),
            filekind.RowSet(kind="fixture", set="s", table="example", columns=(), key=("k",)),
            filekind.RowSet(kind="fixture", set="s", table="example", columns=("v",), key=()),
            filekind.RowSet(kind="fixture", set="s", table="example", columns=("v\x00",), key=("k",)),
            filekind.RowSet(kind="fixture", set="\udcff", table="example", columns=("v",), key=("k",)),
            filekind.RowSet(kind="fixture", set="s", table="example", columns="v", key=("k",)),
        ):
            with self.assertRaises(filekind.InvalidArgument, msg=rs):
                filekind.digest_rows(conn, rs)

    def test_where_selects_rows(self):
        conn = connect(self.tmp.file("w.db"))
        example_table(conn)
        rs = filekind.RowSet(kind="fixture", set="example", table="example", columns=("k", "v"), key=("k",),
                             where="k > ?", args=(1,))
        self.assertEqual(filekind.digest_rows(conn, rs).rows, 1)

    def test_leaves_the_connection_text_factory(self):
        conn = connect(self.tmp.file("f.db"))
        example_table(conn)
        filekind.digest_rows(conn, EXAMPLE)
        self.assertIs(conn.text_factory, str)


class Values(unittest.TestCase):
    def test_check_values(self):
        tmp = TempDir()
        cases = [
            ("CREATE TABLE e (id INTEGER PRIMARY KEY, body TEXT, trits BLOB)", "INSERT INTO e VALUES (1, '{}', x'79')", None),
            ("CREATE TABLE e (id INTEGER PRIMARY KEY, n BLOB)", "INSERT INTO e VALUES (1, 5)", "e.n"),
            ("CREATE TABLE e (id INTEGER PRIMARY KEY, n)", "INSERT INTO e VALUES (1, 2.5)", "e.n"),
            ("CREATE TABLE e (id INTEGER PRIMARY KEY, v TEXT) WITHOUT ROWID", "INSERT INTO e VALUES (1, 'x')", "e.id"),
            ("CREATE TABLE e (id INT PRIMARY KEY, v TEXT)", "INSERT INTO e VALUES (1, 'x')", "e.id"),
            ("CREATE TABLE e (a INTEGER, b TEXT, PRIMARY KEY (a, b))", "INSERT INTO e VALUES (1, 'x')", "e.a"),
        ]
        for i, (schema, insert, detail) in enumerate(cases):
            conn = connect(tmp.file(f"v{i}.db"))
            conn.execute(schema)
            conn.execute(insert)
            if detail is None:
                filekind.check_values(conn, "runEvents")
            else:
                with self.assertRaises(filekind.BinaryNumber) as cm:
                    filekind.check_values(conn, "runEvents")
                self.assertTrue(str(cm.exception).endswith(detail), cm.exception)
            filekind.check_values(conn, "fixture")
        with self.assertRaises(filekind.InvalidArgument):
            filekind.check_values(connect(tmp.file("x.db")), 0xEE)


class CanonicalJSON(unittest.TestCase):
    def test_forms(self):
        self.assertEqual(filekind.canonical_json({"tf": [7, -3], "decimal": 0.5}), '{"decimal":0.5,"tf":[7,-3]}')
        self.assertEqual(filekind.canonical_json({"b": (1, 2.0), "a": None}), '{"a":null,"b":[1,2.0]}')
        self.assertEqual(filekind.canonical_json(" \x07/"), '" \\u0007/"')
        for bad in (float("nan"), {1: 2}, {"a": object()}, "\ud800", 10 ** 4301):
            with self.assertRaises(filekind.InvalidJSON, msg=type(bad).__name__):
                filekind.canonical_json(bad)
        for bad in ('{"a":1,"a":2}', "NaN", "[Infinity]", b'"\xff"', "1 2"):
            with self.assertRaises(filekind.InvalidJSON, msg=bad):
                filekind.canonical_json_text(bad)


if __name__ == "__main__":
    unittest.main()
