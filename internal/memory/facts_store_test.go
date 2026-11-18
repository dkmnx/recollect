package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

import (
	"github.com/dkmnx/recollect/internal/memory"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppendFactWritesFileAndIndex(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	written, err := store.AppendFact(memory.Fact{Description: "Redis needs maxmemory set", Body: "Set allkeys-lru."})
	if err != nil {
		t.Fatal(err)
	}
	if written.Name != "redis-needs-maxmemory-set" {
		t.Fatalf("derived name = %q", written.Name)
	}
	raw, err := os.ReadFile(filepath.Join(store.ProjectDir(), written.RelPath()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "node_type: "+memory.FactNodeType) {
		t.Fatalf("fact file missing node_type: %q", raw)
	}
	index, err := store.ReadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(index, "- [redis-needs-maxmemory-set](redis-needs-maxmemory-set.md) — Redis needs maxmemory set\n") {
		t.Fatalf("index = %q", index)
	}
}

func TestAppendFactNeverOverwrites(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	first, err := store.AppendFact(memory.Fact{Name: "dup", Description: "first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AppendFact(memory.Fact{Name: "dup", Description: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Name != "dup-2" || first.Name != "dup" {
		t.Fatalf("names = %q, %q", first.Name, second.Name)
	}
	got, err := store.ReadFact("dup")
	if err != nil || got.Description != "first" {
		t.Fatalf("first fact clobbered: %+v (%v)", got, err)
	}
}

func TestUpsertFactReplacesInPlace(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	created, err := store.UpsertFact(memory.Fact{Name: "deploy", Description: "old description", Body: "old"})
	if err != nil {
		t.Fatal(err)
	}
	moved, err := store.MoveFactToTopic("deploy", "ops")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.UpsertFact(memory.Fact{Name: "deploy", Description: "new description", Body: "new"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Path != moved.Path || updated.Path == created.Path {
		t.Fatalf("upsert should replace in place, path = %q", updated.Path)
	}
	got, err := store.ReadFact("deploy")
	if err != nil || got.Description != "new description" || got.Body != "new" {
		t.Fatalf("upsert = %+v (%v)", got, err)
	}
	facts, err := store.ListFacts()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, fact := range facts {
		if fact.Name == "deploy" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("upsert duplicated the fact: %d copies", count)
	}
}

func TestAppendFactStampsCapturedOn(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	written, err := store.AppendFact(memory.Fact{Description: "Redis needs maxmemory set", Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if written.CapturedOn != "2026-09-23" {
		t.Fatalf("append stamped %q, want the clock date", written.CapturedOn)
	}
	got, err := store.ReadFact(written.Name)
	if err != nil || got.CapturedOn != "2026-09-23" {
		t.Fatalf("round trip capturedOn = %q (%v)", got.CapturedOn, err)
	}
	explicit, err := store.AppendFact(memory.Fact{Description: "Observed earlier", CapturedOn: "2026-10-01"})
	if err != nil {
		t.Fatal(err)
	}
	if explicit.CapturedOn != "2026-10-01" {
		t.Fatalf("caller-set capturedOn overwritten: %q", explicit.CapturedOn)
	}
}

func TestUpsertFactPreservesCapturedOn(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	if _, err := store.UpsertFact(memory.Fact{Name: "deploy", Description: "old", Body: "old"}); err != nil {
		t.Fatal(err)
	}
	updated, err := store.UpsertFact(memory.Fact{Name: "deploy", Description: "new", Body: "new"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.CapturedOn != "2026-09-23" {
		t.Fatalf("replace changed capturedOn to %q", updated.CapturedOn)
	}
}

func TestAppendFactRejectsDuplicateBody(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	if _, err := store.AppendFact(memory.Fact{Description: "first", Body: "The build command is make release."}); err != nil {
		t.Fatal(err)
	}
	_, err := store.AppendFact(memory.Fact{Description: "a different summary", Body: "the BUILD command\nis make release!"})
	if err == nil || memory.CodeOf(err) != memory.CodeDuplicate {
		t.Fatalf("duplicate append = %v", err)
	}
	facts, err := store.ListFacts()
	if err != nil || len(facts) != 1 {
		t.Fatalf("duplicate append wrote a file: %d facts (%v)", len(facts), err)
	}
}

func TestAppendFactAllowsEmptyBodyReuse(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	if _, err := store.AppendFact(memory.Fact{Description: "first"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendFact(memory.Fact{Description: "second"}); err != nil {
		t.Fatalf("two bodyless facts should coexist: %v", err)
	}
}

func TestUpsertFactSkipsIdenticalContent(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	created, err := store.UpsertFact(memory.Fact{Name: "deploy", Description: "deploy flow", Body: "make release"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.ProjectDir(), created.RelPath())
	stale := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatal(err)
	}
	same, err := store.UpsertFact(memory.Fact{Name: "deploy", Description: "deploy flow", Body: "make release"})
	if err != nil || same.Name != "deploy" {
		t.Fatalf("identical upsert = %+v (%v)", same, err)
	}
	if info, err := os.Stat(path); err != nil || !info.ModTime().Equal(stale) {
		t.Fatalf("identical upsert rewrote the file: mtime %v (%v)", info.ModTime(), err)
	}
	other, err := store.UpsertFact(memory.Fact{Name: "deploy-copy", Description: "another name", Body: "MAKE   release!"})
	if err != nil || other.Name != "deploy" {
		t.Fatalf("same body under a new name should return the existing fact, got %+v (%v)", other, err)
	}
	facts, _ := store.ListFacts()
	if len(facts) != 1 {
		t.Fatalf("duplicate upsert wrote a file: %d facts", len(facts))
	}
}

func TestUpdateFactCorrectsInPlace(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	if _, err := store.AppendFact(memory.Fact{Name: "timeout", Description: "old summary", Body: "old body"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateFact("missing", "x", "y", nil); err == nil || memory.CodeOf(err) != memory.CodeFactNotFound {
		t.Fatalf("update missing fact = %v", err)
	}
	updated, err := store.UpdateFact("timeout", "new summary", "new body", nil)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Description != "new summary" || updated.Body != "new body" {
		t.Fatalf("updated = %+v", updated)
	}
	kept, err := store.UpdateFact("timeout", "", "", nil)
	if err != nil || kept.Description != "new summary" || kept.Body != "new body" {
		t.Fatalf("empty fields should keep values: %+v (%v)", kept, err)
	}
}

func TestEditFactReplaces(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	if _, err := store.AppendFact(memory.Fact{Name: "cmd", Description: "commands", Body: "one two one"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.EditFact("cmd", "missing", "x", false); err == nil || memory.CodeOf(err) != memory.CodeOldNotFound {
		t.Fatalf("edit missing = %v", err)
	}
	fact, n, err := store.EditFact("cmd", "one", "1", true)
	if err != nil || n != 2 || fact.Body != "1 two 1" {
		t.Fatalf("edit = %+v, %d (%v)", fact, n, err)
	}
}

func TestMoveFactToTopicDemotes(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	if _, err := store.AppendFact(memory.Fact{Name: "detail", Description: "long detail", Body: "detail body"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MoveFactToTopic("detail", "Bad Topic"); err == nil || memory.CodeOf(err) != memory.CodeInvalidTopic {
		t.Fatalf("invalid topic = %v", err)
	}
	moved, err := store.MoveFactToTopic("detail", memory.CaptureOverflowTopic)
	if err != nil {
		t.Fatal(err)
	}
	if moved.Path != "topics/"+memory.CaptureOverflowTopic {
		t.Fatalf("moved path = %q", moved.Path)
	}
	if _, err := os.Stat(filepath.Join(store.ProjectDir(), moved.RelPath())); err != nil {
		t.Fatalf("moved file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.ProjectDir(), "detail.md")); !os.IsNotExist(err) {
		t.Fatalf("old file still present: %v", err)
	}
	index, _ := store.ReadIndex()
	if !strings.Contains(index, "- [detail](topics/"+memory.CaptureOverflowTopic+"/detail.md) — ") {
		t.Fatalf("index not repointed: %q", index)
	}
	again, err := store.MoveFactToTopic("detail", memory.CaptureOverflowTopic)
	if err != nil || again.Name != "detail" {
		t.Fatalf("idempotent move = %+v (%v)", again, err)
	}
}

func TestDeleteFactAndTopic(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	if _, err := store.AppendFact(memory.Fact{Name: "gone", Description: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendFact(memory.Fact{Name: "kept", Description: "y", Path: "topics/notes"}); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.DeleteFact("gone")
	if err != nil || !deleted {
		t.Fatalf("delete = %t (%v)", deleted, err)
	}
	if deleted, err := store.DeleteFact("gone"); err != nil || deleted {
		t.Fatalf("re-delete = %t (%v)", deleted, err)
	}
	topics, err := store.TopicDirs()
	if err != nil || len(topics) != 1 || topics[0] != "notes" {
		t.Fatalf("topics = %v (%v)", topics, err)
	}
	if deleted, err := store.DeleteTopic("notes"); err != nil || !deleted {
		t.Fatalf("delete topic = %t (%v)", deleted, err)
	}
	facts, _ := store.ListFacts()
	if len(facts) != 0 {
		t.Fatalf("facts after topic delete = %+v", facts)
	}
}

func TestSearchFacts(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	if _, err := store.AppendFact(memory.Fact{Name: "redis", Description: "cache layer", Body: "maxmemory allkeys-lru"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendFact(memory.Fact{Name: "deploy-script", Description: "rollout", Body: "scripts/deploy.sh", Path: "topics/ops"}); err != nil {
		t.Fatal(err)
	}
	matches, err := store.SearchFacts("maxmemory", "", 0)
	if err != nil || len(matches) != 1 || matches[0].Field != "body" || matches[0].Channel != "project" {
		t.Fatalf("body search = %+v (%v)", matches, err)
	}
	matches, err = store.SearchFacts("rollout", "", 0)
	if err != nil || len(matches) != 1 || matches[0].Channel != "topic" {
		t.Fatalf("topic search = %+v (%v)", matches, err)
	}
	matches, err = store.SearchFacts("deploy", "", 0)
	if err != nil || len(matches) != 1 || matches[0].Field != "name" {
		t.Fatalf("name search = %+v (%v)", matches, err)
	}
}

func TestSearchFactsRanksAndScores(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	if _, err := store.AppendFact(memory.Fact{Name: "misc", Description: "general notes", Body: "we deploy on fridays"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendFact(memory.Fact{Name: "notes", Description: "deploy pipeline", Body: "how the pipeline is wired"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendFact(memory.Fact{Name: "deploy-flow", Description: "how releases ship", Body: "unrelated words here"}); err != nil {
		t.Fatal(err)
	}
	matches, err := store.SearchFacts("deploy", "", 0)
	if err != nil || len(matches) != 3 {
		t.Fatalf("search = %d matches (%v)", len(matches), err)
	}
	want := []string{"deploy-flow", "notes", "misc"}
	for i, match := range matches {
		if match.Name != want[i] {
			t.Fatalf("match %d = %s, want %s", i, match.Name, want[i])
		}
		if match.Score <= 0 {
			t.Fatalf("match %s carries no score: %.3f", match.Name, match.Score)
		}
	}
	if matches[0].Score <= matches[1].Score || matches[1].Score <= matches[2].Score {
		t.Fatalf("scores not descending: %.3f %.3f %.3f", matches[0].Score, matches[1].Score, matches[2].Score)
	}
}

func TestNoWriteIsRefusedForSize(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	huge := strings.Repeat("detail ", 100_000)
	if _, err := store.AppendFact(memory.Fact{Name: "huge", Description: "big fact", Body: huge}); err != nil {
		t.Fatalf("large fact refused: %v", err)
	}
	if _, err := store.AppendFact(memory.Fact{Name: "user-pref", Description: "likes dark themes", Type: memory.FactTypeUser}); err != nil {
		t.Fatalf("fact refused: %v", err)
	}
	index, _ := store.ReadIndex()
	if !strings.Contains(index, "user-pref") {
		t.Fatalf("index = %q", index)
	}
}

func TestFactsWithoutFrontmatterAreValid(t *testing.T) {
	store, _, projectDir := newTestStore(t, memory.Limits{})
	if err := os.WriteFile(filepath.Join(projectDir, "hand-made.md"), []byte("First line is the description.\n\nbody"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.RegenerateIndex(); err != nil {
		t.Fatal(err)
	}
	facts, err := store.ListFacts()
	if err != nil || len(facts) != 1 {
		t.Fatalf("facts = %+v (%v)", facts, err)
	}
	if facts[0].Name != "hand-made" || facts[0].Description != "First line is the description." {
		t.Fatalf("hand-made fact = %+v", facts[0])
	}
	// Index regeneration keeps it.
	index, _ := store.ReadIndex()
	if !strings.Contains(index, "- [hand-made](hand-made.md) — First line is the description.") {
		t.Fatalf("index = %q", index)
	}
}

func TestOriginSessionStamping(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	if _, err := store.UpsertFact(memory.Fact{Name: "stamped", Description: "d", OriginSessionID: "sess_9"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(store.ProjectDir(), "stamped.md"))
	if !strings.Contains(string(raw), "originSessionId: sess_9") {
		t.Fatalf("provenance missing: %q", raw)
	}
	plain, err := store.AppendFact(memory.Fact{Name: "plain", Description: "d"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(filepath.Join(store.ProjectDir(), plain.RelPath()))
	if strings.Contains(string(raw), "originSessionId") {
		t.Fatalf("interactive write should omit provenance: %q", raw)
	}
}

func TestUpsertFactRewritesSummaryOnSameBody(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	if _, err := store.AppendFact(memory.Fact{Name: "hook", Description: "weak hook", Body: "identical body"}); err != nil {
		t.Fatal(err)
	}
	updated, err := store.UpsertFact(memory.Fact{Name: "hook", Description: "sharper hook", Body: "identical body"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Description != "sharper hook" {
		t.Fatalf("summary-only update skipped: %+v", updated)
	}
	if got, _ := store.ReadFact("hook"); got.Description != "sharper hook" || got.CapturedOn != "2026-09-23" {
		t.Fatalf("stored = %+v", got)
	}
}
