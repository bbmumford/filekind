# Changelog

## 0.1.0 (2026-11-24)


### ⚠ BREAKING CHANGES

* the registry's header reads 'system' where it read 'lane', and Entry.Lane is Entry.System in Go (entry.system in Python).

### Features

* a file-kind registry, identity header check and stamp, and canonical row digest for single-file SQLite databases, in Go and Python ([4361a66](https://github.com/bbmumford/filekind/commit/4361a66df72d861ea2048473c992b0e4a34cd0e1))
* Verify refuses a damaged file before its header is trusted, and the registry publishes runLedger at generation 4 and decisionIndex at generation 3 ([06e2e44](https://github.com/bbmumford/filekind/commit/06e2e447f13336ba781bad09b4b0b9ab1a31a48a))


### Code Refactoring

* the registry names each kind's consuming system and says in one plain phrase what its files hold ([fced9de](https://github.com/bbmumford/filekind/commit/fced9de69e2718b220c871b8165fa127a3cfd60b))
