"""Several processes appending to one file at once: none loses or tears a
row, the schema is created once, and the header is the kind asked."""

from __future__ import annotations

import json
import os
import subprocess
import sys
import time
import unittest

import filekind
import filekind.writer

from _support import HERE, TempDir, connect

WRITERS = 6
ROWS = 150
SCHEMA = ["CREATE TABLE log (id INTEGER PRIMARY KEY, n INTEGER NOT NULL UNIQUE, writer TEXT NOT NULL, "
          "seq TEXT NOT NULL, body TEXT NOT NULL, UNIQUE (writer, seq))"]

CHILD = r"""
import os, sys, time
sys.path.insert(0, sys.argv[1])
import filekind
from filekind.writer import Writer
path, go, name, rows = sys.argv[2], sys.argv[3], sys.argv[4], int(sys.argv[5])
schema = [sys.argv[6]]
while not os.path.exists(go):
    time.sleep(0.001)
with Writer(path, "fixture", 1, schema, busy_timeout_ms=60000) as w:
    for seq in range(rows):
        body = filekind.canonical_json({"writer": name, "seq": str(seq), "pad": "x" * 200})
        with w.transaction() as c:
            # Read, then write: only a transaction that holds the write lock
            # from its start keeps two writers from taking the same n.
            n = c.execute("SELECT coalesce(max(n), 0) + 1 FROM log").fetchone()[0]
            c.execute("INSERT INTO log (n, writer, seq, body) VALUES (?, ?, ?, ?)", (n, name, str(seq), body))
"""


class SeveralWriters(unittest.TestCase):
    def test_processes_append_at_once(self):
        tmp = TempDir()
        path = tmp.file("shared.db")
        go = tmp.file("go")
        package = os.path.normpath(os.path.join(HERE, ".."))
        procs = [
            subprocess.Popen([sys.executable, "-c", CHILD, package, path, go, f"w{i}", str(ROWS), SCHEMA[0]],
                             stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            for i in range(WRITERS)
        ]
        time.sleep(0.5)
        open(go, "w").close()
        for p in procs:
            out, err = p.communicate(timeout=600)
            self.assertEqual(p.returncode, 0, err.decode(errors="replace"))

        conn = connect(path)
        self.assertEqual(filekind.check(conn, filekind.Want("fixture", 1)), filekind.Header(filekind.application_id("fixture"), 1))
        self.assertEqual(conn.execute("PRAGMA integrity_check").fetchone()[0], "ok")
        self.assertEqual(conn.execute("PRAGMA journal_mode").fetchone()[0], "wal")
        tables = [r[0] for r in conn.execute("SELECT name FROM sqlite_schema WHERE type = 'table'")]
        self.assertEqual(tables, ["log"])
        rows = conn.execute("SELECT writer, seq, body FROM log").fetchall()
        self.assertEqual(len(rows), WRITERS * ROWS)
        numbers = [r[0] for r in conn.execute("SELECT n FROM log ORDER BY n")]
        self.assertEqual(numbers, list(range(1, WRITERS * ROWS + 1)), "two writers took one sequence number")
        seen = set()
        for writer, seq, body in rows:
            record = json.loads(body)
            self.assertEqual((record["writer"], record["seq"]), (writer, seq), "a torn row")
            self.assertEqual(filekind.canonical_json(record), body)
            seen.add((writer, seq))
        self.assertEqual(seen, {(f"w{i}", str(s)) for i in range(WRITERS) for s in range(ROWS)})
        filekind.checkpoint(conn)
        self.assertEqual(filekind.read_file_header(path), filekind.Header(filekind.application_id("fixture"), 1))

    def test_a_transaction_rolls_back_on_an_exception(self):
        tmp = TempDir()
        with filekind.writer.Writer(tmp.file("w.db"), "fixture", 1, SCHEMA) as w:
            with self.assertRaises(RuntimeError):
                with w.transaction() as c:
                    c.execute("INSERT INTO log (n, writer, seq, body) VALUES (1, 'a', '0', '{}')")
                    raise RuntimeError("stop")
            self.assertEqual(w.conn.execute("SELECT count(*) FROM log").fetchone()[0], 0)
            self.assertFalse(w.conn.in_transaction)


if __name__ == "__main__":
    unittest.main()
