package memory_test

// audit: behavior - hybrid recall@5 finds held-out paraphrases without adding false positives on negative fixtures.
// audit: regression - a semantic backend could outrank exact identifiers or resurrect deleted and out-of-scope facts.
// audit: coverage gap - the rank corpus has no labeled paraphrases or negative fixtures.
// audit: seam - the test drives Store.SearchFacts with lexical and hybrid backends through the public API.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dkmnx/recollect/internal/memory"
)

func semanticStore(t *testing.T, backend string, embedder memory.EmbeddingProvider) *memory.Store {
	t.Helper()
	project := t.TempDir()
	store := memory.NewStore(memory.Options{
		ProjectDir: project, UserDir: t.TempDir(),
		Meta:          memory.ProjectMeta{Root: project, Name: "proj", Slug: "proj"},
		Now:           func() time.Time { return deterministicNow },
		SearchBackend: backend, Embedder: embedder,
	})
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func hasHit(matches []memory.FactMatch, name string) bool {
	for _, match := range matches {
		if match.Name == name {
			return true
		}
	}
	return false
}

func TestHybridRecallOnLabeledParaphrases(t *testing.T) {
	embedder := memory.NewDeterministicEmbedder(256)
	lexical := semanticStore(t, "", nil)
	hybrid := semanticStore(t, memory.SearchBackendHybrid, embedder)
	facts := []memory.Fact{
		{Name: "setup-doc", Description: "setup guide", Body: "installation steps"},
		{Name: "rollout-doc", Description: "rollout plan", Body: "release steps"},
		{Name: "terse-doc", Description: "terse notes", Body: "short answers"},
		{Name: "workflow-doc", Description: "workflow steps", Body: "task order"},
		{Name: "noise-doc", Description: "calendar dates", Body: "meeting schedule"},
	}
	for _, store := range []*memory.Store{lexical, hybrid} {
		for _, fact := range facts {
			if _, err := store.AppendFact(fact); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := hybrid.RefreshSearch(); err != nil {
		t.Fatal(err)
	}
	paraphrases := map[string]string{
		"configuration": "setup-doc",
		"deployment":    "rollout-doc",
		"brief":         "terse-doc",
		"pipeline":      "workflow-doc",
	}
	for query, want := range paraphrases {
		lexicalHits, err := lexical.SearchFacts(query, "", 5)
		if err != nil {
			t.Fatal(err)
		}
		hybridHits, err := hybrid.SearchFacts(query, "", 5)
		if err != nil {
			t.Fatal(err)
		}
		if hasHit(lexicalHits, want) {
			t.Fatalf("paraphrase %q already found lexically", query)
		}
		if !hasHit(hybridHits, want) {
			t.Fatalf("hybrid missed %q for %q: %+v", want, query, hybridHits)
		}
	}

	exact, err := hybrid.SearchFacts("setup", "", 5)
	if err != nil || !hasHit(exact, "setup-doc") || exact[0].Kind != "" {
		t.Fatalf("exact identifier changed: %+v %v", exact, err)
	}
	unrelated, err := hybrid.SearchFacts("calendar", "", 5)
	if err != nil || !hasHit(unrelated, "noise-doc") {
		t.Fatalf("unrelated lexical query lost: %+v %v", unrelated, err)
	}
	for _, match := range unrelated {
		if match.Kind == "semantic" {
			t.Fatalf("negative query gained a semantic hit: %+v", unrelated)
		}
	}

	hybridProject := hybrid.ProjectDir()
	if err := os.Remove(filepath.Join(hybridProject, "noise-doc.md")); err != nil {
		t.Fatal(err)
	}
	if err := hybrid.RefreshSearch(); err != nil {
		t.Fatal(err)
	}
	deleted, err := hybrid.SearchFacts("calendar", "", 5)
	if err != nil || hasHit(deleted, "noise-doc") {
		t.Fatalf("deleted fact still searchable: %+v %v", deleted, err)
	}
}

func TestHybridBackfillsUnchangedFacts(t *testing.T) {
	project, user := t.TempDir(), t.TempDir()
	lexicalStore := memory.NewStore(memory.Options{
		ProjectDir: project, UserDir: user,
		Meta:          memory.ProjectMeta{Root: project, Name: "proj", Slug: "proj"},
		Now:           func() time.Time { return deterministicNow },
		SearchBackend: memory.SearchBackendSQLite,
	})
	if _, err := lexicalStore.AppendFact(memory.Fact{
		Name: "setup-doc", Description: "setup guide", Body: "installation steps",
	}); err != nil {
		t.Fatal(err)
	}
	if err := lexicalStore.RefreshSearch(); err != nil {
		t.Fatal(err)
	}

	hybridStore := memory.NewStore(memory.Options{
		ProjectDir: project, UserDir: user,
		Meta:          memory.ProjectMeta{Root: project, Name: "proj", Slug: "proj"},
		Now:           func() time.Time { return deterministicNow },
		SearchBackend: memory.SearchBackendHybrid,
		Embedder:      memory.NewDeterministicEmbedder(256),
	})
	if err := hybridStore.RefreshSearch(); err != nil {
		t.Fatal(err)
	}
	matches, err := hybridStore.SearchFacts("configuration", "", 5)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, match := range matches {
		if match.Name == "setup-doc" && match.Kind == "semantic" {
			found = true
		}
	}
	if !found {
		t.Fatalf("unchanged fact was not backfilled: %+v", matches)
	}
}

func TestHybridSceneExclusion(t *testing.T) {
	hybrid := semanticStore(t, memory.SearchBackendHybrid, memory.NewDeterministicEmbedder(256))
	if _, err := hybrid.AppendFact(memory.Fact{
		Name: "scoped-doc", Description: "setup guide", Body: "notes", Scene: "chat",
	}); err != nil {
		t.Fatal(err)
	}
	if err := hybrid.RefreshSearch(); err != nil {
		t.Fatal(err)
	}
	matches, err := hybrid.SearchFacts("configuration", memory.FactSceneWorking, 5)
	if err != nil {
		t.Fatal(err)
	}
	if hasHit(matches, "scoped-doc") {
		t.Fatalf("out-of-scene semantic hit: %+v", matches)
	}
}
