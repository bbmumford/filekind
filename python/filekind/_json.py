"""The canonical JSON form a TEXT column holds JSON in, so equal bodies are
equal bytes and digest equal: keys sorted, no whitespace, UTF-8, floats as
their repr. The Go helper's CanonicalJSON writes the same bytes."""

from __future__ import annotations

import json
import math

from . import _errors as E

# Python 3.11 and later refuse to read or write an integer of more than 4300
# digits; the bound is compared as a magnitude, since str() of such an integer
# is itself what raises.
_INT_BOUND = 10 ** 4300


def _refuse_constant(name):
    raise E.InvalidJSON(f"{name} is not a JSON number")


def _pairs(pairs):
    out = {}
    for key, value in pairs:
        if key in out:
            raise E.InvalidJSON(f"repeated key {key!r}")
        out[key] = value
    return out


def _check(value) -> None:
    if value is None or isinstance(value, (bool, str, int)):
        if isinstance(value, int) and not isinstance(value, bool) and abs(value) >= _INT_BOUND:
            raise E.InvalidJSON("integer of more than 4300 digits")
        return
    if isinstance(value, float):
        if math.isnan(value) or math.isinf(value):
            raise E.InvalidJSON("a non-finite float has no JSON form")
        return
    if isinstance(value, (list, tuple)):
        for item in value:
            _check(item)
        return
    if isinstance(value, dict):
        for key, item in value.items():
            if not isinstance(key, str):
                raise E.InvalidJSON(f"object key {key!r} is not a string")
            _check(item)
        return
    raise E.InvalidJSON(f"{type(value).__name__} has no JSON form")


def canonical_json(value) -> str:
    """The canonical text of a JSON-shaped value (dict, list, tuple, str,
    int, float, bool, None)."""
    _check(value)
    text = json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False, allow_nan=False)
    try:
        text.encode("utf-8")
    except UnicodeEncodeError:
        raise E.InvalidJSON("a string holds an unpaired surrogate") from None
    return text


def canonical_json_text(doc) -> str:
    """Rewrite one JSON document (str, or bytes read as strict UTF-8) in the
    canonical form, refusing repeated keys and NaN or Infinity."""
    if isinstance(doc, (bytes, bytearray)):
        try:
            doc = bytes(doc).decode("utf-8")
        except UnicodeDecodeError:
            raise E.InvalidJSON("not UTF-8") from None
    try:
        value = json.loads(doc, object_pairs_hook=_pairs, parse_constant=_refuse_constant)
    except E.InvalidJSON:
        raise
    except ValueError as err:
        raise E.InvalidJSON(str(err)) from None
    return canonical_json(value)
