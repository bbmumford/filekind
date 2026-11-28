# Changelog

## [0.2.0](https://github.com/bbmumford/filekind/compare/v0.1.2...v0.2.0) (2026-11-28)


### ⚠ BREAKING CHANGES

* Verify (Go) and verify (Python) answer ErrUnsafeOpen (UnsafeOpen) for a connection whose engine does not report query_only or trusted_schema; standard SQLite reports both, and the Turso engine answers trusted_schema with no row.
* Verify and the Python verify answer ErrUnsafeOpen (UnsafeOpen) for a connection without both settings where its engine reports them; the Turso engine reports query_only off and cannot set it, so Verify refuses a file opened through it.
* DigestRows and EncodeRows refuse a Key column outside Columns with ErrKeyNotCovered unless RowSet.UnsignedKey is set; the Python helper's RowSet takes unsigned_key and the vectors carry unsignedKey.

### Features

* a row set whose key is outside its columns is refused unless it says its key is unsigned, so a signed digest binds every row's key ([1042805](https://github.com/bbmumford/filekind/commit/1042805b21eeb4d422884fa58f12934e907645b7))
* package sqliteverify opens a file for Verify read-only through modernc SQLite, with query_only on and trusted_schema off, after checking it is a regular file within a size bound ([02979a3](https://github.com/bbmumford/filekind/commit/02979a3535b843ce6138891ea94013b8bb4037e4))


### Bug Fixes

* a row set naming no schema reads the main database's table, never a TEMP table of the same name ([4e1a63c](https://github.com/bbmumford/filekind/commit/4e1a63c0d6a32c1beebff79d957fa2a11baf9fdb))
* Verify refuses a connection that answers query_only or trusted_schema with no row, so only an engine that reports both settings safe passes ([ff178f9](https://github.com/bbmumford/filekind/commit/ff178f91b9baa90ea687c69913c5caf6523a6ede))
* Verify refuses a connection that reports query_only off or trusted_schema on, before it reads the file ([305d3e6](https://github.com/bbmumford/filekind/commit/305d3e6128a1d3e31cef0d527e0edad1e84e9804))

## [0.1.2](https://github.com/bbmumford/filekind/compare/v0.1.1...v0.1.2) (2026-11-25)


### Bug Fixes

* Read takes application_id and user_version from one statement, so a reader racing a stamp never sees one without the other ([0147c47](https://github.com/bbmumford/filekind/commit/0147c479d33aded60d929141894306929b691c43))

## [0.1.1](https://github.com/bbmumford/filekind/compare/v0.1.0...v0.1.1) (2026-11-24)


### Bug Fixes

* every Close, Flush and buffered write result is checked, or dropped where the code says why ([2f4e64a](https://github.com/bbmumford/filekind/commit/2f4e64a750f6a56c8f3b7bbb1d561902c9096050))

## 0.1.0 (2026-11-24)


### ⚠ BREAKING CHANGES

* the registry's header reads 'system' where it read 'lane', and Entry.Lane is Entry.System in Go (entry.system in Python).

### Features

* a file-kind registry, identity header check and stamp, and canonical row digest for single-file SQLite databases, in Go and Python ([4361a66](https://github.com/bbmumford/filekind/commit/4361a66df72d861ea2048473c992b0e4a34cd0e1))
* Verify refuses a damaged file before its header is trusted, and the registry publishes runLedger at generation 4 and decisionIndex at generation 3 ([06e2e44](https://github.com/bbmumford/filekind/commit/06e2e447f13336ba781bad09b4b0b9ab1a31a48a))


### Code Refactoring

* the registry names each kind's consuming system and says in one plain phrase what its files hold ([fced9de](https://github.com/bbmumford/filekind/commit/fced9de69e2718b220c871b8165fa127a3cfd60b))
