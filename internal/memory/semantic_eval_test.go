package memory_test

// audit: behavior - hybrid semantic recall with a real embedding model beats lexical recall on labeled paraphrases.
// audit: regression - semantic hits must not add false positives, include deleted facts, or cross scene bounds.
// audit: coverage gap - the deterministic test provider cannot prove recall with a real model.
// audit: seam - the public Store API with a host-injected HTTP embedder over a real local model.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/dkmnx/recollect/internal/memory"
)

func evalStore(t *testing.T, backend string, provider memory.EmbeddingProvider) *memory.Store {
	t.Helper()
	project, user := t.TempDir(), t.TempDir()
	store := memory.NewStore(memory.Options{
		ProjectDir: project, UserDir: user,
		Meta:          memory.ProjectMeta{Root: project, Name: "eval", Slug: "eval"},
		Now:           func() time.Time { return deterministicNow },
		SearchBackend: backend, Embedder: provider,
	})
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func hitNames(matches []memory.FactMatch) map[string]bool {
	names := make(map[string]bool, len(matches))
	for _, match := range matches {
		names[match.Name] = true
	}
	return names
}

func TestRealEmbeddingRecallEvaluation(t *testing.T) {
	baseURL := os.Getenv("RECOLLECT_EVAL_EMBED_URL")
	if baseURL == "" {
		t.Skip("set RECOLLECT_EVAL_EMBED_URL to run the real embedding evaluation")
	}
	provider := memory.NewHTTPEmbedder(memory.HTTPEmbedConfig{
		BaseURL:    baseURL,
		Model:      "paraphrase-multilingual-MiniLM-L12-v2",
		Dimensions: 384,
	})
	if _, err := provider.Embed(context.Background(), []string{"probe"}); err != nil {
		t.Fatalf("embedder unavailable: %v", err)
	}
	lexical := evalStore(t, "", nil)
	hybrid := evalStore(t, memory.SearchBackendHybrid, provider)
	facts := []memory.Fact{
		{Name: "setup-guide", Description: "configure the deployment port", Body: "set the service port in the config file"},
		{Name: "deploy-runbook", Description: "rollout steps", Body: "ship the release to production"},
		{Name: "brief-answer", Description: "terse user preference", Body: "answer briefly"},
		{Name: "workflow-map", Description: "pipeline order", Body: "the task pipeline runs in a fixed order"},
		{Name: "calendar-note", Description: "meeting dates", Body: "the next meeting is on Friday"},
		{Name: "chat-scoped", Description: "setup for chat", Body: "chat-specific configuration", Scene: memory.FactSceneChat},
		{Name: "obsolete-doc", Description: "old configuration", Body: "this fact will be deleted"},
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
	if err := os.Remove(hybrid.ProjectDir() + "/obsolete-doc.md"); err != nil {
		t.Fatal(err)
	}
	if err := hybrid.RefreshSearch(); err != nil {
		t.Fatal(err)
	}

	paraphrases := map[string]string{
		"服务端口在哪里设置": "setup-guide",
		"发布流程是什么":   "deploy-runbook",
		"用户偏好简短回答":  "brief-answer",
		"任务执行顺序是什么": "workflow-map",
	}
	lexicalHits, hybridHits := 0, 0
	for query, want := range paraphrases {
		start := time.Now()
		lexicalMatches, err := lexical.SearchFacts(query, "", 5)
		lexicalMS := time.Since(start).Milliseconds()
		if err != nil {
			t.Fatal(err)
		}
		start = time.Now()
		hybridMatches, err := hybrid.SearchFacts(query, "", 5)
		totalMS := time.Since(start).Milliseconds()
		if err != nil {
			t.Fatal(err)
		}
		lexicalNames, hybridNames := hitNames(lexicalMatches), hitNames(hybridMatches)
		if lexicalNames[want] {
			lexicalHits++
		}
		if hybridNames[want] {
			hybridHits++
		}
		t.Logf("query %q: lexical=%v (%dms) hybrid=%v (total %dms)", query, lexicalNames, lexicalMS, hybridNames, totalMS)
	}
	if lexicalHits != 0 || hybridHits != len(paraphrases) {
		t.Fatalf("recall@5 lexical=%d hybrid=%d of %d", lexicalHits, hybridHits, len(paraphrases))
	}
	exact, err := hybrid.SearchFacts("setup-guide", "", 5)
	if err != nil || !hitNames(exact)["setup-guide"] {
		t.Fatalf("exact identifier lost: %+v %v", exact, err)
	}
	unrelated, err := hybrid.SearchFacts("quantum shipping lane", "", 5)
	if err != nil || len(unrelated) != 0 {
		t.Fatalf("false positive on negative query: %+v %v", unrelated, err)
	}
	deleted, err := hybrid.SearchFacts("obsolete configuration", "", 5)
	if err != nil || hitNames(deleted)["obsolete-doc"] {
		t.Fatalf("deleted fact resurfaced: %+v %v", deleted, err)
	}
	scoped, err := hybrid.SearchFacts("how should I set up the service port", memory.FactSceneWorking, 5)
	if err != nil || hitNames(scoped)["chat-scoped"] {
		t.Fatalf("scene leak: %+v %v", scoped, err)
	}
	start := time.Now()
	_, err = provider.Embed(context.Background(), []string{"latency probe"})
	t.Logf("provider latency: %dms err=%v", time.Since(start).Milliseconds(), err)
	if err != nil {
		t.Fatal(err)
	}
}
