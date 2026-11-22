"""The kinds registry: kinds.tsv, read once from this package's copy.

The repository's root kinds.tsv is the registry; this package carries a
byte-identical copy so an installed helper has it. The Go and Python tests both
fail when the two differ."""

from __future__ import annotations

from dataclasses import dataclass
from functools import lru_cache
from importlib import resources

from ._errors import InvalidArgument

PREFIX = 0x4F524200
HEADER = "kind\tname\tlane\tgeneration\tencrypted\tvalues\tuse"
VALUES = ("sql", "base3", "radix")


@dataclass(frozen=True)
class Entry:
    kind: int
    name: str
    lane: str
    generation: int
    encrypted: bool
    values: str
    use: str

    @property
    def application_id(self) -> int:
        return PREFIX | self.kind


def parse(text: str) -> tuple[Entry, ...]:
    """Parse kinds.tsv by the rules the Go helper's parseRegistry applies."""
    lines = text[:-1].split("\n") if text.endswith("\n") else text.split("\n")
    if not lines or lines[0] != HEADER:
        raise InvalidArgument(f"kinds.tsv header is not {HEADER!r}")
    entries = []
    names = set()
    last = -1
    for row, line in enumerate(lines[1:], start=2):
        cols = line.split("\t")
        if len(cols) != 7:
            raise InvalidArgument(f"kinds.tsv row {row} has {len(cols)} columns, want 7")
        k, name, lane, generation, encrypted, values, use = cols
        if len(k) != 2 or k.lower() != k or any(c not in "0123456789abcdef" for c in k):
            raise InvalidArgument(f"kinds.tsv row {row} kind {k!r} is not two lower-case hex digits")
        byte = int(k, 16)
        if byte == 0:
            raise InvalidArgument(f"kinds.tsv row {row} kind {k!r} is not a byte from 01 to ff")
        if byte <= last:
            raise InvalidArgument(f"kinds.tsv row {row} kind {k} does not ascend")
        last = byte
        if not name or name in names:
            raise InvalidArgument(f"kinds.tsv row {row} name {name!r} is empty or repeated")
        names.add(name)
        if not generation.isdigit() or int(generation) < 1 or int(generation) > 0x7FFFFFFF:
            raise InvalidArgument(f"kinds.tsv row {row} generation {generation!r} is not a whole number from 1")
        if encrypted not in ("yes", "no"):
            raise InvalidArgument(f"kinds.tsv row {row} encrypted {encrypted!r} is not yes or no")
        if values not in VALUES:
            raise InvalidArgument(f"kinds.tsv row {row} values {values!r} is not sql, base3 or radix")
        if not lane or not use:
            raise InvalidArgument(f"kinds.tsv row {row} has an empty lane or use")
        entries.append(Entry(byte, name, lane, int(generation), encrypted == "yes", values, use))
    return tuple(entries)


def registry_text() -> str:
    return resources.files(__package__).joinpath("kinds.tsv").read_text(encoding="utf-8")


@lru_cache(maxsize=1)
def registry() -> tuple[Entry, ...]:
    """Every registered kind in ascending byte order."""
    return parse(registry_text())


def lookup(kind) -> Entry | None:
    """The row for a kind given as its byte or its name."""
    for entry in registry():
        if entry.kind == kind or entry.name == kind:
            return entry
    return None


def kind_byte(kind) -> int:
    """A kind given as its byte or its name, refused unless registered."""
    if isinstance(kind, bool):
        raise InvalidArgument(f"kind {kind!r} is not registered")
    entry = lookup(kind)
    if entry is None:
        raise InvalidArgument(f"kind {kind!r} is not registered")
    return entry.kind


def application_id(kind) -> int:
    return PREFIX | kind_byte(kind)


def kind_of(app_id: int) -> int | None:
    """The kind byte of an application_id, or None outside the 'ORB' prefix."""
    value = app_id & 0xFFFFFFFF
    if value & 0xFFFFFF00 != PREFIX:
        return None
    return value & 0xFF
