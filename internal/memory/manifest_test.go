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

func TestManifestListsFactsAndDigests(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	if _, err := store.AppendFact(memory.Fact{Name: "redis", Description: "cache", Body: "maxmemory"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendFact(memory.Fact{Name: "deploy", Description: "rollout", Body: "scripts", Path: "topics/ops"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteDigest(memory.Digest{SessionID: "s1", Topic: "session", Summary: "where we stopped", Date: deterministicNow, Updated: deterministicNow}); err != nil {
		t.Fatal(err)
	}
	entries, err := store.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	byKind := map[string][]string{}
	for _, entry := range entries {
		byKind[entry.Kind] = append(byKind[entry.Kind], entry.Name)
	}
	if len(byKind[memory.ManifestKindFact]) != 2 || len(byKind[memory.ManifestKindDigest]) != 1 || len(byKind[memory.ManifestKindIndex]) != 1 {
		t.Fatalf("manifest = %+v", entries)
	}
	for _, entry := range entries {
		if entry.Kind == memory.ManifestKindFact && entry.Name == "redis" && entry.Description != "cache" {
			t.Fatalf("fact description = %q", entry.Description)
		}
	}
	// Facts rank ahead of digests.
	if memory.ManifestKindRankForTest(memory.ManifestKindFact) >= memory.ManifestKindRankForTest(memory.ManifestKindDigest) {
		t.Fatal("facts must rank ahead of digests")
	}
}

func TestManifestSkipsEngineState(t *testing.T) {
	store, _, projectDir := newTestStore(t, memory.Limits{})
	if err := os.WriteFile(filepath.Join(projectDir, "capture.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "project.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := store.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name, ".json") {
			t.Fatalf("engine state leaked into the manifest: %+v", entry)
		}
	}
}

func TestManifestBlock(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	if _, err := store.AppendFact(memory.Fact{Name: "redis", Description: "cache", Body: "maxmemory"}); err != nil {
		t.Fatal(err)
	}
	block, err := store.ManifestBlock()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(block, "redis [fact]") || !strings.Contains(block, "maxmemory") {
		t.Fatalf("block = %q", block)
	}
}
