package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/dkmnx/recollect/internal/bindings"
	"github.com/dkmnx/recollect/internal/config"
	"github.com/dkmnx/recollect/internal/jobs"
	"github.com/dkmnx/recollect/internal/mcp"
	"github.com/dkmnx/recollect/internal/rpc"
	"github.com/dkmnx/recollect/memory"
)

// injected by GoReleaser.
var (
	version string
	commit  string
	date    string
)

func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "devel"
}

func versionText(v, c, d string) string {
	if v == "" {
		v = "devel"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "recollect %s", v)
	if c != "" {
		fmt.Fprintf(&b, "\ncommit: %s", c)
	}
	if d != "" {
		fmt.Fprintf(&b, "\nbuilt: %s", d)
	}
	return b.String()
}

func fileURL(path string) string {
	slashed := filepath.ToSlash(path)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	return "file://" + slashed
}

func openCodeConfig(exePath, adapterDir, userDir string) ([]byte, error) {
	config := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"plugins": []any{map[string]any{
			"package": fileURL(adapterDir),
			"options": map[string]any{
				"binaryPath":       exePath,
				"userDir":          userDir,
				"generationPolicy": "host",
			},
		}},
	}
	return json.MarshalIndent(config, "", "  ")
}

func resolveAdapterDir(exePath, cwd string, env map[string]string) string {
	if dir := strings.TrimSpace(env["RECOLLECT_ADAPTER_DIR"]); dir != "" && isDir(dir) {
		return dir
	}
	for _, candidate := range []string{
		filepath.Join(filepath.Dir(exePath), "adapters", "opencode"),
		filepath.Join(cwd, "adapters", "opencode"),
	} {
		if isDir(candidate) {
			return candidate
		}
	}
	return ""
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func defaultUserDir() string {
	return bindings.DefaultUserDir(runtime.GOOS, os.LookupEnv)
}

func openCodeConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "opencode"), nil
}

// writeOpenCodeConfig writes a plain JSON config. A jsonc file is never
// rewritten because a JSON encoder would drop its comments.
func writeOpenCodeConfig(dir, exePath, adapterDir, userDir string) (string, error) {
	jsonc := filepath.Join(dir, "opencode.jsonc")
	if _, err := os.Lstat(jsonc); err == nil {
		return "", fmt.Errorf("%s exists; add the printed block by hand", jsonc)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "opencode.json")
	config := map[string]any{"$schema": "https://opencode.ai/config.json"}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &config); err != nil {
			return "", fmt.Errorf("parse %s: %w", path, err)
		}
	}
	entry := map[string]any{
		"package": fileURL(adapterDir),
		"options": map[string]any{
			"binaryPath":       exePath,
			"userDir":          userDir,
			"generationPolicy": "host",
		},
	}
	var plugins []any
	if existing, ok := config["plugins"]; ok {
		list, isList := existing.([]any)
		if !isList {
			return "", errors.New("plugins in " + path + " is not an array")
		}
		plugins = list
	}
	replaced := false
	for i, item := range plugins {
		if existing, ok := item.(map[string]any); ok && existing["package"] == entry["package"] {
			plugins[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		plugins = append(plugins, entry)
	}
	config["plugins"] = plugins
	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, append(raw, '\n'), 0o600)
}

func runSetup(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "opencode" {
		fmt.Fprintln(stderr, "usage: recollect setup opencode [--write] [--adapter DIR]")
		return 2
	}
	fs := flag.NewFlagSet("setup opencode", flag.ContinueOnError)
	fs.SetOutput(stderr)
	write := fs.Bool("write", false, "merge the plugin entry into the global OpenCode config")
	adapter := fs.String("adapter", "", "path to adapters/opencode")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(stderr, "recollect:", err)
		return 1
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if abs, err := filepath.Abs(exe); err == nil {
		exe = abs
	}
	cwd, _ := os.Getwd()
	adapterDir := strings.TrimSpace(*adapter)
	if adapterDir == "" {
		env := map[string]string{"RECOLLECT_ADAPTER_DIR": os.Getenv("RECOLLECT_ADAPTER_DIR")}
		adapterDir = resolveAdapterDir(exe, cwd, env)
	}
	if adapterDir == "" {
		fmt.Fprintln(stderr, "recollect: cannot find adapters/opencode; pass --adapter DIR")
		return 1
	}
	userDir := defaultUserDir()
	block, err := openCodeConfig(exe, adapterDir, userDir)
	if err != nil {
		fmt.Fprintln(stderr, "recollect:", err)
		return 1
	}
	if *write {
		dir, err := openCodeConfigDir()
		if err != nil {
			fmt.Fprintln(stderr, "recollect:", err)
			return 1
		}
		path, err := writeOpenCodeConfig(dir, exe, adapterDir, userDir)
		if err != nil {
			fmt.Fprintln(stderr, "recollect:", err)
			fmt.Fprintf(stderr, "Add this block by hand:\n\n%s\n", block)
			return 1
		}
		fmt.Fprintf(stdout, "Wrote %s\n\n", path)
	} else {
		fmt.Fprintf(stdout, "Add this to your OpenCode config:\n\n%s\n\n", block)
	}
	fmt.Fprintln(stdout, "Restart OpenCode, then run /recollect-status.")
	return 0
}

