// facts_rank.go — relevance ranking for fact search. BM25 over the name,
// description, and body with field weights, an exact-phrase bonus that keeps
// verbatim recall on top, a freshness nudge, and a backlink boost. The terms
// come from terms.go, which normalizes and tokenizes the corpus and the
// query alike. Pure functions over a listed fact set; the store supplies the
// clock.

package memory

import (
	"math"
	"sort"
	"strings"
	"time"
)

// Ranking constants. One copy here so callers cannot drift: a name is the
// strongest signal and the body the weakest, an exact phrase beats token
// overlap, fresh facts edge out stale ones at otherwise equal scores, and a
// fact that other facts point at is lifted, never drowned.
const (
	bm25K1 = 1.4
	bm25B  = 0.75

	fieldWeightName = 3.0
	fieldWeightDesc = 2.0
	fieldWeightBody = 1.0

	exactPhraseBonus = 0.5

	freshnessWeight   = 0.3
	freshnessTauHours = 720.0 // roughly 30 days

	backlinkWeight = 0.15
)

// stopWords are the English function words no fact is about. A query built
// from them alone carries no signal, so ranking falls back to input order.
var stopWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true,
	"be": true, "been": true, "but": true, "by": true, "for": true,
	"from": true, "if": true, "in": true, "is": true, "it": true, "its": true,
	"not": true, "of": true, "on": true, "or": true, "so": true, "such": true,
	"than": true, "that": true, "the": true, "then": true, "these": true,
	"this": true, "those": true, "to": true, "was": true, "were": true,
	"with": true,
}

// scoredFact is one ranked hit: the fact, its combined score, and the field
// a reader should credit (a raw substring field when one exists, otherwise
// "ranked" for a token-only match).
type scoredFact struct {
	fact  Fact
	score float64
	field string
	index int
}

// rankFacts scores and orders facts for query. A blank query returns nil;
// a token-free query (stopwords only) keeps input order with zero scores;
// an empty corpus returns nil. A fact with no token match and no phrase hit
// is not a result: freshness and backlinks never rescue an unrelated hit.
// Ties break by name, so the order is deterministic.
func rankFacts(facts []Fact, query string, now time.Time) []scoredFact {
	normalized := strings.TrimSpace(normalizeText(query))
	if normalized == "" || len(facts) == 0 {
		return nil
	}
	phraseHit := func(s string) bool {
		return strings.Contains(normalizeText(s), normalized)
	}
	tokens := queryTokens(query)
	if len(tokens) == 0 {
		out := make([]scoredFact, len(facts))
		for i, fact := range facts {
			out[i] = scoredFact{fact: fact, field: matchField(fact, phraseHit), index: i}
		}
		return out
	}
	scorer := newFactScorer(facts)
	out := make([]scoredFact, 0, len(facts))
	for i, fact := range facts {
		relevance, field := scorer.score(i, fact, tokens, phraseHit)
		if relevance <= 0 {
			continue
		}
		score := relevance
		score += freshnessBonus(fact, now)
		score += backlinkWeight * math.Log(1+float64(backlinkCount(facts, fact.Name)))
		out = append(out, scoredFact{fact: fact, score: score, field: field, index: i})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].fact.Name < out[j].fact.Name
	})
	return out
}

// A narrowed candidate set must keep the same order and scores as rankFacts.
func rankCandidates(facts []Fact, rels []string, candidates map[string]bool, query string, now time.Time) []scoredFact {
	normalized := strings.TrimSpace(normalizeText(query))
	if normalized == "" || len(facts) == 0 {
		return nil
	}
	phraseHit := func(s string) bool {
		return strings.Contains(normalizeText(s), normalized)
	}
	tokens := queryTokens(query)
	if len(tokens) == 0 {
		out := make([]scoredFact, 0, len(facts))
		for i, fact := range facts {
			if !candidates[rels[i]] {
				continue
			}
			out = append(out, scoredFact{fact: fact, field: matchField(fact, phraseHit), index: i})
		}
		return out
	}
	scorer := newFactScorer(facts)
	out := make([]scoredFact, 0, len(candidates))
	for i, fact := range facts {
		if !candidates[rels[i]] {
			continue
		}
		relevance, field := scorer.score(i, fact, tokens, phraseHit)
		if relevance <= 0 {
			continue
		}
		score := relevance
		score += freshnessBonus(fact, now)
		score += backlinkWeight * math.Log(1+float64(backlinkCount(facts, fact.Name)))
		out = append(out, scoredFact{fact: fact, score: score, field: field, index: i})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].fact.Name < out[j].fact.Name
	})
	return out
}

