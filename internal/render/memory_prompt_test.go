package render_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.
import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dkmnx/recollect/internal/memory"
	"github.com/dkmnx/recollect/internal/render"
)

func writeMemoryFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testStore(t *testing.T, root string) (*memory.Store, string) {
	t.Helper()
	agentDir := t.TempDir()
	projectDir := t.TempDir()
	writeMemoryFile(t, memory.UserFile(memory.MemoryRoot(agentDir)), "prefer tabs")
	writeMemoryFile(t, memory.IndexFile(projectDir),
		"- [build-check](build-check.md) — make check is the gate\n")
	writeMemoryFile(t, filepath.Join(memory.SessionsDir(projectDir), "2026-09-19-sess1.md"),
		memory.FormatDigest(memory.Digest{
			SessionID: "sess-1",
			Topic:     "stream retry",
			Summary:   "session loop owns mid-stream drops",
			Date:      time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC),
			Updated:   time.Date(2026, 9, 19, 11, 0, 0, 0, time.UTC),
		}))
	writeMemoryFile(t, filepath.Join(memory.PagesDir(projectDir), "conventions.md"),
		memory.FormatPage(memory.Page{
			Name:      "conventions",
			Question:  "Which conventions does this project follow?",
			Sources:   []string{"tabs"},
			UpdatedOn: "2026-10-23",
			Body:      "Go sources use tabs.",
		}))
	store := memory.NewStore(memory.Options{
		UserDir:    memory.MemoryRoot(agentDir),
		ProjectDir: projectDir,
		Meta:       memory.ProjectMetaFor(root),
	})
	return store, projectDir
}

func TestMemorySectionLayers(t *testing.T) {
	root := filepath.Join(t.TempDir(), "proj")
	store, _ := testStore(t, root)
	section := render.MemorySection(render.Config{Enabled: true, Store: store, Root: root})
	for _, want := range []string{
		"## Memory",
		"<user_memory",
		"prefer tabs",
		"<project_memory",
		"- [build-check](build-check.md) — make check is the gate",
		"sessions: sess-1 — stream retry: session loop owns mid-stream drops",
		"<project_pages>",
		"name=\"conventions\"",
		"Go sources use tabs.",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("section lacks %q:\n%s", want, section)
		}
	}
}

func TestMemorySectionIsStable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "proj")
	store, _ := testStore(t, root)
	cfg := render.Config{Enabled: true, Store: store, Root: root}
	first := render.MemorySection(cfg)
	second := render.MemorySection(cfg)
	if first != second {
		t.Fatalf("unchanged store rendered differently:\n%s\n%s", first, second)
	}
}

func TestMemorySectionDisabled(t *testing.T) {
	if section := render.MemorySection(render.Config{Enabled: false}); section != "" {
		t.Fatalf("disabled section = %q, want empty", section)
	}
}

func TestMemorySectionEmptyStillTeaches(t *testing.T) {
	root := filepath.Join(t.TempDir(), "proj")
	agentDir := t.TempDir()
	projectDir := t.TempDir()
	store := memory.NewStore(memory.Options{
		UserDir:    memory.MemoryRoot(agentDir),
		ProjectDir: projectDir,
		Meta:       memory.ProjectMetaFor(root),
	})
	section := render.MemorySection(render.Config{Enabled: true, Store: store, Root: root})
	if !strings.Contains(section, "## Memory") || !strings.Contains(section, "No-op is") {
		t.Fatalf("instructions missing:\n%s", section)
	}
	if strings.Contains(section, "<user_memory") || strings.Contains(section, "<project_memory") {
		t.Fatalf("empty store should have no data blocks:\n%s", section)
	}
}

func TestMemorySectionTruncatesIndex(t *testing.T) {
	root := filepath.Join(t.TempDir(), "proj")
	agentDir := t.TempDir()
	projectDir := t.TempDir()
	var b strings.Builder
	for i := 0; i < memory.IndexLineLimit+10; i++ {
		b.WriteString("- [fact-" + fmt.Sprintf("%03d", i) + "](f.md) — line\n")
	}
	writeMemoryFile(t, memory.IndexFile(projectDir), b.String())
	store := memory.NewStore(memory.Options{
		UserDir:    memory.MemoryRoot(agentDir),
		ProjectDir: projectDir,
		Meta:       memory.ProjectMetaFor(root),
	})
	section := render.MemorySection(render.Config{Enabled: true, Store: store, Root: root})
	if !strings.Contains(section, "WARNING: MEMORY.md is") {
		t.Fatalf("truncation warning missing:\n%s", section)
	}
}

func TestMemorySectionClampsPages(t *testing.T) {
	root := filepath.Join(t.TempDir(), "proj")
	agentDir := t.TempDir()
	projectDir := t.TempDir()
	writeMemoryFile(t, filepath.Join(memory.PagesDir(projectDir), "component-map.md"),
		memory.FormatPage(memory.Page{
			Name:      "component-map",
			Question:  "How is this project's code organized?",
			UpdatedOn: "2026-10-22",
			Body:      strings.Repeat("filler ", 3000),
		}))
	store := memory.NewStore(memory.Options{
		UserDir:    memory.MemoryRoot(agentDir),
		ProjectDir: projectDir,
		Meta:       memory.ProjectMetaFor(root),
	})
	section := render.MemorySection(render.Config{Enabled: true, Store: store, Root: root})
	start := strings.Index(section, "<project_pages>")
	if start < 0 {
		t.Fatalf("pages block missing:\n%s", section)
	}
	block := section[start:]
	if end := strings.Index(block, "</project_pages>"); end >= 0 {
		block = block[:end+len("</project_pages>")]
	}
	if !strings.Contains(block, "[truncated; full pages:") {
		t.Fatalf("pages clamp missing:\n%s", block)
	}
	if len(block) > memory.DefaultPagesInjectBytes+256 {
		t.Fatalf("pages block = %d bytes, want clamped near %d", len(block), memory.DefaultPagesInjectBytes)
	}
}
