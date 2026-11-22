package e2e

// audit: behavior - hybrid search adds labeled semantic hits without changing lexical hits or their order.
// audit: regression - a vector backend could replace lexical results or leak its text into RPC bodies.
// audit: coverage gap - no existing test enables embeddings or checks the result kind field.
// audit: seam - tests drive the built binary with JSON-RPC search and maintenance frames.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dkmnx/recollect/internal/memory"
)

func startEmbedServer(t *testing.T) *httptest.Server {
	t.Helper()
	embedder := memory.NewDeterministicEmbedder(256)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if r.URL.Path != "/embeddings" {
			t.Errorf("embeddings path = %q", r.URL.Path)
		}
		if body.Model != "test" {
			t.Errorf("embeddings model = %q", body.Model)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q", got)
		}
		vectors, err := embedder.Embed(r.Context(), body.Input)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		data := make([]map[string]any, len(vectors))
		for i, vector := range vectors {
			data[i] = map[string]any{"embedding": vector}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
}

func writeHybridConfig(t *testing.T, userDir, baseURL string) {
	t.Helper()
	if err := os.MkdirAll(userDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	raw := fmt.Sprintf(
		`{"search":{"backend":"hybrid"},"embedding":{"baseURL":%q,"model":"test","dimensions":256,"apiKeyEnv":"EMBED_KEY"}}`,
		baseURL,
	)
	if err := os.WriteFile(filepath.Join(userDir, "config.json"), []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

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
	t.Setenv("EMBED_KEY", "test-key")
	server := startEmbedServer(t)
	defer server.Close()
	d := tempDirs(t)
	writeHybridConfig(t, d.user, server.URL)
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
	t.Setenv("EMBED_KEY", "test-key")
	server := startEmbedServer(t)
	defer server.Close()
	d := tempDirs(t)
	writeHybridConfig(t, d.user, server.URL)
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

func TestHybridWithoutEndpointFailsInitialize(t *testing.T) {
	t.Setenv("RECOLLECT_SEARCH_INDEX", "hybrid")
	d := tempDirs(t)
	c := start(t)
	defer c.cmd.Process.Kill()
	resp := c.call(t, "initialize", initParams{
		ProjectDir: d.project, UserDir: d.user, HarnessID: "refuse",
		Capabilities: []string{"memory"}, Protocol: map[string]int{"major": 1},
	})
	if resp.Error == nil || resp.Error.Code != -32006 {
		t.Fatalf("initialize = %+v, want storage failure", resp.Error)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(c.stderr.String(), "hybrid search requires embedding.baseURL") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(c.stderr.String(), "hybrid search requires embedding.baseURL") {
		t.Fatalf("stderr = %q", c.stderr.String())
	}
}
