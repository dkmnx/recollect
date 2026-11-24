# Changelog

All notable changes to `opencode-recollect` are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- Run `/recollect-status` and `/recollect-refresh` through host Effects and put
  the result in the visible notice description, so OpenCode shows the health
  report and the refresh outcome in the TUI. Keep the model-facing notice text
  empty, skip work without a session ID, and report engine RPC failures without
  failing the command.

## [v0.1.1] - 2026-11-22

### Fixed

- Restore npm publication after the retracted `0.1.0` release.

## [v0.1.0] - 2026-11-21

### Added

- OpenCode plugin that starts the Recollect engine, exposes the `memory`
  tool, registers the `/recollect-*` commands, and captures session events.
- Engine binary detection from `binaryPath`, `RECOLLECT_BINARY`, `PATH`, or
  the default install directory.
- Options for `userDir`, `generationPolicy`, and `generationTimeoutMs`.

[v0.1.0]: https://github.com/dkmnx/recollect/releases/tag/v0.1.0
[v0.1.1]: https://github.com/dkmnx/recollect/releases/tag/opencode-recollect-v0.1.1
