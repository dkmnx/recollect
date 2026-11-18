// rank_corpus_test.go — the fact-search corpus: JSON cases under
// testdata/rank-cases that pin which facts a query finds and in which
// order. The cases run against the real search boundary, so recall for
// Han text, full-width text, and English stays pinned in one place.
//
// Audit record.
// Protected behavior: a query finds the facts it shares a term with, and
// the hits come back in a defined order.
// Credible regression: a tokenizer that returns one term per text run finds
// nothing for a Chinese paraphrase, and a normalizer that drops the width
// fold finds nothing for a full-width query.
// Coverage gap: the ranking unit tests cover the field weights, the phrase
// bonus, and the stopword path, but no non-Latin script and no difference
// in width.
// Seam check: the cases drive Store.SearchFacts through the exported API,
// so no test-only export was added.

package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"recollect/internal/memory"
	"testing"
)

// rankCase is one corpus case: the query, the scene filter, the facts to
// store, and the expected fact names in the order the search must return
// them.
type rankCase struct {
	Query  string     `json:"query"`
	Scene  string     `json:"scene"`
	Facts  []rankFact `json:"facts"`
	Expect []string   `json:"expect"`
	Note   string     `json:"note"`
}

// rankFact is one fact a corpus case stores.
type rankFact struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
	Scene       string `json:"scene"`
}

// TestRankCorpus runs every corpus case. Each case stores its facts in a
// fresh store and searches it, so the expectation is the fact list a user
// sees, not a score.
func TestRankCorpus(t *testing.T) {
	dir := filepath.Join("testdata", "rank-cases")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read corpus dir: %v", err)
	}
	cases := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		cases++
		t.Run(entry.Name(), func(t *testing.T) {
			runRankCase(t, filepath.Join(dir, entry.Name()))
		})
	}
	if cases == 0 {
		t.Fatal("corpus dir holds no case files")
	}
}

// runRankCase stores one case's facts and checks the search result names
// against the expectation, element by element.
func runRankCase(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read case: %v", err)
	}
	var tc rankCase
	if err := json.Unmarshal(raw, &tc); err != nil {
		t.Fatalf("decode case: %v", err)
	}
	store, _, _ := newTestStore(t, memory.Limits{})
	for _, fact := range tc.Facts {
		if _, err := store.AppendFact(memory.Fact{
			Name:        fact.Name,
			Description: fact.Description,
			Body:        fact.Body,
			Scene:       fact.Scene,
		}); err != nil {
			t.Fatalf("append fact %s: %v", fact.Name, err)
		}
	}
	matches, err := store.SearchFacts(tc.Query, tc.Scene, 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	got := make([]string, 0, len(matches))
	for _, match := range matches {
		got = append(got, match.Name)
	}
	if len(got) != len(tc.Expect) {
		t.Fatalf("hits = %v, want %v (case note: %s)", got, tc.Expect, tc.Note)
	}
	for i := range got {
		if got[i] != tc.Expect[i] {
			t.Fatalf("hits = %v, want %v (case note: %s)", got, tc.Expect, tc.Note)
		}
	}
}
