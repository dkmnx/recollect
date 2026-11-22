package bindings_test

// audit: behavior - bindings resolve the same project ID as the OpenCode adapter and the same user root as its TS helper.
// audit: regression - a different canonical root or user root would put one project's memory in two stores.
// audit: coverage gap - the derivation lived only in TypeScript; Go had no parity fixtures.
// audit: seam - tests call Resolve, CanonicalRoot, ProjectID, and DefaultUserDir directly; git fixtures use real repositories and worktrees.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dkmnx/recollect/internal/bindings"
)

func lookupFunc(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

// resolvedTempDir matches what CanonicalRoot returns on hosts where the
// temporary directory is reached through a symlink, such as macOS.
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", dir, err)
	}
	return resolved
}

func TestDefaultUserDir(t *testing.T) {
	cases := []struct {
		name string
		goos string
		env  map[string]string
		want string
	}{
		{"windows localappdata", "windows", map[string]string{"LOCALAPPDATA": `C:\Users\x\AppData\Local`}, `C:\Users\x\AppData\Local\recollect`},
		{"windows userprofile", "windows", map[string]string{"USERPROFILE": `C:\Users\x`}, `C:\Users\x\AppData\Local\recollect`},
		{"windows default", "windows", map[string]string{}, `C:\Users\default\AppData\Local\recollect`},
		{"windows empty localappdata", "windows", map[string]string{"LOCALAPPDATA": ""}, "recollect"},
		{"windows unc localappdata", "windows", map[string]string{"LOCALAPPDATA": `\\server\share\appdata`}, `\\server\share\appdata\recollect`},
		{"linux home", "linux", map[string]string{"HOME": "/home/u"}, "/home/u/.recollect"},
		{"linux missing home", "linux", map[string]string{}, "~/.recollect"},
		{"linux empty home", "linux", map[string]string{"HOME": ""}, ".recollect"},
		{"darwin home", "darwin", map[string]string{"HOME": "/Users/u"}, "/Users/u/.recollect"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := bindings.DefaultUserDir(testCase.goos, lookupFunc(testCase.env))
			if got != testCase.want {
				t.Fatalf("DefaultUserDir = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestProjectIDMatchesOpenCode(t *testing.T) {
	cases := map[string]string{
		"/work/demo":               "111b1182b4b056ca",
		"/work/demo/sub":           "0abdc714cd61312c",
		`C:\work\demo`:             "486842466e33688a",
		`C:\Users\x\AppData\Local`: "9df2c2af9192dd6c",
		"/home/u":                  "f9e409c5c16ecfdd",
	}
	for canonical, want := range cases {
		if got := bindings.ProjectID(canonical); got != want {
			t.Errorf("ProjectID(%q) = %q, want %q", canonical, got, want)
		}
	}
}

func TestCanonicalRootPlainDirectory(t *testing.T) {
	dir := t.TempDir()
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	got, err := bindings.CanonicalRoot(dir)
	if err != nil {
		t.Fatalf("CanonicalRoot: %v", err)
	}
	if got != want {
		t.Fatalf("CanonicalRoot = %q, want %q", got, want)
	}
}

func TestCanonicalRootGitRootAndSubdirectory(t *testing.T) {
	root := resolvedTempDir(t)
	gitRun(t, root, "init", "-q")
	sub := filepath.Join(root, "nested", "deeper")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for _, input := range []string{root, sub} {
		got, err := bindings.CanonicalRoot(input)
		if err != nil {
			t.Fatalf("CanonicalRoot(%q): %v", input, err)
		}
		if got != root {
			t.Fatalf("CanonicalRoot(%q) = %q, want %q", input, got, root)
		}
		if runtime.GOOS == "windows" && strings.Contains(got, "/") {
			t.Fatalf("CanonicalRoot(%q) returned forward slashes: %q", input, got)
		}
	}
}

func TestCanonicalRootSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs a privilege on Windows")
	}
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	got, err := bindings.CanonicalRoot(link)
	if err != nil {
		t.Fatalf("CanonicalRoot: %v", err)
	}
	if got != want {
		t.Fatalf("CanonicalRoot(link) = %q, want %q", got, want)
	}
}

func TestCanonicalRootLinkedWorktreeUsesMainCheckout(t *testing.T) {
	main := resolvedTempDir(t)
	gitRun(t, main, "init", "-q")
	gitRun(t, main, "config", "user.email", "test@example.com")
	gitRun(t, main, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(main, "file.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	gitRun(t, main, "add", ".")
	gitRun(t, main, "commit", "-q", "-m", "init")

	linked := filepath.Join(resolvedTempDir(t), "linked")
	gitRun(t, main, "worktree", "add", "-q", linked, "-b", "topic")
	sub := filepath.Join(linked, "nested")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for _, input := range []string{linked, sub} {
		got, err := bindings.CanonicalRoot(input)
		if err != nil {
			t.Fatalf("CanonicalRoot(%q): %v", input, err)
		}
		if got != main {
			t.Fatalf("CanonicalRoot(%q) = %q, want main checkout %q", input, got, main)
		}
		if runtime.GOOS == "windows" && strings.Contains(got, "/") {
			t.Fatalf("CanonicalRoot(%q) returned forward slashes: %q", input, got)
		}
	}
}

func TestCanonicalRootLinkedWorktreeWithoutGit(t *testing.T) {
	main := resolvedTempDir(t)
	gitDir := filepath.Join(main, ".git", "worktrees", "topic")
	if err := os.MkdirAll(gitDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	linked := resolvedTempDir(t)
	if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("PATH", "")
	got, err := bindings.CanonicalRoot(linked)
	if err != nil {
		t.Fatalf("CanonicalRoot: %v", err)
	}
	if got != main {
		t.Fatalf("CanonicalRoot = %q, want %q", got, main)
	}
}

func TestResolve(t *testing.T) {
	root := resolvedTempDir(t)
	gitRun(t, root, "init", "-q")
	resolved, err := bindings.Resolve(root, "/srv/memory")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.ProjectRoot != root {
		t.Fatalf("ProjectRoot = %q, want %q", resolved.ProjectRoot, root)
	}
	if resolved.UserDir != "/srv/memory" {
		t.Fatalf("UserDir = %q, want /srv/memory", resolved.UserDir)
	}
	wantDir := filepath.Join("/srv/memory", "projects", bindings.ProjectID(root))
	if resolved.ProjectDir != wantDir {
		t.Fatalf("ProjectDir = %q, want %q", resolved.ProjectDir, wantDir)
	}
}

func TestResolveUsesHostDefaultUserDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX home assertion")
	}
	t.Setenv("HOME", "/home/parity")
	resolved, err := bindings.Resolve(t.TempDir(), "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.UserDir != "/home/parity/.recollect" {
		t.Fatalf("UserDir = %q, want /home/parity/.recollect", resolved.UserDir)
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
