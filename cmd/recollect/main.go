// Command recollect serves the Recollect engine over stdio and exposes
// small setup commands for the OpenCode adapter.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/dkmnx/recollect/internal/memory"
	"github.com/dkmnx/recollect/internal/rpc"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return serve(stderr)
	}
	switch args[0] {
	case "version", "--version", "-version":
		fmt.Fprintln(stdout, versionText(buildVersion(), commit, date))
		return 0
	case "doctor":
		if err := runDoctor(stdout); err != nil {
			fmt.Fprintln(stderr, "recollect:", err)
			return 1
		}
		return 0
	case "resolve":
		return runResolve(args[1:], stdout, stderr)
	case "setup":
		return runSetup(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "recollect: unknown command %q\n\n", args[0])
		usage(stderr)
		return 2
	}
}

func serve(stderr io.Writer) int {
	searchBackend := os.Getenv("RECOLLECT_SEARCH_INDEX")
	if searchBackend != "sqlite" && searchBackend != "hybrid" {
		searchBackend = ""
	}
	var embedder memory.EmbeddingProvider
	if searchBackend == "hybrid" {
		embedder = memory.NewDeterministicEmbedder(256)
	}
	deps := rpc.Deps{Open: func(b rpc.Bindings) (rpc.Deps, error) {
		return rpc.OpenBindingsWithSearch(b, searchBackend, embedder)
	}}
	if err := rpc.Serve(os.Stdin, os.Stdout, deps); err != nil {
		fmt.Fprintln(stderr, "recollect:", err)
		return 1
	}
	return 0
}
