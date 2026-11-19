package e2e

// audit: behavior - the opt-in SQLite index mirrors filtered file search and stays derived from Markdown.
// audit: regression - an index tied to the store could override or lose durable facts on refresh or corruption.
// audit: coverage gap - existing search tests never enable the index or inspect its derived state.
// audit: seam - tests drive the built binary with JSON-RPC and inspect the private cache directory it creates.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func writeFactFile(t *testing.T, project, name, description, body string) {
	t.Helper()
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(project, name+".md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendNamedFact(t *testing.T, c *client, id, name, description, body string) {
	t.Helper()
	ok(t, c.call(t, "memory.execute", execParams{
		Target: "project", Operation: "append", RequestID: id,
		Name: name, Description: description, Body: body,
	}))
}

func searchNamesRPC(t *testing.T, c *client, query string) []string {
	t.Helper()
	raw := ok(t, c.call(t, "memory.execute", execParams{
		Target: "project", Operation: "search", Query: query, Limit: 20,
	}))
	var result struct {
		Matches []struct {
			Name string `json:"name"`
		} `json:"matches"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(result.Matches))
	for _, match := range result.Matches {
		names = append(names, match.Name)
	}
	return names
}

func TestSearchIndexParityAndDerivedState(t *testing.T) {
	t.Setenv("RECOLLECT_SEARCH_INDEX", "sqlite")
	d := tempDirs(t)
	c := start(t)
	defer c.cmd.Process.Kill()
	initialize(t, c, d, "index-harness", map[string]int{"major": 1})
	appendNamedFact(t, c, "idx-1", "idx-1", "alpha topic", "visible alpha body")
	appendNamedFact(t, c, "idx-2", "idx-2", "beta topic", "visible beta body")

	if names := searchNamesRPC(t, c, "alpha"); len(names) != 1 || names[0] != "idx-1" {
		t.Fatalf("indexed search = %v", names)
	}
	dbPath := filepath.Join(d.project, ".engine", "search", "index-v1.db")
	if info, err := os.Stat(dbPath); err != nil || info.Size() == 0 {
		t.Fatalf("index database missing: %v", err)
	}
	if info, err := os.Stat(filepath.Join(d.project, ".engine", "search")); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("search cache is not private: %v", info)
	}

	writeFactFile(t, d.project, "idx-1", "gamma topic", "visible gamma body")
	if names := searchNamesRPC(t, c, "gamma"); len(names) != 1 || names[0] != "idx-1" {
		t.Fatalf("out-of-band edit not reflected: %v", names)
	}
	if names := searchNamesRPC(t, c, "alpha"); len(names) != 0 {
		t.Fatalf("stale indexed terms survived: %v", names)
	}
	if err := os.Remove(filepath.Join(d.project, "idx-1.md")); err != nil {
		t.Fatal(err)
	}
	if names := searchNamesRPC(t, c, "gamma"); len(names) != 0 {
		t.Fatalf("deleted fact still searchable: %v", names)
	}
}

func TestSearchIndexFallsBackOnCorruptDatabase(t *testing.T) {
	t.Setenv("RECOLLECT_SEARCH_INDEX", "sqlite")
	d := tempDirs(t)
	c := start(t)
	defer c.cmd.Process.Kill()
	initialize(t, c, d, "index-harness", map[string]int{"major": 1})
	appendNamedFact(t, c, "fallback-1", "fallback-1", "alpha topic", "visible alpha body")
	if names := searchNamesRPC(t, c, "alpha"); len(names) != 1 {
		t.Fatalf("initial indexed search = %v", names)
	}
	dbPath := filepath.Join(d.project, ".engine", "search", "index-v1.db")
	if err := os.WriteFile(dbPath, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if names := searchNamesRPC(t, c, "alpha"); len(names) != 1 || names[0] != "fallback-1" {
		t.Fatalf("corrupt index lost search: %v", names)
	}
}

func TestSearchIndexCrossProcessVisibility(t *testing.T) {
	d := tempDirs(t)
	t.Setenv("RECOLLECT_SEARCH_INDEX", "sqlite")
	indexed := start(t)
	defer indexed.cmd.Process.Kill()
	initialize(t, indexed, d, "index-harness", map[string]int{"major": 1})

	t.Setenv("RECOLLECT_SEARCH_INDEX", "")
	fileClient := start(t)
	defer fileClient.cmd.Process.Kill()
	initialize(t, fileClient, d, "file-harness", map[string]int{"major": 1})

	appendNamedFact(t, fileClient, "cross-1", "cross-1", "delta topic", "visible delta body")
	if names := searchNamesRPC(t, indexed, "delta"); len(names) != 1 || names[0] != "cross-1" {
		t.Fatalf("indexed process did not see the other process write: %v", names)
	}
}

func TestSearchIndexConcurrentRefreshAndMutation(t *testing.T) {
	t.Setenv("RECOLLECT_SEARCH_INDEX", "sqlite")
	d := tempDirs(t)
	indexed := start(t)
	defer indexed.cmd.Process.Kill()
	initialize(t, indexed, d, "index-harness", map[string]int{"major": 1})
	for i := 0; i < 20; i++ {
		appendNamedFact(t, indexed, fmt.Sprintf("conc-%d", i), fmt.Sprintf("conc-%d", i),
			"topic", "visible body")
	}
	if names := searchNamesRPC(t, indexed, "conc"); len(names) == 0 {
		t.Fatal("indexed corpus empty before concurrency")
	}

	t.Setenv("RECOLLECT_SEARCH_INDEX", "")
	fileClient := start(t)
	defer fileClient.cmd.Process.Kill()
	initialize(t, fileClient, d, "file-harness", map[string]int{"major": 1})

	var wg sync.WaitGroup
	errs := make(chan string, 40)
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			if _, err := fileClient.callRaw("memory.execute", execParams{
				Target: "project", Operation: "append", RequestID: fmt.Sprintf("race-%d", n),
				Description: "raced topic", Body: "visible raced body",
			}); err != nil {
				errs <- err.Error()
			}
		}(i)
		go func() {
			defer wg.Done()
			if _, err := indexed.callRaw("memory.execute", execParams{
				Target: "project", Operation: "search", Query: "conc", Limit: 20,
			}); err != nil {
				errs <- err.Error()
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent operation failed: %s", err)
	}
	if names := searchNamesRPC(t, indexed, "raced"); len(names) == 0 {
		t.Fatal("indexed process did not converge on concurrent writes")
	}
}

func TestUnsetBackendWritesNoIndex(t *testing.T) {
	d := tempDirs(t)
	c := start(t)
	defer c.cmd.Process.Kill()
	initialize(t, c, d, "file-harness", map[string]int{"major": 1})
	appendFact(t, c, "file-1", "alpha topic", "visible alpha body")
	if names := searchNamesRPC(t, c, "alpha"); len(names) != 1 {
		t.Fatalf("file search = %v", names)
	}
	if _, err := os.Stat(filepath.Join(d.project, ".engine", "search")); !os.IsNotExist(err) {
		t.Fatalf("unset backend created an index: %v", err)
	}
}
