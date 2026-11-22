"""A writer for a file several processes append to at once.

Each process opens its own Writer on the same path. The file is put in WAL
mode, every connection waits for the write lock rather than failing
(busy_timeout), the first process to arrive creates the schema and stamps the
header inside one exclusive transaction (create), and every later write is a
short BEGIN IMMEDIATE transaction. A multi-writer file lives on a local disk,
never a network or synced folder, and every writer of a live file uses the
same engine (SPEC.md, "Several writers")."""

from __future__ import annotations

import sqlite3
import time
from contextlib import contextmanager

from . import _errors as E
from ._header import Header, create


def _switch_to_wal(conn, deadline: float) -> str:
    """Put the file in WAL mode, waiting out other processes. The switch needs
    the file to itself, and SQLite answers "database is locked" at once,
    without its busy handler, while another process is opening or creating the
    same new file, so the switch is retried until the busy timeout runs out."""
    pause = 0.005
    while True:
        try:
            return conn.execute("PRAGMA journal_mode = WAL").fetchone()[0]
        except sqlite3.OperationalError as err:
            if "locked" not in str(err) and "busy" not in str(err) or time.monotonic() >= deadline:
                raise
        time.sleep(pause)
        pause = min(pause * 2, 0.2)


class Writer:
    """One process's connection to a multi-writer file of `kind` at
    `generation`. `schema` is the list of statements the creating process
    runs; every process passes the same list."""

    def __init__(self, path, kind, generation: int, schema=(), busy_timeout_ms: int = 30000):
        if isinstance(busy_timeout_ms, bool) or not isinstance(busy_timeout_ms, int) or busy_timeout_ms < 1:
            raise E.InvalidArgument("busy_timeout_ms must be a whole number of milliseconds from 1")
        self.conn = sqlite3.connect(path, timeout=busy_timeout_ms / 1000, isolation_level=None)
        try:
            self.conn.execute(f"PRAGMA busy_timeout = {busy_timeout_ms}")
            mode = _switch_to_wal(self.conn, time.monotonic() + busy_timeout_ms / 1000)
            if str(mode).lower() != "wal":
                raise E.NotWAL(f"journal_mode is {mode}")
            self.header: Header = create(self.conn, kind, generation, schema)
        except BaseException:
            self.conn.close()
            raise

    @contextmanager
    def transaction(self):
        """A short write transaction: BEGIN IMMEDIATE takes the write lock at
        once (waiting up to the busy timeout), so two writers never both read
        and then race to write; it commits on exit and rolls back on an
        exception."""
        self.conn.execute("BEGIN IMMEDIATE")
        try:
            yield self.conn
        except BaseException:
            self.conn.execute("ROLLBACK")
            raise
        self.conn.execute("COMMIT")

    def close(self) -> None:
        self.conn.close()

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.close()
