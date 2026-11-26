"""filekind: the identity header, the kinds registry and the canonical row
digest of a single-file SQLite format, over the standard library's
sqlite3 (SPEC.md in the repository holds the rules).

Base-3 value helpers live in filekind.base3 and import orbitra only when
called; the multi-writer writer lives in filekind.writer."""

from ._errors import (
    BinaryNumber, CheckpointBusy, FileKindError, ForeignFile, GenerationBackwards, HeaderRefusal,
    InvalidArgument, InvalidJSON, InvalidText, KeyNotCovered, KeyNotUnique, NewerGeneration, NoGeneration, NotBaseTable,
    NotIntact, NotPlainFile, NotWAL, OlderGeneration, OUTCOMES, StampNotHeld, UnknownKind, Unstamped,
    UnreadableHeader, UnsafeOpen, ValueType, WrongKind,
)
from ._digest import Digest, RowSet, digest_rows, encode_rows, query
from ._header import (
    Header, Want, check, checkpoint, create, expect, read_file_header, read_header, set_header, stamp, verify,
)
from ._json import canonical_json, canonical_json_text
from ._registry import PREFIX, Entry, application_id, kind_byte, kind_of, lookup, registry
from ._values import check_values

__all__ = [
    "BinaryNumber", "CheckpointBusy", "Digest", "Entry", "FileKindError", "ForeignFile",
    "GenerationBackwards", "Header", "HeaderRefusal", "InvalidArgument", "InvalidJSON", "InvalidText",
    "KeyNotCovered", "KeyNotUnique", "NewerGeneration", "NoGeneration", "NotBaseTable", "NotIntact", "NotPlainFile", "NotWAL",
    "OUTCOMES", "OlderGeneration", "PREFIX", "RowSet", "StampNotHeld", "UnknownKind", "Unstamped",
    "UnreadableHeader", "UnsafeOpen", "ValueType", "Want", "WrongKind", "application_id", "canonical_json",
    "canonical_json_text", "check", "check_values", "checkpoint", "create", "digest_rows", "encode_rows",
    "expect", "kind_byte", "kind_of", "lookup", "query", "read_file_header", "read_header", "registry",
    "set_header", "stamp", "verify",
]
