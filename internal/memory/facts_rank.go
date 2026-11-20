// facts_rank.go — relevance ranking for fact search. BM25 over the name,
// description, and body with field weights, an exact-phrase bonus that keeps
// verbatim recall on top, a freshness nudge, and a backlink boost. The terms
// come from terms.go, which normalizes and tokenizes the corpus and the
// query alike. Pure functions over a listed fact set; the store supplies the
// clock.

package memory

import (
	"math"
	"slices"
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
	return rankCandidates(facts, nil, nil, nil, query, now)
}

// A narrowed candidate set keeps the same corpus statistics and scoring as rankFacts.
func rankCandidates(facts []Fact, prepared []factTerms, rels []string, candidates map[string]bool, query string, now time.Time) []scoredFact {
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
			if candidates != nil && !candidates[rels[i]] {
				continue
			}
			out = append(out, scoredFact{fact: fact, field: matchField(fact, phraseHit), index: i})
		}
		return out
	}
	if prepared == nil {
		prepared = make([]factTerms, len(facts))
		for i, fact := range facts {
			prepared[i] = prepareFactTerms(fact)
		}
	}
	scorer := newFactScorer(prepared, tokens)
	backlinks := countBacklinks(facts)
	capacity := len(facts)
	if candidates != nil {
		capacity = len(candidates)
	}
	out := make([]scoredFact, 0, capacity)
	for i, fact := range facts {
		if candidates != nil && !candidates[rels[i]] {
			continue
		}
		relevance, field := scorer.score(prepared[i], fact, tokens, phraseHit)
		if relevance <= 0 {
			continue
		}
		score := relevance
		score += freshnessBonus(fact, now)
		score += backlinkWeight * math.Log(1+float64(backlinks[fact.Name]))
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

const (
	nameField = iota
	descriptionField
	bodyField
	fieldCount
)

type fieldTerms struct {
	length int
	counts map[string]int
}

type factTerms [fieldCount]fieldTerms

func prepareFactTerms(fact Fact) factTerms {
	texts := [fieldCount]string{fact.Name, fact.Description, fact.Body}
	var prepared factTerms
	for field, text := range texts {
		tokens := tokenize(text)
		counts := make(map[string]int)
		for _, token := range tokens {
			counts[token]++
		}
		prepared[field] = fieldTerms{length: len(tokens), counts: counts}
	}
	return prepared
}

type fieldStats struct {
	docFreq map[string]int
	avgLen  float64
}

func (f fieldStats) bm25(doc fieldTerms, tokens []string, corpusSize int) float64 {
	if doc.length == 0 || f.avgLen == 0 {
		return 0
	}
	lengthNorm := bm25K1 * (1 - bm25B + bm25B*float64(doc.length)/f.avgLen)
	var score float64
	for _, token := range tokens {
		tf := float64(doc.counts[token])
		if tf == 0 {
			continue
		}
		df := float64(f.docFreq[token])
		idf := math.Log(1 + (float64(corpusSize)-df+0.5)/(df+0.5))
		tfNorm := tf * (bm25K1 + 1) / (tf + lengthNorm)
		score += idf * tfNorm
	}
	return score
}

type factScorer struct {
	fields [fieldCount]fieldStats
	size   int
}

func newFactScorer(docs []factTerms, tokens []string) factScorer {
	queryTerms := make(map[string]bool, len(tokens))
	for _, token := range tokens {
		queryTerms[token] = true
	}
	scorer := factScorer{size: len(docs)}
	for field := range scorer.fields {
		total := 0
		frequencies := make(map[string]int, len(queryTerms))
		for _, doc := range docs {
			entry := doc[field]
			total += entry.length
			if len(entry.counts) < len(queryTerms) {
				for token := range entry.counts {
					if queryTerms[token] {
						frequencies[token]++
					}
				}
				continue
			}
			for token := range queryTerms {
				if entry.counts[token] != 0 {
					frequencies[token]++
				}
			}
		}
		scorer.fields[field] = fieldStats{docFreq: frequencies, avgLen: float64(total) / float64(len(docs))}
	}
	return scorer
}

func (s factScorer) score(terms factTerms, fact Fact, tokens []string, phraseHit func(string) bool) (float64, string) {
	total := fieldWeightName*s.fields[nameField].bm25(terms[nameField], tokens, s.size) +
		fieldWeightDesc*s.fields[descriptionField].bm25(terms[descriptionField], tokens, s.size) +
		fieldWeightBody*s.fields[bodyField].bm25(terms[bodyField], tokens, s.size)
	field := matchField(fact, phraseHit)
	if field != "ranked" {
		total += exactPhraseBonus
	}
	return total, field
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

// countBacklinks gives each source fact at most one vote for a link target.
func countBacklinks(facts []Fact) map[string]int {
	counts := make(map[string]int)
	for _, fact := range facts {
		for i, link := range fact.Links {
			if !slices.Contains(fact.Links[:i], link) {
				counts[link]++
			}
		}
	}
	return counts
}
