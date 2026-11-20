package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

import (
	"github.com/dkmnx/recollect/internal/memory"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// legacyProject writes a pre-format store: structured MEMORY.md entries,
// corrections.md, one multi-entry topic file, and a summary index.
func legacyProject(t *testing.T, projectDir string) {
	t.Helper()
	write := func(rel, content string) {
		path := filepath.Join(projectDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("MEMORY.md", "## redis-tuning — Redis needs maxmemory set\nType: fact\n\nSet maxmemory-policy allkeys-lru.\n\n## old-note — superseded note\nType: note\n\nstale content\n")
	write("corrections.md", "## fix-redis — Redis port is 6380\nCorrects: redis-tuning\n\nThe port changed to 6380.\n\n## orphan-fix — target vanished\nCorrects: gone-away\n\nNothing points here.\n")
	write("topics/ops.md", "# operations\n\n## deploy — Rollout steps\nType: fact\n\nRun scripts/deploy.sh.\n")
	write(".summary.md", "summary text, retired\n")
}

func TestMigrateLegacyStore(t *testing.T) {
	store, _, projectDir := newTestStore(t, memory.Limits{})
	legacyProject(t, projectDir)

	result, err := store.MigrateLegacyIfPresent()
	if err != nil {
		t.Fatal(err)
	}
	if result.Facts != 4 { // two index entries + one stale correction + one topic entry
		t.Fatalf("facts = %+v", result)
	}
	if result.CorrectionsFolded != 1 || result.StaleCorrections != 1 || result.Topics != 1 {
		t.Fatalf("result = %+v", result)
	}

	fact, err := store.ReadFact("redis-tuning")
	if err != nil {
		t.Fatal(err)
	}
	if fact.Description != "Redis needs maxmemory set" || fact.Type != memory.FactTypeProject {
		t.Fatalf("fact = %+v", fact)
	}
	if !strings.Contains(fact.Body, "Set maxmemory-policy allkeys-lru.") {
		t.Fatalf("body not preserved: %q", fact.Body)
	}
	if !strings.Contains(fact.Body, "Correction: Redis port is 6380") || !strings.Contains(fact.Body, "The port changed to 6380.") {
		t.Fatalf("correction not folded: %q", fact.Body)
	}

	stale, err := store.ReadFact("correction-orphan-fix")
	if err != nil || !strings.Contains(stale.Body, "Nothing points here.") {
		t.Fatalf("stale correction = %+v (%v)", stale, err)
	}

	deploy, err := store.ReadFact("deploy")
	if err != nil || deploy.Path != "topics/ops" || deploy.Description != "Rollout steps" {
		t.Fatalf("topic fact = %+v (%v)", deploy, err)
	}

	// The legacy side files are gone, and MEMORY.md no longer carries
	// structured entries.
	for _, rel := range []string{"corrections.md", ".summary.md", "topics/ops.md"} {
		if _, err := os.Stat(filepath.Join(projectDir, rel)); !os.IsNotExist(err) {
			t.Fatalf("legacy file %s still present", rel)
		}
	}
	index, err := store.ReadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(index, "## ") {
		t.Fatalf("index still carries legacy entries: %q", index)
	}
	for _, want := range []string{"[redis-tuning](redis-tuning.md)", "[deploy](topics/ops/deploy.md)", "[correction-orphan-fix]"} {
		if !strings.Contains(index, want) {
			t.Fatalf("index missing %q: %q", want, index)
		}
	}

	// Copy-first: the archive keeps the original bytes.
	matches, err := filepath.Glob(filepath.Join(projectDir, "archive", "legacy-*", "MEMORY.md"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("legacy archive = %v (%v)", matches, err)
	}
	archived, err := os.ReadFile(matches[0])
	if err != nil || !strings.Contains(string(archived), "## redis-tuning — Redis needs maxmemory set") {
		t.Fatalf("archived index = %q (%v)", archived, err)
	}

	// Idempotent: a migrated store is not re-migrated.
	again, err := store.MigrateLegacyIfPresent()
	if err != nil || again != (memory.MigrateResult{}) {
		t.Fatalf("re-migration = %+v (%v)", again, err)
	}
}

func TestMigrateSkipsCurrentFormat(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	if _, err := store.AppendFact(memory.Fact{Name: "modern", Description: "already per-fact"}); err != nil {
		t.Fatal(err)
	}
	result, err := store.MigrateLegacyIfPresent()
	if err != nil || result != (memory.MigrateResult{}) {
		t.Fatalf("no-op migration = %+v (%v)", result, err)
	}
}

func TestNewStoreMigratesAutomatically(t *testing.T) {
	_, _, projectDir := newTestStore(t, memory.Limits{})
	legacyProject(t, projectDir)
	// A fresh store over the same directory migrates on construction.
	store := memory.NewStore(memory.Options{ProjectDir: projectDir, Meta: memory.ProjectMeta{Root: projectDir, Name: "proj"}})
	if err := store.MigrateError(); err != nil {
		t.Fatalf("auto migration failed: %v", err)
	}
	if _, err := store.ReadFact("redis-tuning"); err != nil {
		t.Fatalf("auto migration did not convert: %v", err)
	}
}

// audit: behavior - a legacy index entry named corrections is written under a non-reserved name.
// audit: regression - corrections.md was written then deleted by the legacy cleanup.
// audit: coverage gap - no migration test used a reserved store name as an entry key.
// audit: seam - MigrateLegacyIfPresent over a MEMORY.md entry named corrections.
func TestMigrateLegacyReservedEntryName(t *testing.T) {
	store, _, projectDir := newTestStore(t, memory.Limits{})
	if err := os.WriteFile(filepath.Join(projectDir, "MEMORY.md"),
		[]byte("## corrections — notes about fixes\nType: fact\n\nbody text\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := store.MigrateLegacyIfPresent()
	if err != nil {
		t.Fatal(err)
	}
	if result.Facts != 1 {
		t.Fatalf("facts = %+v", result)
	}
	if _, err := store.ReadFact("corrections-fact"); err != nil {
		t.Fatalf("renamed fact missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectDir, "corrections.md")); !os.IsNotExist(err) {
		t.Fatalf("legacy corrections file survived: %v", err)
	}
}

// audit: behavior - a failed migration keeps the legacy inputs for a successful retry.
// audit: regression - corrections.md was removed before the fact write, so the retry folded nothing.
// audit: coverage gap - no migration test retried after a write failure.
// audit: seam - MigrateLegacyIfPresent with a blocked target then a cleared target.
func TestMigrateRetryKeepsLegacyInputs(t *testing.T) {
	store, _, projectDir := newTestStore(t, memory.Limits{})
	legacyProject(t, projectDir)
	blocked := filepath.Join(projectDir, "redis-tuning.md")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MigrateLegacyIfPresent(); err == nil {
		t.Fatal("blocked migration did not fail")
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	result, err := store.MigrateLegacyIfPresent()
	if err != nil {
		t.Fatal(err)
	}
	if result.CorrectionsFolded != 1 {
		t.Fatalf("retry folded %d corrections, want 1", result.CorrectionsFolded)
	}
	if _, err := store.ReadFact("redis-tuning"); err != nil {
		t.Fatalf("migrated fact missing: %v", err)
	}
}

// audit: behavior - a mid-batch migration failure keeps the legacy index for a retry that reuses converted names.
// audit: regression - per-fact index regeneration erased the legacy headings, so the retry dropped later entries.
// audit: coverage gap - the retry test blocked the first write, before any index rewrite.
// audit: seam - MigrateLegacyIfPresent with the second fact target blocked, then cleared.
func TestMigrateMidBatchRetryReusesEntries(t *testing.T) {
	store, _, projectDir := newTestStore(t, memory.Limits{})
	legacyProject(t, projectDir)
	blocked := filepath.Join(projectDir, "old-note.md")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MigrateLegacyIfPresent(); err == nil {
		t.Fatal("mid-batch migration did not fail")
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MigrateLegacyIfPresent(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadFact("old-note"); err != nil {
		t.Fatalf("later legacy entry lost: %v", err)
	}
	alpha, err := store.ReadFact("redis-tuning")
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(alpha.Body, "Correction:"); count != 1 {
		t.Fatalf("correction folded %d times: %q", count, alpha.Body)
	}
	facts, err := store.ListFacts()
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range facts {
		if strings.HasSuffix(fact.Name, "-2") {
			t.Fatalf("retry created a duplicate fact: %+v", fact)
		}
	}
	archives, err := filepath.Glob(filepath.Join(projectDir, "archive", "legacy-*", "MEMORY.md"))
	if err != nil || len(archives) != 2 {
		t.Fatalf("same-second retry did not keep both archives: %v (%v)", archives, err)
	}
}
