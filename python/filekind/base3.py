"""Base-3 values for the kinds whose registry row says base3.

Every function here calls the ternary codec in orbitra.ternary and carries no
codec of its own: packed trits through codec, integer arrays as balanced trit
words through trit_words, and run records (text and JSON with floats as exact
base-3 pairs and counts as trit records) through trit_float_json. orbitra is
an optional dependency (the `base3` extra); it is imported on first use, so
`import filekind` never needs it, and a call without it raises Base3Unavailable
saying so. What these functions return is a BLOB (bytes) or a TEXT (str) value
to store; the row digest reads it like any other blob or text."""

from __future__ import annotations

import importlib
import json

from ._errors import FileKindError
from ._json import canonical_json


class Base3Unavailable(FileKindError, ImportError):
    """orbitra, the ternary codec package, is not importable here."""


def _ternary(module: str):
    try:
        return importlib.import_module("orbitra.ternary." + module)
    except ImportError as err:
        raise Base3Unavailable(
            "filekind.base3 calls the ternary codec in orbitra.ternary, which is not installed: "
            "install orbitra (pip install -e <its python directory>) or the filekind[base3] extra"
        ) from err


def _numpy():
    try:
        import numpy
    except ImportError as err:
        raise Base3Unavailable("filekind.base3 needs numpy, which orbitra's codec uses") from err
    return numpy


def trits_blob(trits) -> bytes:
    """Balanced trits (-1, 0, +1) packed five to a byte by orbitra's codec."""
    return bytes(_ternary("codec").pack(trits).tobytes())


def trits_from_blob(blob: bytes, count: int):
    """The `count` trits a trits_blob holds, as orbitra's codec unpacks them."""
    np = _numpy()
    return _ternary("codec").unpack(np.frombuffer(bytes(blob), dtype=np.uint8), count)


def words_blob(values) -> bytes:
    """An integer array as orbitra's balanced trit words, shape included."""
    return bytes(_ternary("trit_words").encode_words(values).tobytes())


def words_from_blob(blob: bytes):
    """The integer array a words_blob holds."""
    np = _numpy()
    return _ternary("trit_words").decode_words(np.frombuffer(bytes(blob), dtype=np.uint8))


def record_text(value) -> str:
    """A JSON-shaped run record as canonical JSON TEXT, every float replaced
    by its exact base-3 pair (trit_float_json.floats_to_json)."""
    return canonical_json(_ternary("trit_float_json").floats_to_json(value))


def record_from_text(text: str):
    """The record a record_text holds, its pairs read back as floats."""
    return _ternary("trit_float_json").floats_from_json(json.loads(text))


def count_text(n: int) -> str:
    """A whole number as its trit count record, canonical JSON TEXT."""
    return canonical_json(_ternary("trit_float_json").count_to_json(n))


def count_from_text(text: str, source: str) -> int:
    """The whole number a count_text holds; `source` names where it was read."""
    return _ternary("trit_float_json").count_from_json(json.loads(text), source=source)
