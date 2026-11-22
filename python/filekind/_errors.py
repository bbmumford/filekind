"""The helper's refusals. Each class is named as SPEC.md's "Refusals" table and
the golden vectors name the outcome, and matches one Go sentinel error."""

from __future__ import annotations


class FileKindError(Exception):
    """Every refusal this package raises."""


class HeaderRefusal(FileKindError):
    """A header refused: `found` is the (application_id, user_version) the file
    holds and `want` the expectation it failed. It carries no path."""

    def __init__(self, message: str, found=None, want=None):
        super().__init__(message)
        self.found = found
        self.want = want


class Unstamped(HeaderRefusal):
    """The application_id is zero, so the file names no kind."""


class ForeignFile(HeaderRefusal):
    """The application_id lies outside the 'ORB' prefix, or a stamp would take
    over a user_version another scheme already set."""


class UnknownKind(HeaderRefusal):
    """The 'ORB' prefix with a kind byte the registry does not hold."""


class WrongKind(HeaderRefusal):
    """A registered kind other than the one expected."""


class NoGeneration(HeaderRefusal):
    """The right kind with a user_version below 1."""


class NewerGeneration(HeaderRefusal):
    """A format generation newer than the reader reads."""


class OlderGeneration(HeaderRefusal):
    """A format generation older than the reader reads."""


class GenerationBackwards(HeaderRefusal):
    """A stamp would lower a file's format generation."""


class StampNotHeld(HeaderRefusal):
    """The engine accepted a stamp and then read back other values."""


class CheckpointBusy(FileKindError):
    """The checkpoint could not move every WAL frame into the main file."""


class UnreadableHeader(FileKindError):
    """A header pragma did not answer one integer."""


class NotPlainFile(FileKindError):
    """read_file_header found neither an empty file nor the plaintext magic."""


class InvalidArgument(FileKindError, ValueError):
    """An unregistered kind, a generation below 1, or a malformed row set."""


class NotBaseTable(FileKindError):
    """A row set names a view, a virtual table, an engine table or nothing."""


class KeyNotUnique(FileKindError):
    """Two rows of a row set tie on its key."""


class ValueType(FileKindError):
    """A value disagrees with the storage class typeof reported."""


class InvalidText(FileKindError):
    """A TEXT value is not valid UTF-8."""


class BinaryNumber(FileKindError):
    """A base-3 kind holds an INTEGER or REAL value."""


class InvalidJSON(FileKindError, ValueError):
    """A document canonical_json cannot hold."""


class NotWAL(FileKindError):
    """A multi-writer file could not be put in WAL mode."""


# The outcome names the golden vectors use, mapped to the class raised.
OUTCOMES = {
    cls.__name__: cls
    for cls in (
        Unstamped, ForeignFile, UnknownKind, WrongKind, NoGeneration, NewerGeneration,
        OlderGeneration, GenerationBackwards, StampNotHeld, CheckpointBusy, UnreadableHeader,
        NotPlainFile, InvalidArgument, NotBaseTable, KeyNotUnique, ValueType, InvalidText,
        BinaryNumber, InvalidJSON, NotWAL,
    )
}
