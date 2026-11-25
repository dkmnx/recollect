# Changelog

All notable changes to `dsh-recollect` are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v0.1.0] - 2026-11-25

### Added

- First release of the DeepSeek Harness adapter.
- A scoped `recollect_memory` tool for each agent, bound to that agent's
  working directory.
- A read-only memory snapshot injected into the agent's system prompt at agent
  creation.
- Project memory isolation by a hash of the canonical workspace root returned by `recollect resolve`.
- Binary resolution from config, `RECOLLECT_BINARY`, or the default install
  directory, with PATH as a fallback.
- One engine process per agent, sharing a store through the engine's locks and
  receipts.
- Early validation of tool arguments with actionable error messages.
- Read results include the fact revision, so `correct` and `delete` can be
  called with the revision just read.

[v0.1.0]: https://github.com/dkmnx/recollect/compare/9ddfd25cb1f64ff34c071785c27100c5caa5ae71...dsh-recollect-v0.1.0
