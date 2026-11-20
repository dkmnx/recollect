# Recollect context

Recollect is a standalone Go memory engine. The canonical module path is
`github.com/dkmnx/recollect`, not published yet. Go 1.27.4,
`golang.org/x/sys` v0.49.0, `ncruces/go-sqlite3`, and
`luist18/sqlite-vec-go` are the declared dependencies.

## Architecture

- `internal/memory` owns facts, storage, search, ranking, privacy, digests,
  pages, and format-v1 parsing.
- `internal/render` builds the memory prompt section from injected enablement,
  store, and root.
- `internal/oslock` holds OS advisory locks. Windows uses `LockFileEx`; other
  systems use `flock`.
- `internal/engine` owns transactions, revision guards, request receipts,
  session cursors, and scope lock ordering. It wraps `memory.Store`.
- The top-level `memory`, `engine`, `jobs`, `render`, and `imports` packages
  are the public facades. `cmd/recollect` serves the stdio protocol and setup
  commands. `adapters/opencode` is the OpenCode plugin.
- `tools/checktestaudit` rejects test files without an audit record.

## Commands

- Run `go test ./...` and `go vet ./...` from the repository root.
- Race checks need a 64-bit C compiler. On this host set `PATH` to include
  `C:\msys64\clang64\bin` and `CGO_ENABLED=1` before `go test -race ./...`.
- Run `go run ./tools/checktestaudit` before any change with tests.
- Cross-builds use `GOOS=linux GOARCH=amd64` and `GOOS=windows GOARCH=amd64`
  with `CGO_ENABLED=0`.
- Native Linux verification uses Fedora WSL2 with Go at
  `/home/swengr/go-install/go/bin/go`. Run both `go test ./... -count=1` and
  `go test -race ./... -count=1` there before claiming Linux behavior.

## Code style

- Keep packages under `internal/`. Public facades live at the top level.
  Avoid new dependencies.
- Put platform-specific locking in build-tagged files under `internal/oslock`.
- Keep host inputs injected. The render and engine packages do not import
  host or adapter packages.
- Hash request IDs before using them as state file names.

## Testing and state

- Every test file needs an adjacent audit record naming behavior, regression,
  coverage gap, and seam. The check tool enforces this.
- Acceptance tests use real boundaries. Four-process subprocess tests cover
  project and user appends. Recovery tests construct durable states on disk.
- Private state lives in `.engine/`. It is not part of the fact format.
  Receipts replay for 30 days and tombstones block stale retries for 30 more.
- Pages record source hashes. Do not rely on file mtimes alone for staleness.

## README boundary

- Keep README user-facing. Do not put plan status, migration evidence,
  fixture comparisons, or machine-specific paths in it.
