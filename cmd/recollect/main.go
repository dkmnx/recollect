// Command recollect serves the Recollect engine over stdio.
package main

import (
	"fmt"
	"os"

	"github.com/dkmnx/recollect/internal/memory"
	"github.com/dkmnx/recollect/internal/rpc"
)

func main() {
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
		fmt.Fprintln(os.Stderr, "recollect:", err)
		os.Exit(1)
	}
}
