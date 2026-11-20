# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
