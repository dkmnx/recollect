// backfill_test.go — chain backfill bookkeeping: path candidates extracted
// from fact text resolve under the workspace root, pending marks skip
// chained and already-marked paths, and the marker map stays bounded.

package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"recollect/internal/memory"
)

// Test-audit record (test-audit skill):
// Protected behavior: ChainBackfillCandidates keeps only tokens that stat
// as regular files under the workspace root, normalizes an absolute path
// under the root to its root-relative chain identity, drops directories,
// missing files, prose tokens, and out-of-root absolutes, and caps the
// result per call.
// Credible regression: prose tokens ("e.g") or directories becoming
// backfill targets (git log on a directory errors, a missing file burns a
// probe every pass), or an uncapped extraction flooding one fact read.
// Coverage gap: the marker tests cannot see token extraction; it is the
// join between fact text and the filesystem.
// Seam check: none — extraction stats real temp files.
func TestChainBackfillCandidates(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"src/search.go", "notes.md"} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(file)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "src", "dir.go"), 0o755); err != nil {
		t.Fatal(err)
	}

	absInside := filepath.Join(root, "notes.md")
	absOutside := filepath.Join(filepath.Dir(root), "elsewhere.md")
	text := "fixed in src/search.go and " + absInside + " per e.g " + absOutside + ", see src/missing.go and src/dir.go"
	got := memory.ChainBackfillCandidates(text, root, 4)
	want := []string{"src/search.go", "notes.md"}
	if len(got) != len(want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidates[%d] = %q, want %q (full %v)", i, got[i], want[i], want)
		}
	}

	for i := 0; i < 6; i++ {
		name := fmt.Sprintf("f%d.go", i)
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	capped := memory.ChainBackfillCandidates("see f0.go f1.go f2.go f3.go f4.go f5.go", root, 4)
	if len(capped) != 4 {
		t.Fatalf("capped candidates = %d, want 4", len(capped))
	}
}

// Test-audit record (test-audit skill):
// Protected behavior: MarkChainBackfillPending records unseen paths with
// an empty (pending) value, ignores paths the chain already holds and
// paths already marked, persists the map across a store reopen, and caps
// the map so capture.json cannot grow without bound.
// Credible regression: re-probing the same file on every pass (the marker
// lost), a chained file re-backfilled (duplicate rows), or an unbounded
// marker map bloating the state file over a project's life.
// Coverage gap: the agent-side detection test covers one mark; the
// chained-skip, persistence, and cap rules live here.
// Seam check: none — the test drives the store's production mark and
// state reads, including a reopen over the same directories.
func TestMarkChainBackfillPending(t *testing.T) {
	store, userDir, projectDir := newTestStore(t, memory.Limits{})
	if err := store.AppendChainRevisions([]memory.ChainRevision{{Path: "chained.go", NewText: "x"}}); err != nil {
		t.Fatalf("seed chain: %v", err)
	}
	chained := map[string]bool{"chained.go": true}
	store.MarkChainBackfillPending([]string{"a.go", "chained.go"}, chained)
	store.MarkChainBackfillPending([]string{"a.go", "b.go"}, chained)
	state, err := store.ReadCaptureState()
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if len(state.ChainBackfill) != 2 || state.ChainBackfill["a.go"] != "" || state.ChainBackfill["b.go"] != "" {
		t.Fatalf("markers = %v, want a.go and b.go pending and chained.go ignored", state.ChainBackfill)
	}

	reopened := memory.NewStore(memory.Options{
		UserDir:    userDir,
		ProjectDir: projectDir,
		Meta:       memory.ProjectMeta{Root: projectDir, Name: "proj", Slug: "proj"},
	})
	state, err = reopened.ReadCaptureState()
	if err != nil {
		t.Fatalf("reopened state: %v", err)
	}
	if len(state.ChainBackfill) != 2 {
		t.Fatalf("reopened markers = %v, want the pending set persisted", state.ChainBackfill)
	}

	for i := 0; i < 300; i++ {
		store.MarkChainBackfillPending([]string{fmt.Sprintf("f%03d.go", i)}, nil)
	}
	state, _ = store.ReadCaptureState()
	if len(state.ChainBackfill) > 256 {
		t.Fatalf("markers = %d entries, want the map bounded at 256", len(state.ChainBackfill))
	}
}
