// facts_search.go — fact-aware search: BM25-ranked matches across name,
// description, and body (facts_rank.go owns the scoring), reporting the
// channel, name, description, path, credited field, and score so recall can
// point at the exact fact and the model can see why it ranked.

package memory

import (
	"path/filepath"
	"strings"
)

// FactMatch is one fact search hit. Channel is "project" for a root fact
// and "topic" for a fact inside a topic directory. Scene is the fact's
// domain tag; empty means unscoped. Score is the ranking score; only order
// is contractual, the number is advisory.
type FactMatch struct {
	Channel     string  `json:"channel"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Path        string  `json:"path"`
	Field       string  `json:"field"`
	Scene       string  `json:"scene,omitempty"`
	Score       float64 `json:"score"`
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
	facts, err := s.ListFacts()
	if err != nil {
		return nil, err
	}
	if scene != "" {
		scoped := make([]Fact, 0, len(facts))
		for _, fact := range facts {
			if fact.Scene == "" || fact.Scene == scene {
				scoped = append(scoped, fact)
			}
		}
		facts = scoped
	}
	ranked := rankFacts(facts, query, s.now())
	matches := make([]FactMatch, 0, len(ranked))
	for _, hit := range ranked {
		channel := "project"
		if hit.fact.Path != "" {
			channel = "topic"
		}
		matches = append(matches, FactMatch{
			Channel:     channel,
			Name:        hit.fact.Name,
			Description: hit.fact.Description,
			Path:        filepath.Join(s.projectDir, filepath.FromSlash(hit.fact.RelPath())),
			Field:       hit.field,
			Scene:       hit.fact.Scene,
			Score:       hit.score,
		})
		if max > 0 && len(matches) >= max {
			break
		}
	}
	return matches, nil
}
