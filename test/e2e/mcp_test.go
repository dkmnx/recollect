package e2e

// audit: behavior - the built binary serves MCP, saves facts, and searches them across two processes.
// audit: regression - CLI wiring or concurrency could lose a fact or return no tool result.
// audit: coverage gap - no subprocess test exercised recollect mcp.
// audit: seam - the test spawns the built binary with isolated HOME and project dirs and crosses stdio.

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func startMCP(t *testing.T, project, home string) *client {
	t.Helper()
	cmd := exec.Command(binaryPath, "mcp", "--project", project)
	cmd.Env = append(os.Environ(), "HOME="+home, "LOCALAPPDATA="+home)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	c := &client{cmd: cmd, in: in, out: bufio.NewReader(out)}
	cmd.Stderr = &c.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	return c
}

func mcpDirs(t *testing.T) (project, home string) {
	t.Helper()
	base := t.TempDir()
	project = filepath.Join(base, "project")
	home = filepath.Join(base, "home")
	for _, dir := range []string{project, home} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// Stop project resolution from walking into an unrelated ancestor repo.
	if err := os.MkdirAll(filepath.Join(project, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	return project, home
}

func saveMCPRaw(c *client, description, body string) (response, error) {
	return c.callRaw("tools/call", map[string]any{
		"name": "memory_save",
		"arguments": map[string]any{
			"description": description,
			"body":        body,
		},
	})
}

func initializeMCP(t *testing.T, c *client) {
	t.Helper()
	ok(t, c.call(t, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"clientInfo":      map[string]any{"name": "e2e"},
	}))
}

func saveMCP(t *testing.T, c *client, description, body string) {
	t.Helper()
	ok(t, c.call(t, "tools/call", map[string]any{
		"name": "memory_save",
		"arguments": map[string]any{
			"description": description,
			"body":        body,
		},
	}))
}

func searchMCP(t *testing.T, c *client, query string) string {
	t.Helper()
	return string(ok(t, c.call(t, "tools/call", map[string]any{
		"name":      "memory_search",
		"arguments": map[string]any{"query": query},
	})))
}

func TestMCPServerSavesAndSearches(t *testing.T) {
	project, home := mcpDirs(t)
	c := startMCP(t, project, home)
	initializeMCP(t, c)
	saveMCP(t, c, "MCP fact", "saved over the MCP transport")
	found := searchMCP(t, c, "mcp")
	if !strings.Contains(found, "MCP fact") {
		t.Fatalf("search = %s", found)
	}
}

func TestMCPTwoProcessesAppend(t *testing.T) {
	project, home := mcpDirs(t)
	first := startMCP(t, project, home)
	second := startMCP(t, project, home)
	initializeMCP(t, first)
	initializeMCP(t, second)

	type outcome struct {
		resp response
		err  error
	}
	outcomes := make(chan outcome, 2)
	go func() {
		resp, err := saveMCPRaw(first, "First fact", "written by the first process")
		outcomes <- outcome{resp, err}
	}()
	go func() {
		resp, err := saveMCPRaw(second, "Second fact", "written by the second process")
		outcomes <- outcome{resp, err}
	}()
	for i := 0; i < 2; i++ {
		got := <-outcomes
		if got.err != nil {
			t.Fatalf("concurrent save: %v", got.err)
		}
		if got.resp.Error != nil {
			t.Fatalf("concurrent save error: %+v", got.resp.Error)
		}
	}

	found := searchMCP(t, first, "fact")
	if !strings.Contains(found, "First fact") || !strings.Contains(found, "Second fact") {
		t.Fatalf("search after concurrent appends = %s", found)
	}
}
