// facts_search.go — fact-aware search: BM25-ranked matches across name,
// description, and body (facts_rank.go owns the scoring), reporting the
// channel, name, description, path, credited field, and score so recall can
// point at the exact fact and the model can see why it ranked.

package memory

import (
	"path/filepath"
	"sort"
	"strings"
)

// FactMatch is one fact search hit. Channel is "project" for a root fact
// and "topic" for a fact inside a topic directory. Scene is the fact's
// domain tag; empty means unscoped. Score is the ranking score; only order
// is contractual, the number is advisory. Body holds the fact body for
// lexical hits; semantic hits leave it empty.
type FactMatch struct {
	Channel     string  `json:"channel"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Body        string  `json:"body,omitempty"`
	Path        string  `json:"path"`
	Field       string  `json:"field"`
	Scene       string  `json:"scene,omitempty"`
	Score       float64 `json:"score"`
	Kind        string  `json:"kind,omitempty"`
}

// SearchFacts ranks the corpus against query and caps the results at max
// (non-positive means no cap). A scene filters the corpus to facts tagged
// with that scene plus unscoped facts, so a missing tag never hides
// knowledge; an empty scene searches everything.
func (s *Store) SearchFacts(query, scene string, max int) ([]FactMatch, error) {
	query = strings.TrimSpace(query)
	scene = NormalizeScene(scene)
	if query == "" {
		return nil, nil
	}
	query = filterSearchText(query)
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	corpus, err := s.searchCorpus()
	if err != nil {
		return nil, err
	}
	allCorpus := corpus
	var candidates map[string]bool
	if s.searchBackend != "" && len(queryTokens(query)) > 0 {
		candidates = s.indexedSearch(allCorpus, query)
	}
	facts := make([]Fact, 0, len(allCorpus))
	rels := make([]string, 0, len(allCorpus))
	terms := make([]factTerms, 0, len(allCorpus))
	for _, item := range allCorpus {
		if scene != "" && item.Fact.Scene != "" && item.Fact.Scene != scene {
			continue
		}
		facts = append(facts, item.Fact)
		rels = append(rels, item.RelPath)
		terms = append(terms, item.terms)
	}
	ranked := rankCandidates(facts, terms, rels, candidates, query, s.now())
	matches := make([]FactMatch, 0, len(ranked))
	seen := make(map[string]bool, len(ranked))
	for _, hit := range ranked {
		if hit.index < 0 || hit.index >= len(rels) {
			continue
		}
		rel := rels[hit.index]
		seen[rel] = true
		channel := "project"
		if dir := filepath.ToSlash(filepath.Dir(rel)); dir != "." {
			channel = "topic"
		}
		matches = append(matches, FactMatch{
			Channel:     channel,
			Name:        hit.fact.Name,
			Description: hit.fact.Description,
			Body:        hit.fact.Body,
			Path:        filepath.Join(s.projectDir, filepath.FromSlash(rel)),
			Field:       hit.field,
			Scene:       hit.fact.Scene,
			Score:       hit.score,
		})
		if max > 0 && len(matches) >= max {
			break
		}
	}
	if s.searchBackend == SearchBackendHybrid && max > 0 {
		matches = s.fillSemantic(matches, allCorpus, query, scene, max, seen)
	}
	return matches, nil
}

func (s *Store) fillSemantic(matches []FactMatch, corpus []searchFact, query, scene string, max int, seen map[string]bool) []FactMatch {
	scores, err := s.semanticCandidates(query, scene)
	if err != nil {
		indexDiagnostic(err)
		return matches
	}
	byPath := make(map[string]searchFact, len(corpus))
	for _, fact := range corpus {
		if scene != "" && fact.Fact.Scene != "" && fact.Fact.Scene != scene {
			continue
		}
		byPath[fact.RelPath] = fact
	}
	const semanticDistanceCutoff = 0.6
	type candidate struct {
		rel  string
		dist float64
	}
	list := make([]candidate, 0, len(scores))
	for rel, dist := range scores {
		if !seen[rel] && dist <= semanticDistanceCutoff {
			list = append(list, candidate{rel: rel, dist: dist})
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].dist < list[j].dist })
	for _, item := range list {
		if max > 0 && len(matches) >= max {
			break
		}
		fact, ok := byPath[item.rel]
		if !ok {
			continue
		}
		channel := "project"
		if dir := filepath.ToSlash(filepath.Dir(fact.RelPath)); dir != "." {
			channel = "topic"
		}
		matches = append(matches, FactMatch{
			Channel: channel, Name: fact.Name, Description: fact.Description,
			Path:  filepath.Join(s.projectDir, filepath.FromSlash(fact.RelPath)),
			Scene: fact.Scene, Kind: "semantic",
		})
	}
	return matches
}
