# Recollect

Recollect is a Go memory engine for coding agents. It stores facts as Markdown
files and ranks them for recall.

## Description

An agent needs memory that survives a session and stays easy to inspect.
Recollect stores each fact in its own Markdown file. A generated index lists
the facts. The engine searches the corpus, follows links between facts, and
builds derived pages from cited sources.

The packages are written in Go and depend only on the standard library and
`golang.org/x/sys`. Platform-specific code is limited to atomic file
replacement. The engine is designed to be cross platform. Linux and Windows
builds are checked. macOS is not verified.

Markdown stays the authoritative store, so a person can read and edit the
files without the engine.

Features:

- Fact storage with format-v1 parsing and atomic writes
- Ranked project and user search
- Privacy filtering and secret detection
- Digests, revision chains, journals, and derived pages
- Prompt section rendering from an injected store and enablement flag
- Fixtures and a ranking corpus that exercise the real store

## Requirements

Go 1.27 or later.

## Installation

1. Clone the repository.
2. Open the repository directory.
3. Download the module dependencies.

```sh
git clone <repository-url>
cd recollect
go mod download
```

The module is local. It has not been published.

## Usage

Import the public facade `github.com/dkmnx/recollect/memory` in your Go
program. Open a store over two directories.

```go
package main

import (
    "fmt"
    "os"
    "path/filepath"

    "github.com/dkmnx/recollect/memory"
)

func main() {
    root, err := os.MkdirTemp("", "recollect-demo-")
    if err != nil {
        panic(err)
    }
    userDir := filepath.Join(root, "user")
    projectDir := filepath.Join(root, "project")
    if err := os.MkdirAll(userDir, 0o700); err != nil {
        panic(err)
    }
    if err := os.MkdirAll(projectDir, 0o700); err != nil {
        panic(err)
    }

    store := memory.NewStore(memory.Options{
        UserDir:    userDir,
        ProjectDir: projectDir,
    })
    if _, err := store.EnsureProject(memory.ProjectMeta{
        Root: projectDir,
        Name: "demo",
        Slug: "demo",
    }); err != nil {
        panic(err)
    }

    _, err = store.AppendFact(memory.Fact{
        Name:        "build-command",
        Description: "How to build the project",
        Type:        memory.FactTypeProject,
        Body:        "Run go test ./... before a change.",
    })
    if err != nil {
        panic(err)
    }

    matches, err := store.SearchFacts("build", "", 10)
    if err != nil {
        panic(err)
    }
    for _, match := range matches {
        fmt.Println(match.Name, match.Description)
    }
}
```

Run it from the repository root with `go run ./cmd/demo`.
No credentials are needed.

## Tests

Run these checks from the repository root.

```sh
go test ./...
go vet ./...
go run ./tools/checktestaudit
```

These commands use the Go toolchain and run on any supported platform. The
audit check requires an audit record in every test file. The test suite
includes the format fixtures and the ranking corpus. The Windows atomic
replacement tests run only on Windows.

## Contributing

Run the checks before opening a change. Keep new behavior covered by tests at
the store or search boundary.

## License

Recollect uses the MIT License. See [LICENSE](LICENSE).

You may use, copy, modify, merge, publish, distribute, sublicense, and sell
copies of the software. Keep the copyright and permission notice in all copies
or substantial portions. The software comes without warranty.
