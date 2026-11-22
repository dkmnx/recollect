package memory

// audit: behavior - changing the embedding provider rebuilds the derived vector table.
// audit: regression - a new model or vector width could query stale vectors and fail semantic search.
// audit: coverage gap - the index recorded the model per row but never rebuilt on identity change.
// audit: seam - the test opens the real on-disk index with two different embedders.

import (
	"context"
	"testing"
)

type fixedEmbedder struct {
	name string
	dims int
}

func (e fixedEmbedder) Name() string    { return e.name }
func (e fixedEmbedder) Dimensions() int { return e.dims }
func (e fixedEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		vector := make([]float32, e.dims)
		for j := range vector {
			vector[j] = 0.1
		}
		out[i] = vector
	}
	return out, nil
}

func TestEmbeddingIdentityChangeRebuildsVectors(t *testing.T) {
	cases := []struct {
		name   string
		second fixedEmbedder
	}{
		{"different width", fixedEmbedder{name: "model-b", dims: 3}},
		{"same width", fixedEmbedder{name: "model-c", dims: 2}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			projectDir := t.TempDir()
			corpus := []searchFact{{
				Fact:       Fact{Name: "setup", Description: "guide", Body: "steps"},
				RelPath:    "fact.md",
				SourceHash: "hash-1",
			}}

			first, err := openSearchIndex(projectDir, fixedEmbedder{name: "model-a", dims: 2})
			if err != nil {
				t.Fatalf("open first: %v", err)
			}
			if _, err := first.reconcile(corpus); err != nil {
				t.Fatalf("reconcile first: %v", err)
			}
			if err := first.db.Close(); err != nil {
				t.Fatalf("close first: %v", err)
			}

			second, err := openSearchIndex(projectDir, testCase.second)
			if err != nil {
				t.Fatalf("open second: %v", err)
			}
			defer second.db.Close()
			if _, err := second.reconcile(corpus); err != nil {
				t.Fatalf("reconcile second: %v", err)
			}
			var model string
			if err := second.db.QueryRow(`SELECT model FROM search_vec_meta WHERE rel = ?`, "fact.md").Scan(&model); err != nil {
				t.Fatalf("read vector model: %v", err)
			}
			if model != testCase.second.name {
				t.Fatalf("model = %q, want %q", model, testCase.second.name)
			}
			hits, err := second.semanticCandidates("setup", "")
			if err != nil {
				t.Fatalf("semantic candidates: %v", err)
			}
			if len(hits) != 1 {
				t.Fatalf("semantic hits = %v, want the fact", hits)
			}
		})
	}
}
