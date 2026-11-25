# Changelog

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
