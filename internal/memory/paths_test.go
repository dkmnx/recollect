package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

import (
	"path/filepath"
	"recollect/internal/memory"
	"runtime"
	"strings"
	"testing"
)

func TestContainPathKeepsInside(t *testing.T) {
	// Native root: "/r" is not absolute on Windows, so containPath would join
	// onto it instead of treating it as the boundary.
	root := filepath.Join(string(filepath.Separator), "r")
	got, err := memory.ContainPathForTest(root, filepath.Join("topics", "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "topics", "a.md"); got != want {
		t.Fatalf("containPath = %q, want %q", got, want)
	}
}

func TestContainPathRefusesEscapes(t *testing.T) {
	// The absolute outsider must be spelled natively: on Windows "/etc/passwd"
	// is just a relative name the root absorbs, so it would not be an escape.
	absoluteOutside := "/etc/passwd"
	if runtime.GOOS == "windows" {
		absoluteOutside = `C:\Windows\System32\config\SAM`
	}
	for _, name := range []string{
		absoluteOutside,
		"../../.ssh/id_rsa",
		"..",
		"a/../../b",
		`..\secret`,
		`a\..\..\b`,
		"",
	} {
		if _, err := memory.ContainPathForTest(filepath.Join(string(filepath.Separator), "r"), name); err == nil {
			t.Errorf("memory.ContainPathForTest(%q) admitted an escape", name)
		}
		if _, err := memory.ContainPathForTest(filepath.Join(string(filepath.Separator), "r"), name); memory.CodeOf(err) != memory.CodePathEscape {
			t.Errorf("memory.ContainPathForTest(%q) code = %q, want %q", name, memory.CodeOf(err), memory.CodePathEscape)
		}
	}
}

func TestTopicFactPathStaysContained(t *testing.T) {
	store := memory.NewStore(memory.Options{UserDir: t.TempDir(), ProjectDir: t.TempDir(), Meta: memory.ProjectMeta{Root: "/r"}})
	fact, err := store.AppendFact(memory.Fact{Name: "notes", Description: "d", Path: "topics/notes"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.ProjectDir(), fact.RelPath())
	if !strings.HasPrefix(path, store.ProjectDir()) {
		t.Fatalf("topic fact path %q escapes %q", path, store.ProjectDir())
	}
	if _, err := store.AppendFact(memory.Fact{Name: "evil", Description: "d", Path: "topics/../../.ssh"}); err == nil || memory.CodeOf(err) != memory.CodeInvalidTopic {
		t.Fatalf("traversal topic admitted: %v", err)
	}
}
