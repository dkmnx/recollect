package memory_test

// audit: behavior - chain revisions carry a durable event identity, so identical edits stay distinct.
// audit: regression - content-based dedupe dropped the second of two identical edits in one record.
// audit: coverage gap - existing chain tests use distinct edit text.
// audit: seam - Store.AppendChainRevisions and Store.ChainRevisions over a temporary project.

import (
	"testing"
	"time"

	"github.com/dkmnx/recollect/internal/memory"
)

func TestAppendChainRevisionsKeepsDistinctIDs(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	at := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	batch := []memory.ChainRevision{
		{ID: "call-1", Path: "main.go", At: at, OldText: "x", NewText: "y"},
		{ID: "call-2", Path: "main.go", At: at, OldText: "x", NewText: "y"},
	}
	if err := store.AppendChainRevisions(batch); err != nil {
		t.Fatal(err)
	}
	got, err := store.ChainRevisions("main.go", -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("chain revisions = %d, want 2", len(got))
	}
	if err := store.AppendChainRevisions(batch); err != nil {
		t.Fatal(err)
	}
	got, err = store.ChainRevisions("main.go", -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("chain revisions after replay = %d, want 2", len(got))
	}
}
