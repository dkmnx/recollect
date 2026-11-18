package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"recollect/internal/memory"
	"testing"
	"time"
)

func newDigestStore(t *testing.T, now time.Time, limits memory.Limits) *memory.Store {
	t.Helper()
	return memory.NewStore(memory.Options{
		UserDir:    filepath.Join(t.TempDir(), "user"),
		ProjectDir: filepath.Join(t.TempDir(), "project"),
		Now:        func() time.Time { return now },
		Limits:     limits,
	})
}

func testDigest(session string, updated time.Time, summary string) memory.Digest {
	return memory.Digest{
		SessionID: session,
		Topic:     "work",
		Summary:   summary,
		Date:      updated.Add(-time.Hour),
		Updated:   updated,
	}
}

func TestWriteAndListDigests(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	store := newDigestStore(t, now, memory.Limits{})
	older := testDigest("s-old", now.Add(-2*time.Hour), "older summary")
	newer := testDigest("s-new", now.Add(-1*time.Hour), "newer summary")
	if _, err := store.WriteDigest(older); err != nil {
		t.Fatal(err)
	}
	path, err := store.WriteDigest(newer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("digest file missing: %v", err)
	}
	digests, err := store.ListDigests()
	if err != nil {
		t.Fatal(err)
	}
	if len(digests) != 2 {
		t.Fatalf("expected 2 digests, got %d", len(digests))
	}
	if digests[0].SessionID != "s-new" || digests[1].SessionID != "s-old" {
		t.Fatalf("order = %s, %s", digests[0].SessionID, digests[1].SessionID)
	}
	if digests[0].Summary != "newer summary" || digests[0].Topic != "work" {
		t.Errorf("digest round trip = %+v", digests[0])
	}
}

