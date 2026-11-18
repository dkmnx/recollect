// facts_rank_test.go — the ranking contract: field weights order a
// cross-field query, an exact phrase beats token overlap, freshness breaks
// ties, backlinks lift their targets, and a query of nothing but stopwords
// falls back to input order.

package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

import (
	"recollect/internal/memory"
	"reflect"
	"strings"
	"testing"
	"time"
)

// rankNow is the ranking tests' clock.
var rankNow = deterministicNow

func TestRankFactsOrdersFieldsByWeight(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	facts := []memory.Fact{
		{Name: "misc", Description: "general notes", Body: "we deploy on fridays", ModTime: base},
		{Name: "notes", Description: "deploy pipeline", Body: "how the pipeline is wired", ModTime: base},
		{Name: "deploy-flow", Description: "how releases ship", Body: "unrelated words here", ModTime: base},
	}
	ranked := memory.RankFactsForTest(facts, "deploy", rankNow)
	if len(ranked) != 3 {
		t.Fatalf("ranked = %d facts", len(ranked))
	}
	want := []string{"deploy-flow", "notes", "misc"}
	got := make([]string, 3)
	for i, sf := range ranked {
		got[i] = sf.Fact.Name
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if ranked[0].Field != "name" || ranked[1].Field != "description" || ranked[2].Field != "body" {
		t.Fatalf("fields = %q %q %q", ranked[0].Field, ranked[1].Field, ranked[2].Field)
	}
	if ranked[0].Score <= ranked[1].Score || ranked[1].Score <= ranked[2].Score {
		t.Fatalf("scores not descending: %.3f %.3f %.3f", ranked[0].Score, ranked[1].Score, ranked[2].Score)
	}
}

func TestRankFactsPrefersExactPhrase(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	facts := []memory.Fact{
		{Name: "docs", Description: "changelog pointers", Body: "release: make targets documented", ModTime: base},
		{Name: "runbook", Description: "operations", Body: "use make release then smoke test", ModTime: base},
	}
	ranked := memory.RankFactsForTest(facts, "make release", rankNow)
	if ranked[0].Fact.Name != "runbook" {
		t.Fatalf("phrase hit lost to token overlap: %s", ranked[0].Fact.Name)
	}
}

func TestRankFactsPrefersFreshAtEqualScore(t *testing.T) {
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fresh := old.Add(45 * 24 * time.Hour)
	facts := []memory.Fact{
		{Name: "alpha", Description: "stale twin", Body: "cache config notes", ModTime: old},
		{Name: "beta", Description: "fresh twin", Body: "cache config notes", ModTime: fresh},
	}
	ranked := memory.RankFactsForTest(facts, "cache", rankNow)
	if ranked[0].Fact.Name != "beta" {
		t.Fatalf("stale fact outranked fresh twin: %s", ranked[0].Fact.Name)
	}
}

func TestRankFactsBacklinkBoost(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	facts := []memory.Fact{
		{Name: "spoke", Description: "plain fact", Body: "cache config notes", ModTime: base},
		{Name: "hub", Description: "referenced fact", Body: "cache config notes", ModTime: base},
		{Name: "inbound", Description: "points at hub", Body: "see the cache setup", Links: []string{"hub"}, ModTime: base},
	}
	ranked := memory.RankFactsForTest(facts, "cache", rankNow)
	if ranked[0].Fact.Name != "hub" {
		t.Fatalf("backlinked fact not boosted: %s", ranked[0].Fact.Name)
	}
}

func TestRankFactsStopwordQueryKeepsInputOrder(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	facts := []memory.Fact{
		{Name: "zeta", Description: "the first", Body: "of the", ModTime: base},
		{Name: "alpha", Description: "the second", Body: "and the", ModTime: base},
	}
	ranked := memory.RankFactsForTest(facts, "the and of", rankNow)
	if len(ranked) != 2 || ranked[0].Fact.Name != "zeta" || ranked[1].Fact.Name != "alpha" {
		t.Fatalf("stopword query reordered input: %q %q", ranked[0].Fact.Name, ranked[1].Fact.Name)
	}
	for _, sf := range ranked {
		if sf.Score != 0 {
			t.Fatalf("stopword query scored %.3f", sf.Score)
		}
	}
}

func TestRankFactsEmptyQueryReturnsNil(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if got := memory.RankFactsForTest([]memory.Fact{{Name: "a", ModTime: base}}, "   ", rankNow); got != nil {
		t.Fatalf("blank query = %+v", got)
	}
	if got := memory.RankFactsForTest(nil, "deploy", rankNow); got != nil {
		t.Fatalf("empty corpus = %+v", got)
	}
}

func TestRankFactsDeterministic(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	facts := []memory.Fact{
		{Name: "one", Description: "deploy notes", Body: "deploy scripts", ModTime: base},
		{Name: "two", Description: "deploy pipeline", Body: "deploy checks", ModTime: base},
		{Name: "three", Description: "other", Body: "deploy again", ModTime: base},
	}
	first := memory.RankFactsForTest(facts, "deploy", rankNow)
	second := memory.RankFactsForTest(facts, "deploy", rankNow)
	for i := range first {
		if first[i].Fact.Name != second[i].Fact.Name || first[i].Score != second[i].Score {
			t.Fatalf("rank %d differs: %s/%.3f vs %s/%.3f", i,
				first[i].Fact.Name, first[i].Score, second[i].Fact.Name, second[i].Score)
		}
	}
}

func TestRecallSurvivesRanking(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	facts := []memory.Fact{
		{Name: "redis", Description: "cache layer", Body: "maxmemory allkeys-lru", ModTime: base},
		{Name: "deploy-script", Description: "rollout", Body: "scripts/deploy.sh", Path: "topics/ops", ModTime: base},
		{Name: "loopguard", Description: "guards loops", Body: "repeating thinking markers", ModTime: base},
		{Name: "compaction", Description: "session compaction", Body: "preserve rules for long sessions", ModTime: base},
	}
	queries := []string{"redis", "maxmemory", "rollout", "deploy", "loopguard", "thinking", "compaction", "preserve", "sessions", "cache"}
	for _, query := range queries {
		want := map[string]bool{}
		lower := strings.ToLower(query)
		for _, fact := range facts {
			switch {
			case strings.Contains(strings.ToLower(fact.Name), lower):
				want[fact.Name] = true
			case strings.Contains(strings.ToLower(fact.Description), lower):
				want[fact.Name] = true
			case strings.Contains(strings.ToLower(fact.Body), lower):
				want[fact.Name] = true
			}
		}
		ranked := memory.RankFactsForTest(facts, query, rankNow)
		got := map[string]bool{}
		for _, sf := range ranked {
			got[sf.Fact.Name] = true
		}
		for name := range want {
			if !got[name] {
				t.Fatalf("query %q lost substring match %q", query, name)
			}
		}
	}
}
