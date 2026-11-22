# filekind (Python)

The Python helper for the single-file SQLite format: the identity header check
and stamp, the kinds registry, the canonical row digest (SHA-256 and BLAKE3)
and the canonical JSON form, over the standard library's `sqlite3`. The format
is specified in the repository's SPEC.md, and this package passes the same
golden vectors as the Go helper.

Extras: `fast` uses the `blake3` package's hasher; `base3` names the ternary
codec package `orbitra`, which `filekind.base3` calls and which is installed
from its own checkout.