func TestWriteDigestOverwritesSameSession(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	store := newDigestStore(t, now, memory.Limits{})
	if _, err := store.WriteDigest(testDigest("s1", now, "first")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteDigest(testDigest("s1", now.Add(time.Minute), "second")); err != nil {
		t.Fatal(err)
	}
	digests, err := store.ListDigests()
	if err != nil {
		t.Fatal(err)
	}
	if len(digests) != 1 || digests[0].Summary != "second" {
		t.Fatalf("overwrite = %+v", digests)
	}
}

func TestPruneByCount(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	store := newDigestStore(t, now, memory.Limits{MaxDigests: 3})
	for i := 0; i < 5; i++ {
		if _, err := store.WriteDigest(testDigest(sessionName(i), now.Add(time.Duration(i)*time.Minute), "summary")); err != nil {
			t.Fatal(err)
		}
	}
	digests, err := store.ListDigests()
	if err != nil {
		t.Fatal(err)
	}
	if len(digests) != 3 {
		t.Fatalf("expected 3 searchable digests, got %d", len(digests))
	}
	if digests[0].SessionID != sessionName(4) {
		t.Errorf("newest kept = %s", digests[0].SessionID)
	}
	archived, err := os.ReadDir(memory.ArchiveSessionsDir(store.ProjectDir()))
	if err != nil {
		t.Fatalf("archive dir: %v", err)
	}
	if len(archived) != 2 {
		t.Errorf("expected 2 archived digests, got %d", len(archived))
	}
}

func TestPruneByTtl(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	store := newDigestStore(t, now, memory.Limits{DigestTTLDays: 30})
	if _, err := store.WriteDigest(testDigest("fresh", now, "fresh")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteDigest(testDigest("ten", now.AddDate(0, 0, -10), "ten days")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteDigest(testDigest("forty", now.AddDate(0, 0, -40), "forty days")); err != nil {
		t.Fatal(err)
	}
	digests, err := store.ListDigests()
	if err != nil {
		t.Fatal(err)
	}
	if len(digests) != 2 {
		t.Fatalf("expected 2 digests, got %d (%+v)", len(digests), digests)
	}
	for _, digest := range digests {
		if digest.SessionID == "forty" {
			t.Errorf("40-day digest stayed searchable")
		}
	}
}

func TestNewestDigestAlwaysKept(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	store := newDigestStore(t, now, memory.Limits{DigestTTLDays: 30})
	if _, err := store.WriteDigest(testDigest("ancient", now.AddDate(0, 0, -400), "very old")); err != nil {
		t.Fatal(err)
	}
	digests, err := store.ListDigests()
	if err != nil {
		t.Fatal(err)
	}
	if len(digests) != 1 || digests[0].SessionID != "ancient" {
		t.Fatalf("newest digest not kept: %+v", digests)
	}
}

func TestRetainDigestsZeroDisablesBounds(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	var digests []memory.Digest
	for i := 0; i < 30; i++ {
		digests = append(digests, testDigest(sessionName(i), now.AddDate(0, 0, -i*10), "summary"))
	}
	keep, archive := memory.RetainDigests(digests, now, memory.DigestRetention{})
	if len(keep) != 30 || len(archive) != 0 {
		t.Fatalf("zero bounds kept %d archived %d", len(keep), len(archive))
	}
}

func TestPruneNegativeBoundsKeepAll(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	store := newDigestStore(t, now, memory.Limits{MaxDigests: -1, DigestTTLDays: -1})
	for i := 0; i < 5; i++ {
		if _, err := store.WriteDigest(testDigest(sessionName(i), now.AddDate(0, 0, -i*100), "summary")); err != nil {
			t.Fatal(err)
		}
	}
	moved, err := store.PruneDigests()
	if err != nil {
		t.Fatal(err)
	}
	if moved != 0 {
		t.Fatalf("negative bounds archived %d digests", moved)
	}
	digests, err := store.ListDigests()
	if err != nil {
		t.Fatal(err)
	}
	if len(digests) != 5 {
		t.Fatalf("negative bounds kept %d digests", len(digests))
	}
}

func TestRetainDigestsDeterministicTieBreak(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	stamp := now.Add(-time.Hour)
	digests := []memory.Digest{
		{SessionID: "b", Updated: stamp},
		{SessionID: "a", Updated: stamp},
		{SessionID: "c", Updated: stamp},
	}
	keep, archive := memory.RetainDigests(digests, now, memory.DigestRetention{MaxCount: 2})
	if len(keep) != 2 || len(archive) != 1 {
		t.Fatalf("keep %d archive %d", len(keep), len(archive))
	}
	if keep[0].SessionID != "a" || keep[1].SessionID != "b" || archive[0].SessionID != "c" {
		t.Fatalf("tie break = %s,%s archive %s", keep[0].SessionID, keep[1].SessionID, archive[0].SessionID)
	}
}

func TestListDigestsSkipsUnparseable(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	store := newDigestStore(t, now, memory.Limits{})
	if _, err := store.WriteDigest(testDigest("s1", now, "summary")); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(memory.SessionsDir(store.ProjectDir()), "stray.md")
	if err := os.WriteFile(stray, []byte("no session line here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	digests, err := store.ListDigests()
	if err != nil {
		t.Fatal(err)
	}
	if len(digests) != 1 {
		t.Fatalf("stray file parsed as digest: %+v", digests)
	}
}

func TestReadDigestNotFound(t *testing.T) {
	store := newDigestStore(t, time.Now(), memory.Limits{})
	_, err := store.ReadDigest("missing")
	if !errors.Is(err, &memory.Error{Code: memory.CodeDigestNotFound}) && memory.CodeOf(err) != memory.CodeDigestNotFound {
		t.Fatalf("expected CodeDigestNotFound, got %v", err)
	}
}

func sessionName(i int) string {
	return "s" + string(rune('a'+i))
}

func TestArchiveRetention(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	store := newDigestStore(t, now, memory.Limits{})
	archive := memory.ArchiveDir(store.ProjectDir())
	old := filepath.Join(archive, "2020-01-01")
	recent := filepath.Join(archive, now.Format("2006-01-02"))
	legacy := filepath.Join(archive, "legacy-20260101T000000")
	for _, dir := range []string{old, recent, legacy} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("snapshot"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sessions := memory.ArchiveSessionsDir(store.ProjectDir())
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < memory.ArchiveDigestKeepForTest+5; i++ {
		name := filepath.Join(sessions, fmt.Sprintf("2026-01-01-s%03d.md", i))
		if err := os.WriteFile(name, []byte("aged digest"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := store.PruneDigests(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("stale snapshot dir survived: %v", err)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Fatalf("today's snapshot pruned: %v", err)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("legacy migrator backup pruned: %v", err)
	}
	kept, err := os.ReadDir(sessions)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != memory.ArchiveDigestKeepForTest {
		t.Fatalf("archived digests = %d, want %d", len(kept), memory.ArchiveDigestKeepForTest)
	}
}
