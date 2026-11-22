"""Shared test support: the repository's vectors, and fixture copies so no
test ever writes a fixture."""

from __future__ import annotations

import json
import os
import shutil
import sqlite3
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.normpath(os.path.join(HERE, "..", ".."))
VECTORS = os.path.join(REPO, "testdata", "vectors")


def load_vectors():
    with open(os.path.join(VECTORS, "vectors.json"), encoding="utf-8") as f:
        return json.load(f)


class TempDir:
    def __init__(self):
        self.path = tempfile.mkdtemp(prefix="filekind-test-")

    def file(self, name):
        return os.path.join(self.path, name)

    def fixture(self, name):
        dest = self.file(name)
        shutil.copyfile(os.path.join(VECTORS, name), dest)
        return dest


def connect(path, **kw):
    return sqlite3.connect(path, isolation_level=None, **kw)


def outcome(fn):
    """The outcome word for calling fn: 'ok' or the refusal's class name."""
    import filekind

    try:
        fn()
    except filekind.FileKindError as err:
        return type(err).__name__
    return "ok"
