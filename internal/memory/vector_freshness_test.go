package memory

// audit: behavior - embedding refresh uses the vector's own stored source hash, not the lexical document hash.
// audit: regression - an embedding outage left the old vector while the lexical hash advanced, so stale paraphrases kept matching.
// audit: coverage gap - existing hybrid tests never fail the provider between two refreshes.
// audit: seam - Store.RefreshSearch and Store.semanticCandidates over a store with a scripted provider.
import (
	"context"
	"errors"
	"testing"
	"time"
)

type failingEmbedder struct {
	inner memoryLikeEmbedder
	fail  *bool
}

type memoryLikeEmbedder interface {
	Embed(context.Context, []string) ([][]float32, error)
	Dimensions() int
	Name() string
}

func (e failingEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if *e.fail {
		return nil, errors.New("embedding provider unavailable")
	}
	return e.inner.Embed(ctx, texts)
}
func (e failingEmbedder) Dimensions() int { return e.inner.Dimensions() }
func (e failingEmbedder) Name() string    { return e.inner.Name() }

func TestEmbeddingRefreshChecksVectorSourceHash(t *testing.T) {
	fail := false
	project := t.TempDir()
	store := NewStore(Options{
		ProjectDir: project, UserDir: t.TempDir(),
		Meta:          ProjectMeta{Root: project, Name: "proj", Slug: "proj"},
		Now:           func() time.Time { return time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC) },
		SearchBackend: SearchBackendHybrid,
		Embedder:      failingEmbedder{inner: NewDeterministicEmbedder(256), fail: &fail},
	})
	if _, err := store.AppendFact(Fact{Name: "weather", Description: "old", Body: "sunset"}); err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshSearch(); err != nil {
		t.Fatal(err)
	}
	fail = true
	if _, err := store.UpdateFact("weather", "new", "blizzard", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshSearch(); err != nil {
		t.Fatal(err)
	}
	fail = false
	if err := store.RefreshSearch(); err != nil {
		t.Fatal(err)
	}
	blizzard, err := store.semanticCandidates("blizzard", "")
	if err != nil {
		t.Fatal(err)
	}
	sunset, err := store.semanticCandidates("sunset", "")
	if err != nil {
		t.Fatal(err)
	}
	if dist, ok := blizzard["weather.md"]; !ok || dist > 0.6 {
		t.Fatalf("new paraphrase not matched after recovery: %v", blizzard)
	}
	if dist, ok := sunset["weather.md"]; ok && dist <= 0.6 {
		t.Fatalf("old paraphrase still matched after recovery: %v", sunset)
	}
}