// matchField names the first field the raw query appears in, mirroring the
// pre-ranking search's channel labels; a token-only match reports "ranked".
func matchField(fact Fact, phraseHit func(string) bool) string {
	switch {
	case phraseHit(fact.Name):
		return "name"
	case phraseHit(fact.Description):
		return "description"
	case phraseHit(fact.Body):
		return "body"
	default:
		return "ranked"
	}
}

// queryTokens tokenizes a query and drops the stopwords.
func queryTokens(query string) []string {
	var tokens []string
	for _, token := range tokenize(query) {
		if !stopWords[token] {
			tokens = append(tokens, token)
		}
	}
	return tokens
}

// fieldStats holds one field's corpus statistics: the token lists per fact
// (index-aligned with the input slice) and the per-term document frequency.
type fieldStats struct {
	docs     [][]string
	total    int
	docFreq  map[string]int
	avgBytes float64
}

func newFieldStats(texts []string) fieldStats {
	stats := fieldStats{docFreq: map[string]int{}}
	for _, text := range texts {
		tokens := tokenize(text)
		stats.docs = append(stats.docs, tokens)
		stats.total += len(tokens)
		seen := map[string]bool{}
		for _, token := range tokens {
			if !seen[token] {
				seen[token] = true
				stats.docFreq[token]++
			}
		}
	}
	if len(texts) > 0 {
		stats.avgBytes = float64(stats.total) / float64(len(texts))
	}
	return stats
}

// bm25 scores one document of the field against the query tokens.
func (f *fieldStats) bm25(doc int, tokens []string) float64 {
	length := float64(len(f.docs[doc]))
	if length == 0 || f.avgBytes == 0 {
		return 0
	}
	var score float64
	for _, token := range tokens {
		df := f.docFreq[token]
		if df == 0 {
			continue
		}
		tf := 0
		for _, candidate := range f.docs[doc] {
			if candidate == token {
				tf++
			}
		}
		idf := math.Log(1 + (float64(len(f.docs))-float64(df)+0.5)/(float64(df)+0.5))
		tfNorm := float64(tf) * (bm25K1 + 1) / (float64(tf) + bm25K1*(1-bm25B+bm25B*length/f.avgBytes))
		score += idf * tfNorm
	}
	return score
}

// factScorer holds the three fields' corpus statistics for one ranking pass.
type factScorer struct {
	name fieldStats
	desc fieldStats
	body fieldStats
}

func newFactScorer(facts []Fact) *factScorer {
	names, descs, bodies := make([]string, len(facts)), make([]string, len(facts)), make([]string, len(facts))
	for i, fact := range facts {
		names[i] = fact.Name
		descs[i] = fact.Description
		bodies[i] = fact.Body
	}
	return &factScorer{name: newFieldStats(names), desc: newFieldStats(descs), body: newFieldStats(bodies)}
}

// score combines the weighted field scores and the exact-phrase bonus for
// the fact at doc (the statistics are index-aligned with the listed facts).
func (s *factScorer) score(doc int, fact Fact, tokens []string, phraseHit func(string) bool) (float64, string) {
	total := fieldWeightName*s.name.bm25(doc, tokens) +
		fieldWeightDesc*s.desc.bm25(doc, tokens) +
		fieldWeightBody*s.body.bm25(doc, tokens)
	if phraseHit(fact.Name) || phraseHit(fact.Description) || phraseHit(fact.Body) {
		total += exactPhraseBonus
	}
	return total, matchField(fact, phraseHit)
}

// freshnessBonus decays toward zero over roughly a month, so a fresh fact
// edges out a stale twin at otherwise equal scores. Future mtimes clamp to
// the full bonus.
func freshnessBonus(fact Fact, now time.Time) float64 {
	if fact.ModTime.IsZero() {
		return 0
	}
	age := now.Sub(fact.ModTime)
	if age < 0 {
		age = 0
	}
	return freshnessWeight * math.Exp(-age.Hours()/freshnessTauHours)
}

// backlinkCount counts facts whose links point at name.
func backlinkCount(facts []Fact, name string) int {
	count := 0
	for _, fact := range facts {
		for _, link := range fact.Links {
			if link == name {
				count++
				break
			}
		}
	}
	return count
}
