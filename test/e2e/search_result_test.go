package e2e

// audit: behavior - project search returns the credited match field and the stored fact body.
// audit: regression - mapping the match reason into body made search report "description" as fact text.
// audit: coverage gap - existing search tests check match names or non-empty bodies, never the body value or field label.
// audit: seam - tests drive the built binary with JSON-RPC and inspect the search result.

import (
	"encoding/json"
	"testing"
)

func TestSearchReportsFieldAndFactBody(t *testing.T) {
	d := tempDirs(t)
	c := start(t)
	defer c.cmd.Process.Kill()
	initialize(t, c, d, "harness-a", map[string]int{"major": 1})

	ok(t, c.call(t, "memory.execute", execParams{
		Target: "project", Operation: "append", RequestID: "field-1",
		Name: "deploy-port-fact", Description: "where deploy port lives",
		Body: "The deploy port is 8443.",
	}))

	raw := ok(t, c.call(t, "memory.execute", execParams{
		Target: "project", Operation: "search", Query: "lives", Limit: 10,
	}))
	var result struct {
		Matches []struct {
			Name  string `json:"name"`
			Body  string `json:"body"`
			Field string `json:"field"`
		} `json:"matches"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("search matches: %s", raw)
	}
	match := result.Matches[0]
	if match.Name != "deploy-port-fact" {
		t.Errorf("match name = %q, want deploy-port-fact", match.Name)
	}
	if match.Field != "description" {
		t.Errorf("match field = %q, want description", match.Field)
	}
	if match.Body != "The deploy port is 8443." {
		t.Errorf("match body = %q, want the stored fact body", match.Body)
	}
}