func runResolve(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("resolve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", "", "project directory to resolve (default: current directory)")
	asJSON := fs.Bool("json", false, "print the result as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: recollect resolve [--root DIR] [--json]")
		return 2
	}
	resolved, err := bindings.Resolve(*root, "")
	if err != nil {
		fmt.Fprintln(stderr, "recollect:", err)
		return 1
	}
	if *asJSON {
		raw, err := json.MarshalIndent(resolved, "", "  ")
		if err != nil {
			fmt.Fprintln(stderr, "recollect:", err)
			return 1
		}
		fmt.Fprintln(stdout, string(raw))
		return 0
	}
	fmt.Fprintf(stdout, "projectRoot: %s\nprojectDir: %s\nuserDir: %s\n",
		resolved.ProjectRoot, resolved.ProjectDir, resolved.UserDir)
	return 0
}

func runMCP(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	project := fs.String("project", "", "project directory (default: current directory)")
	harness := fs.String("harness", "mcp", "harness identifier")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: recollect mcp [--project DIR] [--harness NAME]")
		return 2
	}
	opts := mcp.Options{
		ProjectHint:  *project,
		ProjectFixed: strings.TrimSpace(*project) != "",
		Harness:      *harness,
		Version:      buildVersion(),
		Open: func(dir string) (mcp.Opened, error) {
			resolved, err := bindings.Resolve(dir, "")
			if err != nil {
				return mcp.Opened{}, err
			}
			cfg, err := config.LoadUserDir(resolved.UserDir)
			if err != nil {
				return mcp.Opened{}, err
			}
			deps, err := openDeps(rpc.Bindings{
				ProjectDir:   resolved.ProjectDir,
				ProjectRoot:  resolved.ProjectRoot,
				UserDir:      resolved.UserDir,
				HarnessID:    *harness,
				Capabilities: []string{"generation", "events", "context"},
				Protocol:     rpc.Protocol{Major: 1},
			}, cfg, stderr)
			if err != nil {
				return mcp.Opened{}, err
			}
			generator, err := buildGenerator(cfg.Generation, stderr)
			if err != nil {
				deps.Store.Close()
				return mcp.Opened{}, err
			}
			if configured := time.Duration(cfg.Generation.TimeoutSeconds) * time.Second; configured > jobs.MaxGenerationTimeout {
				fmt.Fprintf(stderr, "recollect: generation timeout capped at %s to stay within the job lease\n", jobs.MaxGenerationTimeout)
			}
			return mcp.Opened{
				Deps: deps, Root: resolved.ProjectRoot, Generate: generator,
				Timeout: time.Duration(cfg.Generation.TimeoutSeconds) * time.Second,
			}, nil
		},
		Start: func(opened mcp.Opened) func() {
			ctx, cancel := context.WithCancel(context.Background())
			runner := &jobs.Runner{
				Queue: opened.Deps.Jobs, Harness: *harness, Generation: opened.Generate != nil,
				Timeout: opened.Timeout, Generate: opened.Generate,
				Log: func(err error) { fmt.Fprintln(stderr, "recollect:", err) },
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				runner.Run(ctx)
			}()
			return func() {
				cancel()
				<-done
			}
		},
	}
	if err := mcp.Serve(os.Stdin, stdout, opts); err != nil {
		fmt.Fprintln(stderr, "recollect:", err)
		return 1
	}
	return 0
}

func runDoctor(args []string, out, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	userDir := fs.String("user-dir", "", "user dir holding config.json (default: host default)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: recollect doctor [--user-dir DIR]")
		return 2
	}
	dir := strings.TrimSpace(*userDir)
	if dir == "" {
		dir = defaultUserDir()
	}
	if err := doctor(dir, out); err != nil {
		fmt.Fprintln(stderr, "recollect:", err)
		return 1
	}
	return 0
}

func doctor(userDir string, out io.Writer) error {
	cfg, err := config.LoadUserDir(userDir)
	if err != nil {
		return err
	}
	root, err := os.MkdirTemp("", "recollect-doctor-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	projectDir := filepath.Join(root, "project")
	probeUser := filepath.Join(root, "user")
	for _, dir := range []string{projectDir, probeUser} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	meta := memory.ProjectMetaFor(projectDir)
	store := memory.NewStore(memory.Options{ProjectDir: projectDir, UserDir: probeUser, Meta: meta})
	defer store.Close()
	if _, err := store.EnsureProject(meta); err != nil {
		return err
	}
	if _, err := store.AppendFact(memory.Fact{
		Name: "doctor-probe", Description: "doctor probe", Type: memory.FactTypeProject,
		Body: "engine probe",
	}); err != nil {
		return err
	}
	matches, err := store.SearchFacts("doctor", "", 5)
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return errors.New("search returned no probe fact")
	}
	fmt.Fprintf(out, "engine ok\nversion: %s\n", buildVersion())
	fmt.Fprintf(out, "config: %s\n", filepath.Join(userDir, "config.json"))
	for _, line := range cfg.Summary(os.Getenv) {
		fmt.Fprintf(out, "  %s\n", line)
	}
	return nil
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage: recollect [command]

With no command, recollect serves the JSON-RPC engine on stdin and stdout.

Commands:
  version              print the engine version
  doctor               run an in-process engine probe
  resolve              print the canonical project and user roots
  mcp                  serve the Model Context Protocol on stdin and stdout
  setup opencode       print or write the OpenCode plugin entry

MCP flags:
  --project DIR        project directory (default: current directory)
  --harness NAME       harness identifier (default: mcp)

Doctor flags:
  --user-dir DIR       read config.json from DIR (default: host default)

Resolve flags:
  --root DIR           project directory to resolve (default: current directory)
  --json               print the result as JSON

Setup flags:
  --write              merge into the global OpenCode config
  --adapter DIR        path to adapters/opencode
`)
}
