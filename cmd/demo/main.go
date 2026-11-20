// Command demo is the quickstart from the README. It opens a temporary
// store, writes one fact, and prints the search hit.
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
	defer os.RemoveAll(root)
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

	if _, err := store.AppendFact(memory.Fact{
		Name:        "build-command",
		Description: "How to build the project",
		Type:        memory.FactTypeProject,
		Body:        "Run go test ./... before a change.",
	}); err != nil {
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
