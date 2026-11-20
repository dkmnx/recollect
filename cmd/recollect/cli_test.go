package main

// audit: behavior - version, OpenCode setup, and doctor produce a usable installer surface.
// audit: regression - the binary served stdio only, so a user had no way to discover or configure it.
// audit: coverage gap - no test drove the CLI subcommands.
// audit: seam - run with injected directories and an in-process engine probe.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionText(t *testing.T) {
	got := versionText("v0.1.0", "abc123", "2026-11-20")
	if !strings.Contains(got, "recollect v0.1.0") || !strings.Contains(got, "abc123") {
		t.Fatalf("version = %q", got)
	}
	if fallback := versionText("", "", ""); !strings.Contains(fallback, "devel") {
		t.Fatalf("fallback = %q", fallback)
	}
}

func TestOpenCodeConfig(t *testing.T) {
	raw, err := openCodeConfig("/repo/recollect", "/repo/adapters/opencode", "/home/u/.recollect")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Plugins []struct {
			Package string `json:"package"`
			Options struct {
				BinaryPath string `json:"binaryPath"`
				UserDir    string `json:"userDir"`
			} `json:"options"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Plugins) != 1 {
		t.Fatalf("plugins = %+v", cfg.Plugins)
	}
	entry := cfg.Plugins[0]
	if entry.Package != "file:///repo/adapters/opencode" {
		t.Fatalf("package = %q", entry.Package)
	}
	if entry.Options.BinaryPath != "/repo/recollect" || entry.Options.UserDir != "/home/u/.recollect" {
		t.Fatalf("options = %+v", entry.Options)
	}
}

func TestResolveAdapterDir(t *testing.T) {
	repo := t.TempDir()
	adapter := filepath.Join(repo, "adapters", "opencode")
	if err := os.MkdirAll(adapter, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(repo, "recollect")
	if got := resolveAdapterDir(exe, t.TempDir(), nil); got != adapter {
		t.Fatalf("adapter = %q, want %q", got, adapter)
	}
	if got := resolveAdapterDir(exe, t.TempDir(), map[string]string{"RECOLLECT_ADAPTER_DIR": adapter}); got != adapter {
		t.Fatalf("env adapter = %q", got)
	}
}

func TestWriteOpenCodeConfigCreatesJSON(t *testing.T) {
	dir := t.TempDir()
	path, err := writeOpenCodeConfig(dir, "/repo/recollect", "/repo/adapters/opencode", "/home/u/.recollect")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "adapters/opencode") {
		t.Fatalf("config = %s", raw)
	}
}

func TestWriteOpenCodeConfigRefusesJSONC(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "opencode.jsonc"), []byte("{ // comment\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := writeOpenCodeConfig(dir, "/repo/recollect", "/repo/adapters/opencode", "/home/u/.recollect"); err == nil {
		t.Fatal("jsonc config was overwritten")
	}
}

func TestRunDoctor(t *testing.T) {
	var out strings.Builder
	if err := runDoctor(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "engine ok") {
		t.Fatalf("doctor = %q", out.String())
	}
}
