"""The golden vectors, which the Go helper's tests also reproduce."""

from __future__ import annotations

import json
import os
import unittest

import filekind
from filekind._blake3 import pure_blake3

from _support import REPO, VECTORS, TempDir, connect, load_vectors, outcome


class GoldenVectors(unittest.TestCase):
    def setUp(self):
        self.v = load_vectors()
        self.tmp = TempDir()

    def test_registry_copy_matches_the_registry(self):
        with open(os.path.join(REPO, "kinds.tsv"), "rb") as f:
            root = f.read()
        self.assertEqual(filekind._registry.registry_text().encode("utf-8"), root,
                         "python/filekind/kinds.tsv differs from kinds.tsv; copy kinds.tsv over it")

    def test_headers(self):
        for case in self.v["headers"]:
            path = self.tmp.fixture(case["file"])
            want = filekind.Header(case["header"]["applicationId"], case["header"]["userVersion"])
            self.assertEqual(filekind.read_file_header(path), want, case["file"])
            conn = connect(path)
            self.assertEqual(filekind.read_header(conn), want, case["file"])
            for c in case["checks"]:
                got = outcome(lambda: filekind.check(conn, filekind.Want(c["kind"], c["newest"], c["oldest"])))
                self.assertEqual(got, c["outcome"], f'{case["file"]} {c}')
            conn.close()

    def test_digests(self):
        for i, case in enumerate(self.v["digests"]):
            rs = case["rowSet"]
            rowset = filekind.RowSet(kind=rs["kind"], set=rs["set"], table=rs["table"], columns=tuple(rs["columns"]),
                                     key=tuple(rs["key"]), where=rs.get("where", ""), args=tuple(rs.get("args", ())))
            conn = connect(self.tmp.fixture(case["file"]))
            label = f'digest {i} ({case["file"]} {rs["set"]})'
            if "outcome" in case:
                self.assertEqual(outcome(lambda: filekind.digest_rows(conn, rowset)), case["outcome"], label)
                self.assertEqual(outcome(lambda: filekind.encode_rows(conn, rowset)), case["outcome"], label)
                continue
            stream = filekind.encode_rows(conn, rowset)
            if "streamHex" in case:
                self.assertEqual(stream.hex(), case["streamHex"], label)
            d = filekind.digest_rows(conn, rowset)
            self.assertEqual((d.sha256_hex, d.blake3_hex, d.rows), (case["sha256"], case["blake3"], case["rows"]), label)
            conn.close()

    def test_values(self):
        for case in self.v["values"]:
            conn = connect(self.tmp.fixture(case["file"]))
            got = outcome(lambda: filekind.check_values(conn, case["kind"]))
            self.assertEqual(got, case["outcome"], case["file"])
            if case.get("detail"):
                with self.assertRaises(filekind.BinaryNumber) as cm:
                    filekind.check_values(conn, case["kind"])
                self.assertIn(case["detail"], str(cm.exception))
            conn.close()

    def test_json(self):
        for i, case in enumerate(self.v["json"]):
            doc = case["input"] if "input" in case else bytes.fromhex(case["inputHex"])
            if "outcome" in case:
                self.assertEqual(outcome(lambda: filekind.canonical_json_text(doc)), case["outcome"], f"json {i}")
                continue
            self.assertEqual(filekind.canonical_json_text(doc), case["canonical"], f"json {i}")

    def test_blake3(self):
        with open(os.path.join(VECTORS, "blake3.json"), encoding="utf-8") as f:
            vectors = json.load(f)
        self.assertEqual(vectors[0], {"length": 0, "hash": "af1349b9f5f9a1a6a0404dea36dcc9499bcb25c9adc112b7cc9a93cae41f3262"})
        for case in vectors:
            data = bytes(i % 251 for i in range(case["length"]))
            self.assertEqual(pure_blake3(data).hexdigest(), case["hash"], case["length"])
            # The same input fed in uneven pieces.
            h = pure_blake3()
            for start in range(0, len(data), 777):
                h.update(data[start:start + 777])
            self.assertEqual(h.hexdigest(), case["hash"], case["length"])


if __name__ == "__main__":
    unittest.main()
