// journal_test.go — the change journal: one line per store mutation with
// the fact's session provenance, rotation at the size bound, and a journal
// failure that never fails the memory operation it rode on.

package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

import (
	"os"
	"path/filepath"
	"recollect/internal/memory"
	"strings"
	"testing"
)

func journalLines(t *testing.T, projectDir string) []memory.JournalEntry {
	t.Helper()
	raw, err := os.ReadFile(memory.JournalFile(projectDir))
	if err != nil {
		t.Fatal(err)
	}
	var entries []memory.JournalEntry
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		entry, err := memory.DecodeJournalLineForTest(line)
		if err != nil {
			t.Fatalf("journal line %q: %v", line, err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func TestJournalRecordsFactMutations(t *testing.T) {
	store, _, projectDir := newTestStore(t, memory.Limits{})

	if _, err := store.AppendFact(memory.Fact{Name: "alpha", Description: "first fact", Body: "one", OriginSessionID: "sess-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertFact(memory.Fact{Name: "alpha", Description: "rewritten fact", Body: "two", OriginSessionID: "sess-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateFact("alpha", "corrected fact", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.EditFact("alpha", "two", "three", false); err != nil {
		t.Fatal(err)
	}
	moved, err := store.MoveFactToTopic("alpha", "ops")
	if err != nil {
		t.Fatal(err)
	}
	if deleted, err := store.DeleteFact(moved.Name); err != nil || !deleted {
		t.Fatalf("delete = %t (%v)", deleted, err)
	}

	entries := journalLines(t, projectDir)
	ops := make([]string, len(entries))
	for i, entry := range entries {
		ops[i] = entry.Op
	}
	want := []string{"append", "upsert", "update", "edit", "demote", "drop"}
	if len(ops) != len(want) {
		t.Fatalf("journal ops = %v, want %v", ops, want)
	}
	for i := range want {
		if ops[i] != want[i] {
			t.Fatalf("journal ops = %v, want %v", ops, want)
		}
		if entries[i].Key != moved.Name {
			t.Fatalf("entry %d key = %q, want the resolved name %q", i, entries[i].Key, moved.Name)
		}
		if entries[i].Session != "sess-1" {
			t.Fatalf("entry %d session = %q", i, entries[i].Session)
		}
	}
	if entries[0].New != "first fact" {
		t.Fatalf("append new summary = %q", entries[0].New)
	}
	if entries[1].Old != "first fact" || entries[1].New != "rewritten fact" {
		t.Fatalf("upsert old/new = %q/%q", entries[1].Old, entries[1].New)
	}
}

func TestJournalSkipsNoopWrites(t *testing.T) {
	store, _, projectDir := newTestStore(t, memory.Limits{})
	if _, err := store.AppendFact(memory.Fact{Name: "kept", Description: "same", Body: "same body"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertFact(memory.Fact{Name: "kept", Description: "same", Body: "same body"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteFact("missing"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendFact(memory.Fact{Name: "kept-2", Description: "duplicate", Body: "same body"}); err == nil {
		t.Fatal("expected duplicate refusal")
	}
	if entries := journalLines(t, projectDir); len(entries) != 1 {
		t.Fatalf("noop writes journalized: %d entries", len(entries))
	}
}

func TestJournalRotatesAtBound(t *testing.T) {
	store, _, projectDir := newTestStore(t, memory.Limits{})
	if err := os.WriteFile(memory.JournalFile(projectDir), []byte(strings.Repeat("x", memory.JournalRotateBytesForTest)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendFact(memory.Fact{Name: "fresh", Description: "after rotation", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	prev, err := os.Stat(filepath.Join(projectDir, memory.JournalPrevFileForTest))
	if err != nil || prev.Size() < memory.JournalRotateBytesForTest {
		t.Fatalf("prev journal = %v (%v)", prev, err)
	}
	raw, err := os.ReadFile(memory.JournalFile(projectDir))
	if err != nil || len(raw) > 4096 {
		t.Fatalf("fresh journal = %d bytes (%v)", len(raw), err)
	}
	if !strings.Contains(string(raw), `"op":"append"`) {
		t.Fatalf("fresh journal lost the new entry: %q", raw)
	}
}

func TestJournalFailureNeverFailsTheWrite(t *testing.T) {
	store, _, projectDir := newTestStore(t, memory.Limits{})
	if err := os.MkdirAll(memory.JournalFile(projectDir), 0o755); err != nil {
		t.Fatal(err)
	}
	written, err := store.AppendFact(memory.Fact{Name: "stubborn", Description: "lands anyway", Body: "b"})
	if err != nil {
		t.Fatalf("fact write failed with the journal blocked: %v", err)
	}
	if _, err := store.ReadFact(written.Name); err != nil {
		t.Fatalf("fact did not land: %v", err)
	}
}

func TestJournalStatusReadsTheLastEntry(t *testing.T) {
	store, _, projectDir := newTestStore(t, memory.Limits{})
	if _, _, ok := store.JournalStatus(); ok {
		t.Fatal("empty store reported a journal")
	}
	if _, err := store.AppendFact(memory.Fact{Name: "last", Description: "the newest fact", Body: "b", OriginSessionID: "sess-7"}); err != nil {
		t.Fatal(err)
	}
	size, last, ok := store.JournalStatus()
	if !ok || size == 0 {
		t.Fatalf("status = %d bytes, ok %t", size, ok)
	}
	if last.Op != "append" || last.Key != "last" || last.Session != "sess-7" {
		t.Fatalf("last entry = %+v", last)
	}
	if _, err := os.Stat(memory.JournalFile(projectDir)); err != nil {
		t.Fatal(err)
	}
}

func TestJournalSkipsMissingTopic(t *testing.T) {
	store, _, projectDir := newTestStore(t, memory.Limits{})
	deleted, err := store.DeleteTopic("nope")
	if err != nil || deleted {
		t.Fatalf("missing topic delete = %t (%v)", deleted, err)
	}
	if _, err := os.Stat(memory.JournalFile(projectDir)); !os.IsNotExist(err) {
		t.Fatalf("missing topic journalized: %v", err)
	}
}
