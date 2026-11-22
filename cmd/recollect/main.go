// Command recollect serves the Recollect engine over stdio and exposes
// small setup commands for the OpenCode adapter.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/dkmnx/recollect/internal/config"
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
		return runDoctor(args[1:], stdout, stderr)
	case "resolve":
		return runResolve(args[1:], stdout, stderr)
	case "mcp":
		return runMCP(args[1:], stdout, stderr)
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
	deps := rpc.Deps{Open: func(b rpc.Bindings) (rpc.Deps, error) {
		cfg, err := config.LoadUserDir(b.UserDir)
		if err != nil {
			fmt.Fprintln(stderr, "recollect:", err)
			return rpc.Deps{}, err
		}
		return openDeps(b, cfg, stderr)
	}}
	if err := rpc.Serve(os.Stdin, os.Stdout, deps); err != nil {
		fmt.Fprintln(stderr, "recollect:", err)
		return 1
	}
	return 0
}

func openDeps(b rpc.Bindings, cfg config.Config, stderr io.Writer) (rpc.Deps, error) {
	searchBackend := cfg.Search.Backend
	if searchBackend == "lexical" {
		searchBackend = ""
	}
	var embedder memory.EmbeddingProvider
	if searchBackend == "hybrid" {
		embedder = memory.NewHTTPEmbedder(memory.HTTPEmbedConfig{
			BaseURL:    strings.TrimSpace(cfg.Embedding.BaseURL),
			Model:      strings.TrimSpace(cfg.Embedding.Model),
			APIKey:     os.Getenv(cfg.Embedding.APIKeyEnv),
			Dimensions: cfg.Embedding.Dimensions,
		})
	}
	generator, err := buildGenerator(cfg.Generation, stderr)
	if err != nil {
		return rpc.Deps{}, err
	}
	return rpc.OpenBindingsWithGenerator(b, searchBackend, embedder, generator)
}
