"""The base-3 helpers against orbitra's codec, generated at test time.

No base-3 vector is kept as a fixed copy: the codec's rules can change, so
every expected value here is computed by calling orbitra itself. Without
orbitra the calls must say so, and the rest of the package must not need it."""

from __future__ import annotations

import importlib.util
import subprocess
import sys
import unittest

import filekind
from filekind import base3

from _support import HERE, TempDir, connect

HAVE_ORBITRA = importlib.util.find_spec("orbitra") is not None


class WithoutOrbitra(unittest.TestCase):
    def test_import_filekind_never_loads_orbitra(self):
        code = "import sys, filekind; print('orbitra' in sys.modules or 'numpy' in sys.modules)"
        out = subprocess.run([sys.executable, "-c", code], capture_output=True, text=True, check=True,
                             cwd=HERE + "/..")
        self.assertEqual(out.stdout.strip(), "False")

    def test_a_call_says_orbitra_is_missing(self):
        code = ("import sys; sys.modules['orbitra'] = None\n"
                "from filekind import base3\n"
                "try:\n    base3.trits_blob([1, 0, -1])\nexcept base3.Base3Unavailable as e:\n    print('unavailable', 'orbitra' in str(e))\n")
        out = subprocess.run([sys.executable, "-c", code], capture_output=True, text=True, check=True, cwd=HERE + "/..")
        self.assertEqual(out.stdout.strip(), "unavailable True")


@unittest.skipUnless(HAVE_ORBITRA, "orbitra is not installed here")
class WithOrbitra(unittest.TestCase):
    def setUp(self):
        import numpy as np
        from orbitra.ternary import codec, trit_float_json, trit_words

        self.np, self.codec, self.tfj, self.words = np, codec, trit_float_json, trit_words

    def test_each_helper_is_the_codec(self):
        np = self.np
        trits = np.array([1, 0, -1, 1, 1, 0, -1], dtype=np.int8)
        self.assertEqual(base3.trits_blob(trits), self.codec.pack(trits).tobytes())
        self.assertEqual(base3.trits_from_blob(base3.trits_blob(trits), len(trits)).tolist(), trits.tolist())
        ints = np.array([[3, -7], [100, 0]], dtype=np.int64)
        self.assertEqual(base3.words_blob(ints), self.words.encode_words(ints).tobytes())
        self.assertEqual(base3.words_from_blob(base3.words_blob(ints)).tolist(), ints.tolist())
        record = {"loss": 0.25, "steps": [1, 2], "name": "run"}
        self.assertEqual(base3.record_text(record), filekind.canonical_json(self.tfj.floats_to_json(record)))
        self.assertEqual(base3.record_from_text(base3.record_text(record)), record)

    def test_a_base3_file_passes_the_value_rule_and_digests_stably(self):
        np = self.np
        tmp = TempDir()
        rows = [(f"r{i}", base3.trits_blob(np.array([1, -1, 0, i % 3 - 1], dtype=np.int8)),
                 base3.record_text({"x": i / 8, "label": f"v{i}"})) for i in range(20)]
        digests = []
        for name, order in (("a.db", rows), ("b.db", list(reversed(rows)))):
            conn = connect(tmp.file(name))
            filekind.create(conn, "runEvents", 1,
                            ["CREATE TABLE r (name TEXT PRIMARY KEY, trits BLOB NOT NULL, record TEXT NOT NULL) WITHOUT ROWID"])
            conn.executemany("INSERT INTO r VALUES (?, ?, ?)", order)
            filekind.check_values(conn, "runEvents")
            digests.append(filekind.digest_rows(conn, filekind.RowSet(
                kind="runEvents", set="r", table="r", columns=("name", "trits", "record"), key=("name",))))
        self.assertEqual(digests[0], digests[1])
        self.assertEqual(digests[0].rows, 20)


if __name__ == "__main__":
    unittest.main()
