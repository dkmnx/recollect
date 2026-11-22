# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v0.2.0] - 2026-11-22

### Added

- `recollect mcp` serves the Model Context Protocol over stdio with memory
  search, read, save, correct, delete, and context tools, plus resources.
- `recollect resolve` prints the canonical project root, project directory, and
  user directory.
- A JSON config file at `<userDir>/config.json` sets the search backend, the
  embedding endpoint, and the generation mode. Environment variables override
  it.
- Command and HTTP generators let the engine capture memory when a harness
  cannot supply a model.
- `docs/protocol.md` documents the stdio protocol, with a golden transcript
  test.

### Changed

- Hybrid search now uses the configured HTTP embeddings endpoint. The derived
  vector index rebuilds when the model or vector width changes.
- `recollect doctor` prints the effective config, hides secret values, and
  accepts `--user-dir`.

## [v0.1.2] - 2026-11-22

### Changed

- macOS arm64 builds are now checked in CI.

### Fixed

- The HTTP embeddings client now has a 30s timeout and rejects responses larger
  than 16 MiB, so a stalled or oversized endpoint cannot hang or exhaust memory
  during hybrid search.

## [v0.1.1] - 2026-11-22

### Changed

- Installation examples now use `@latest` and a `vX.Y.Z` placeholder instead of
  a fixed release. The README badge tracks the latest GitHub release.

### Fixed

- Search matches now return the filtered fact body in `body` and the credited
  field label in a separate `field` property. Lexical matches populate `body`;
  semantic matches leave it empty. Read responses omit `field`.

## [v0.1.0] - 2026-11-21

### Added

- Markdown fact storage with format-v1 parsing, atomic writes, generated
  indexes, links, and archives.
- Project and user memory scopes with ranked lexical search and an optional
  private SQLite FTS5 index.
- Hybrid semantic recall through a host-injected embedding provider, with
  stale-vector checks and scene filters.
- Privacy filtering that removes private spans and secrets before search,
  prompt rendering, and durable capture.
- Session digests, revision chains, journals, and derived pages built from
  cited facts.
- Cross-process transaction engine with revision guards, request receipts,
  tombstones, session cursors, and crash recovery.
- Durable capture and maintenance job queue with leases, retries, conflict
  states, and an injected model generator.
- JSON-RPC stdio server for memory operations, context snapshots, event
  ingest, jobs, import, and status.
- Prompt section renderer that accepts injected enablement, store, and project
  root.
- Legacy store importer with dry-run reports, source hashes, a manifest, and
  byte-preserving source files.
- Public Go facades for memory, engine, jobs, render, and imports.
- OpenCode adapter that starts the engine, exposes the memory tool, registers
  commands, captures session events, and finds the binary from config, the
  environment, `PATH`, or the default install directory.
- `recollect` stdio binary with `version`, `doctor`, and `setup opencode`
  commands, plus the `cmd/demo` quickstart.
- Checksum-verified install scripts for POSIX and Windows.
- Tag-triggered release archives for Linux, macOS, and Windows with SHA-256
  checksums.
- Cross-platform atomic replacement and advisory locks for Linux and Windows,
  including a user-only Windows search cache.

[v0.1.0]: https://github.com/dkmnx/recollect/compare/9ddfd25cb1f64ff34c071785c27100c5caa5ae71...v0.1.0
[v0.1.1]: https://github.com/dkmnx/recollect/compare/v0.1.0...v0.1.1
[v0.1.2]: https://github.com/dkmnx/recollect/compare/v0.1.1...v0.1.2
[v0.2.0]: https://github.com/dkmnx/recollect/compare/v0.1.2...v0.2.0
