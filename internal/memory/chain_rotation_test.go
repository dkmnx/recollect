package memory

// audit: behavior - replaying a revision already in the active chain does not copy it into the rotated segment.
// audit: regression - rotation ran before dedupe, so an applied revision appeared in both retained files.
// audit: coverage gap - existing chain tests never combine a replay with a file at the rotation threshold.
// audit: seam - Store.AppendChainRevisions with a padded active chain and a repeated event ID.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChainReplayAtRotationDoesNotDuplicate(t *testing.T) {
	project := t.TempDir()
	store := NewStore(Options{ProjectDir: project, UserDir: t.TempDir(),
		Meta: ProjectMeta{Root: project, Name: "proj", Slug: "proj"}})
	active := ChainFile(project)
	seed := `{"id":"rotate-id","path":"main.go","at":"2026-09-23T12:00:00Z","newText":"y"}` + "\n"
	if err := os.WriteFile(active, []byte(strings.Repeat(seed, chainRotateBytes/len(seed)+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	replay := ChainRevision{ID: "rotate-id", Path: "main.go", At: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC), NewText: "y"}
	fresh := ChainRevision{ID: "fresh-id", Path: "main.go", At: time.Date(2026, 9, 23, 12, 1, 0, 0, time.UTC), NewText: "z"}
	if err := store.AppendChainRevisions([]ChainRevision{replay, fresh}); err != nil {
		t.Fatal(err)
	}
	prev, err := os.ReadFile(filepath.Join(project, chainPrevFile))
	if err != nil || !strings.Contains(string(prev), "rotate-id") {
		t.Fatalf("rotation did not preserve the old segment: %v", err)
	}
	if err := store.AppendChainRevisions([]ChainRevision{replay}); err != nil {
		t.Fatal(err)
	}
	newActive, err := os.ReadFile(active)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(newActive), "rotate-id") {
		t.Fatal("replay copied the rotated revision into the active chain")
	}
}

func TestChainAppendRestoresMissingActiveFromPrevious(t *testing.T) {
	project := t.TempDir()
	store := NewStore(Options{ProjectDir: project, UserDir: t.TempDir(),
		Meta: ProjectMeta{Root: project, Name: "proj", Slug: "proj"}})
	seed := `{"id":"prev-id","path":"main.go","at":"2026-09-23T12:00:00Z","newText":"y"}` + "\n"
	if err := os.WriteFile(filepath.Join(project, chainPrevFile), []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	revision := ChainRevision{ID: "new-id", Path: "main.go", At: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC), NewText: "z"}
	if err := store.AppendChainRevisions([]ChainRevision{revision}); err != nil {
		t.Fatal(err)
	}
	active, err := os.ReadFile(ChainFile(project))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(active), "prev-id") || !strings.Contains(string(active), "new-id") {
		t.Fatalf("active chain = %q, want both revisions", active)
	}
}

func TestChainRotationWritesOnlyFreshEntries(t *testing.T) {
	project := t.TempDir()
	store := NewStore(Options{ProjectDir: project, UserDir: t.TempDir(),
		Meta: ProjectMeta{Root: project, Name: "proj", Slug: "proj"}})
	old := `{"id":"old-id","path":"main.go","at":"2026-09-23T12:00:00Z","newText":"old"}` + "\n"
	if err := os.WriteFile(ChainFile(project), []byte(strings.Repeat(old, chainRotateBytes/len(old)+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	if err := store.AppendChainRevisions([]ChainRevision{{ID: "fresh-1", Path: "main.go", At: at, NewText: "one"}}); err != nil {
		t.Fatal(err)
	}
	active, err := os.ReadFile(ChainFile(project))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(active), "old-id") {
		t.Fatal("old revision stayed in the active segment after rotation")
	}
	prev, err := os.ReadFile(filepath.Join(project, chainPrevFile))
	if err != nil || !strings.Contains(string(prev), "old-id") {
		t.Fatalf("old revision missing from rotated segment: %v", err)
	}
	if err := store.AppendChainRevisions([]ChainRevision{{ID: "fresh-2", Path: "main.go", At: at, NewText: "two"}}); err != nil {
		t.Fatal(err)
	}
	prev2, err := os.ReadFile(filepath.Join(project, chainPrevFile))
	if err != nil || string(prev2) != string(prev) {
		t.Fatalf("second append rotated again: %v", err)
	}
}

func TestChainRotationFailureKeepsActiveSegment(t *testing.T) {
	project := t.TempDir()
	store := NewStore(Options{ProjectDir: project, UserDir: t.TempDir(),
		Meta: ProjectMeta{Root: project, Name: "proj", Slug: "proj"}})
	active := ChainFile(project)
	seed := `{"id":"old-id","path":"main.go","at":"2026-09-23T12:00:00Z","newText":"y"}` + "\n"
	if err := os.WriteFile(active, []byte(strings.Repeat(seed, chainRotateBytes/len(seed)+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	// A non-empty directory at the previous path makes the rename fail.
	if err := os.MkdirAll(filepath.Join(project, chainPrevFile, "block"), 0o755); err != nil {
		t.Fatal(err)
	}
	revision := ChainRevision{ID: "fresh-id", Path: "main.go", At: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC), NewText: "z"}
	if err := store.AppendChainRevisions([]ChainRevision{revision}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(active)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "old-id") || !strings.Contains(string(data), "fresh-id") {
		t.Fatalf("active chain lost data after a failed rotation: %q", data)
	}
}
