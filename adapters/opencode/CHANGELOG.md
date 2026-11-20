# Changelog

All notable changes to `opencode-recollect` are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v0.1.0] - 2026-11-21

### Added

- OpenCode plugin that starts the Recollect engine, exposes the `memory`
  tool, registers the `/recollect-*` commands, and captures session events.
- Engine binary detection from `binaryPath`, `RECOLLECT_BINARY`, `PATH`, or
  the default install directory.
- Options for `userDir`, `generationPolicy`, and `generationTimeoutMs`.

[v0.1.0]: https://github.com/dkmnx/recollect/releases/tag/v0.1.0
