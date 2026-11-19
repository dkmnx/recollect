package e2e

// audit: behavior - hybrid search adds labeled semantic hits without changing lexical hits or their order.
// audit: regression - a vector backend could replace lexical results or leak its text into RPC bodies.
// audit: coverage gap - no existing test enables embeddings or checks the result kind field.
// audit: seam - tests drive the built binary with JSON-RPC search and maintenance frames.

import (
	"encoding/json"
	"testing"
)

type semanticMatch struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
	Kind        string `json:"kind"`
}

func semanticSearch(t *testing.T, c *client, query string) []semanticMatch {
	t.Helper()
	raw := ok(t, c.call(t, "memory.execute", execParams{
		Target: "project", Operation: "search", Query: query, Limit: 20,
	}))
	var result struct {
		Matches []semanticMatch `json:"matches"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result.Matches
}

func TestHybridSearchAddsLabeledSemanticHits(t *testing.T) {
	t.Setenv("RECOLLECT_SEARCH_INDEX", "hybrid")
	d := tempDirs(t)
	c := start(t)
	defer c.cmd.Process.Kill()
	initialize(t, c, d, "hybrid-harness", map[string]int{"major": 1})
	appendNamedFact(t, c, "sem-1", "setup-doc", "setup guide", "steps for installation")

	refreshed := ok(t, c.call(t, "maintenance.execute", map[string]any{"action": "refresh"}))
	var refreshResult struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(refreshed, &refreshResult); err != nil || refreshResult.Action != "refresh" {
		t.Fatalf("refresh result: %s %v", refreshed, err)
	}

	lexical := semanticSearch(t, c, "setup")
	if len(lexical) != 1 || lexical[0].Kind != "" || lexical[0].Body == "" {
		t.Fatalf("lexical hit changed shape: %+v", lexical)
	}

	semantic := semanticSearch(t, c, "configuration")
	if len(semantic) != 1 || semantic[0].Kind != "semantic" || semantic[0].Body != "" {
		t.Fatalf("semantic hit shape: %+v", semantic)
	}
	if semantic[0].Name != "setup-doc" {
		t.Fatalf("semantic hit name: %+v", semantic)
	}
}

func TestHybridSearchKeepsLexicalWhenVectorsUnavailable(t *testing.T) {
	t.Setenv("RECOLLECT_SEARCH_INDEX", "hybrid")
	d := tempDirs(t)
	c := start(t)
	defer c.cmd.Process.Kill()
	initialize(t, c, d, "hybrid-harness", map[string]int{"major": 1})
	appendNamedFact(t, c, "lex-1", "setup-doc", "setup guide", "steps for installation")

	if matches := semanticSearch(t, c, "setup"); len(matches) != 1 || matches[0].Kind != "" {
		t.Fatalf("lexical search unavailable before backfill: %+v", matches)
	}
	if matches := semanticSearch(t, c, "unrelated-han-查询"); len(matches) != 0 {
		t.Fatalf("unrelated query produced hits: %+v", matches)
	}
}
