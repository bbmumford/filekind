"""BLAKE3 for the row digest, with nothing outside the standard library.

hashlib has no BLAKE3, so this module carries the hash function itself: the
standard 32-byte unkeyed hash, written from the BLAKE3 specification's
reference implementation. When the `blake3` package is installed (the
`fast` extra) its hasher is used instead; the tests hold both to the same
vectors. It is the hash, not a codec: the row stream it digests is SPEC.md's."""

from __future__ import annotations

import struct

_IV = (0x6A09E667, 0xBB67AE85, 0x3C6EF372, 0xA54FF53A, 0x510E527F, 0x9B05688C, 0x1F83D9AB, 0x5BE0CD19)
_PERMUTATION = (2, 6, 3, 10, 7, 0, 4, 13, 1, 11, 12, 5, 9, 14, 15, 8)
_BLOCK_LEN = 64
_CHUNK_LEN = 1024
_CHUNK_START = 1
_CHUNK_END = 2
_PARENT = 4
_ROOT = 8
_M = 0xFFFFFFFF


def _compress(cv, block, counter, block_len, flags):
    s = [cv[0], cv[1], cv[2], cv[3], cv[4], cv[5], cv[6], cv[7],
         _IV[0], _IV[1], _IV[2], _IV[3],
         counter & _M, (counter >> 32) & _M, block_len, flags]
    m = list(block)
    for r in range(7):
        for a, b, c, d, x, y in ((0, 4, 8, 12, 0, 1), (1, 5, 9, 13, 2, 3), (2, 6, 10, 14, 4, 5),
                                 (3, 7, 11, 15, 6, 7), (0, 5, 10, 15, 8, 9), (1, 6, 11, 12, 10, 11),
                                 (2, 7, 8, 13, 12, 13), (3, 4, 9, 14, 14, 15)):
            sa = (s[a] + s[b] + m[x]) & _M
            sd = s[d] ^ sa
            sd = ((sd >> 16) | (sd << 16)) & _M
            sc = (s[c] + sd) & _M
            sb = s[b] ^ sc
            sb = ((sb >> 12) | (sb << 20)) & _M
            sa = (sa + sb + m[y]) & _M
            sd ^= sa
            sd = ((sd >> 8) | (sd << 24)) & _M
            sc = (sc + sd) & _M
            sb ^= sc
            sb = ((sb >> 7) | (sb << 25)) & _M
            s[a], s[b], s[c], s[d] = sa, sb, sc, sd
        if r < 6:
            m = [m[i] for i in _PERMUTATION]
    for i in range(8):
        s[i] ^= s[i + 8]
        s[i + 8] ^= cv[i]
    return s


def _words(block: bytes):
    return struct.unpack("<16I", block)


class _Output:
    __slots__ = ("cv", "block", "counter", "block_len", "flags")

    def __init__(self, cv, block, counter, block_len, flags):
        self.cv, self.block, self.counter, self.block_len, self.flags = cv, block, counter, block_len, flags

    def chaining_value(self):
        return _compress(self.cv, self.block, self.counter, self.block_len, self.flags)[:8]

    def root_bytes(self, length):
        out = bytearray()
        counter = 0
        while len(out) < length:
            out += struct.pack("<16I", *_compress(self.cv, self.block, counter, self.block_len,
                                                   self.flags | _ROOT))
            counter += 1
        return bytes(out[:length])


class _Chunk:
    __slots__ = ("cv", "counter", "block", "blocks", "flags")

    def __init__(self, key, counter, flags):
        self.cv = list(key)
        self.counter = counter
        self.block = bytearray()
        self.blocks = 0
        self.flags = flags

    def length(self):
        return _BLOCK_LEN * self.blocks + len(self.block)

    def _start(self):
        return _CHUNK_START if self.blocks == 0 else 0

    def update(self, data: memoryview):
        while data:
            if len(self.block) == _BLOCK_LEN:
                self.cv = _compress(self.cv, _words(bytes(self.block)), self.counter, _BLOCK_LEN,
                                    self.flags | self._start())[:8]
                self.blocks += 1
                self.block = bytearray()
            take = min(_BLOCK_LEN - len(self.block), len(data))
            self.block += data[:take]
            data = data[take:]

    def output(self):
        block = bytes(self.block) + bytes(_BLOCK_LEN - len(self.block))
        return _Output(self.cv, _words(block), self.counter, len(self.block),
                       self.flags | self._start() | _CHUNK_END)


def _parent(left, right, key, flags):
    return _Output(list(key), tuple(left) + tuple(right), 0, _BLOCK_LEN, _PARENT | flags)


class Blake3:
    """The unkeyed BLAKE3 hasher: update() any number of times, then digest()."""

    name = "blake3"
    digest_size = 32

    def __init__(self, data: bytes = b""):
        self._key = _IV
        self._flags = 0
        self._chunk = _Chunk(_IV, 0, 0)
        self._stack = []
        if data:
            self.update(data)

    def _push(self, cv, total):
        while total & 1 == 0:
            cv = _parent(self._stack.pop(), cv, self._key, self._flags).chaining_value()
            total >>= 1
        self._stack.append(cv)

    def update(self, data) -> None:
        view = memoryview(bytes(data))
        while view:
            if self._chunk.length() == _CHUNK_LEN:
                cv = self._chunk.output().chaining_value()
                total = self._chunk.counter + 1
                self._push(cv, total)
                self._chunk = _Chunk(self._key, total, self._flags)
            take = min(_CHUNK_LEN - self._chunk.length(), len(view))
            self._chunk.update(view[:take])
            view = view[take:]

    def digest(self, length: int = 32) -> bytes:
        out = self._chunk.output()
        for cv in reversed(self._stack):
            out = _parent(cv, out.chaining_value(), self._key, self._flags)
        return out.root_bytes(length)

    def hexdigest(self) -> str:
        return self.digest().hex()


def pure_blake3(data: bytes = b"") -> Blake3:
    return Blake3(data)


def blake3(data: bytes = b""):
    """A BLAKE3 hasher: the `blake3` package's when installed, else this module's."""
    try:
        from blake3 import blake3 as fast  # type: ignore[import-not-found]
    except ImportError:
        return Blake3(data)
    return fast(data)
