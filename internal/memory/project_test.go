package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

import (
	"os"
	"path/filepath"
	"recollect/internal/memory"
	"regexp"
	"strings"
	"testing"
)

func mustResolve(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", path, err)
	}
	return resolved
}

func TestDetectProjectRootGitRootFromSubdir(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ".git"))
	sub := filepath.Join(root, "a", "b")
	mustMkdir(t, sub)

	got, err := memory.DetectProjectRoot(sub)
	if err != nil {
		t.Fatalf("DetectProjectRoot: %v", err)
	}
	if want := mustResolve(t, root); got != want {
		t.Fatalf("root = %q, want %q", got, want)
	}
}

func TestDetectProjectRootGitFileCounts(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := memory.DetectProjectRoot(root)
	if err != nil {
		t.Fatalf("DetectProjectRoot: %v", err)
	}
	if want := mustResolve(t, root); got != want {
		t.Fatalf("root = %q, want %q", got, want)
	}
}

func TestDetectProjectRootWithoutGit(t *testing.T) {
	dir := t.TempDir()

	got, err := memory.DetectProjectRoot(dir)
	if err != nil {
		t.Fatalf("DetectProjectRoot: %v", err)
	}
	if want := mustResolve(t, dir); got != want {
		t.Fatalf("root = %q, want %q", got, want)
	}
}

func TestProjectKeyStableAndUnique(t *testing.T) {
	root := t.TempDir()
	first := memory.ProjectKey(root)
	second := memory.ProjectKey(root)
	if first != second {
		t.Fatalf("key not stable: %q vs %q", first, second)
	}
	other := memory.ProjectKey(root + "-other")
	if first == other {
		t.Fatalf("different roots share key %q", first)
	}
	if !regexp.MustCompile(`^[a-z0-9-]+-[0-9a-f]{8}$`).MatchString(first) {
		t.Fatalf("key %q does not match slug-hash8", first)
	}
}

func TestProjectKeySlugSanitization(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "My App!")
	key := memory.ProjectKey(dir)
	wantPrefix := "my-app-"
	if !strings.HasPrefix(key, wantPrefix) {
		t.Errorf("key %q does not start with %q", key, wantPrefix)
	}
	if !regexp.MustCompile(`-[0-9a-f]{8}$`).MatchString(key) {
		t.Errorf("key %q does not end with an 8-hex hash", key)
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"My App!", "my-app"},
		{"already-lower", "already-lower"},
		{"...", "project"},
		{"", "project"},
		{"UPPER_case.with.dots", "upper-case-with-dots"},
		{"trailing---dashes--", "trailing-dashes"},
	}
	for _, tc := range cases {
		if got := memory.SlugifyForTest(tc.name); got != tc.want {
			t.Errorf("memory.SlugifyForTest(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := memory.SlugifyForTest(strings.Repeat("verylongname", 5)); len(got) > 24 {
		t.Errorf("memory.SlugifyForTest(long) = %q exceeds 24 chars", got)
	}
}

func TestProjectDirLayout(t *testing.T) {
	agentDir := t.TempDir()
	root := t.TempDir()
	want := filepath.Join(agentDir, "memory", "projects", memory.ProjectKey(root))
	if got := memory.ProjectDir(agentDir, root); got != want {
		t.Fatalf("ProjectDir = %q, want %q", got, want)
	}
}

func TestProjectMetaFor(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Cool Repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := memory.ProjectMetaFor(root)
	if meta.Root != root {
		t.Errorf("root = %q, want %q", meta.Root, root)
	}
	if meta.Name != "Cool Repo" {
		t.Errorf("name = %q", meta.Name)
	}
	if meta.Slug != "cool-repo" {
		t.Errorf("slug = %q", meta.Slug)
	}
	if !meta.CreatedAt.IsZero() {
		t.Errorf("CreatedAt should be unset before the store stamps it, got %v", meta.CreatedAt)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}
