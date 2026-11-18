// chain_test.go — the revision chain store: applied edits persist
// append-only across store restarts, read back per path in write order and
// capped, with distinct paths listed once.

package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

import (
	"testing"
	"time"

	"github.com/dkmnx/recollect/internal/memory"
)

// Test-audit record (test-audit skill):
// Protected behavior: chain revisions persist append-only, survive a store
// reopen, read back per path in write order capped at max, and ChainPaths
// lists each distinct path once.
// Credible regression: the path filter returning another file's revisions,
// the cap ignored (an unbounded diff payload lands in a tool result), the
// paths list duplicating or dropping files, or a store restart losing the
// chain (the store's durable-state rule).
// Coverage gap: no chain store exists; nothing else can catch these.
// Seam check: none — the test drives the store's production append and
// read entries, including a reopen over the same directory.
func TestChainStore(t *testing.T) {
	store, userDir, projectDir := newTestStore(t, memory.Limits{})
	at := deterministicNow
	if err := store.AppendChainRevisions([]memory.ChainRevision{
		{Path: "src/search.go", SessionID: "s1", At: at, OldText: "timeout := 5", NewText: "timeout := 10"},
		{Path: "src/search.go", SessionID: "s1", At: at, OldText: "retry()", NewText: "retry(twice)"},
		{Path: "src/other.go", SessionID: "s2", At: at, OldText: "alpha", NewText: "beta"},
	}); err != nil {
		t.Fatalf("append revisions: %v", err)
	}

	capped, err := store.ChainRevisions("src/search.go", 1)
	if err != nil {
		t.Fatalf("read capped: %v", err)
	}
	if len(capped) != 1 || capped[0].NewText != "retry(twice)" {
		t.Fatalf("capped read = %+v, want only the newest revision", capped)
	}
	all, err := store.ChainRevisions("src/search.go", 0)
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if len(all) != 2 || all[0].OldText != "timeout := 5" || all[1].NewText != "retry(twice)" {
		t.Fatalf("full read = %+v, want both revisions in write order", all)
	}
	if missing, err := store.ChainRevisions("src/absent.go", 10); err != nil || len(missing) != 0 {
		t.Fatalf("absent path = %+v (%v), want empty with no error", missing, err)
	}
	paths, err := store.ChainPaths()
	if err != nil {
		t.Fatalf("paths: %v", err)
	}
	if len(paths) != 2 || paths[0] != "src/other.go" || paths[1] != "src/search.go" {
		t.Fatalf("paths = %v, want each distinct path once, sorted", paths)
	}

	reopened := memory.NewStore(memory.Options{
		UserDir:    userDir,
		ProjectDir: projectDir,
		Meta:       memory.ProjectMeta{Root: projectDir, Name: "proj", Slug: "proj"},
		Now:        func() time.Time { return deterministicNow },
	})
	survived, err := reopened.ChainRevisions("src/search.go", 0)
	if err != nil || len(survived) != 2 {
		t.Fatalf("reopened store chain = %+v (%v), want both revisions", survived, err)
	}
}
