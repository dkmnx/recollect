# Contributing to Recollect

Recollect is a Go memory engine. This guide covers the checks a change must
pass and the conventions the codebase follows.

## Requirements

- Go 1.27.4, matching `go.mod`.
- A 64-bit C compiler for `go test -race`.
- `staticcheck` and `govulncheck` for the analysis gate.
- Bun 1.x only when you change the OpenCode adapter.

Install the analysis tools:

```sh
go install honnef.co/go/tools/cmd/staticcheck@v0.8.2
go install golang.org/x/vuln/cmd/govulncheck@v1.10.0
```

## Setup

Clone the repository and run the demo. The demo opens a temporary store,
writes one fact, and prints the search hit.

```sh
git clone https://github.com/dkmnx/recollect
cd recollect
go run ./cmd/demo
```

## Checks before a pull request

Run every Go check from the repository root. CI runs the same set on Linux
and Windows.

```sh
gofmt -l .                  # must print nothing
go vet ./...
go test ./... -count=1
go test -race ./... -count=1
go run ./tools/checktestaudit
staticcheck ./...
govulncheck ./...
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...
```

Race tests need `CGO_ENABLED=1` and a C compiler on `PATH`. On Windows, CI
uses the MSYS2 MINGW64 toolchain.

For adapter changes:

```sh
cd adapters/opencode
bun install --frozen-lockfile
bun run typecheck
bun test
```

## Tests

Every test file starts with an audit record. The check tool requires the
behavior and coverage lines. Add the regression and the seam too:

```go
// audit: behavior - what observable behavior the test proves.
// audit: regression - the failure this test prevents.
// audit: coverage gap - what the existing suite did not cover.
// audit: seam - the real entry point and fixture the test drives.
```

Write the test before the fix. Confirm it fails on the old code, then confirm
it passes. Acceptance tests use real boundaries: temporary directories,
subprocesses, JSON-RPC frames, and on-disk state. Do not add test-only hooks
to production code. The compatibility helpers in `internal/memory/export.go`
are the existing host-adapter exception.

## Code style

- Keep packages under `internal/`. Add a top-level package only for a public
  facade.
- Do not add a dependency for a few lines. The declared dependencies are
  `golang.org/x/sys`, `ncruces/go-sqlite3`, and `luist18/sqlite-vec-go`.
- Inject host inputs. `internal/render` and `internal/engine` do not import
  host or adapter packages.
- Put platform-specific locking in build-tagged files under `internal/oslock`.
- Hash request IDs before using them as state file names.
- Keep README user-facing. Put architecture notes in `AGENTS.md`.

## Privacy

Facts and user memory can hold private text. Filter complete inputs before
slicing, redacting, or persisting them. Never log fact text, queries, or
vectors. Never commit credentials, tokens, or environment values.

## Commits

Use Conventional Commits. Keep one logical change per commit.

```text
feat(memory): add a read-only store constructor
fix(jobs): keep stable failure codes
docs: add the runnable quickstart demo
```

Write the subject in the imperative mood. Explain why in the body when the
diff does not make it obvious.

## Branches and pull requests

Name branches `<type>/<description>`, for example
`fix/windows-search-dacl`.

A pull request should:

- Explain the problem and the outcome.
- List the checks you ran.
- Include a regression test that fails without the fix.
- Stay focused. Split unrelated changes.

CI must be green before merge.

## Reporting security issues

Do not open a public issue for a vulnerability. Use a private security
advisory on the GitHub repository.

## License

Recollect is MIT licensed. By contributing, you agree that your work is
released under the same license.
