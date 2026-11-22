package memory

// audit: behavior - the HTTP embedder bounds request time and response size.
// audit: regression - a stalled or oversized embeddings response could hang or exhaust memory during hybrid search.
// audit: coverage gap - embed_http.go has no tests for client timeout or response size.
// audit: seam - tests call NewHTTPEmbedder and drive Embed against an httptest server; the timeout invariant uses a same-package assertion.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPEmbedderReturnsVectors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"embedding":[0.25,0.75]}]}`)
	}))
	defer server.Close()

	embedder := NewHTTPEmbedder(HTTPEmbedConfig{
		BaseURL:    server.URL,
		Model:      "test",
		Dimensions: 2,
	})
	vectors, err := embedder.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("Embed returned error: %v", err)
	}
	if len(vectors) != 1 {
		t.Fatalf("Embed returned %d vectors, want 1", len(vectors))
	}
	if len(vectors[0]) != 2 {
		t.Fatalf("Embed returned %d dimensions, want 2", len(vectors[0]))
	}
	if vectors[0][0] != 0.25 || vectors[0][1] != 0.75 {
		t.Fatalf("Embed returned %v, want [0.25 0.75]", vectors[0])
	}
}

func TestHTTPEmbedderHasClientTimeout(t *testing.T) {
	embedder, ok := NewHTTPEmbedder(HTTPEmbedConfig{
		BaseURL:    "http://127.0.0.1:1",
		Model:      "test",
		Dimensions: 1,
	}).(*httpEmbedder)
	if !ok {
		t.Fatal("NewHTTPEmbedder did not return an httpEmbedder")
	}
	if want := 30 * time.Second; embedder.client.Timeout != want {
		t.Fatalf("client timeout = %s, want %s", embedder.client.Timeout, want)
	}
}

func TestHTTPEmbedderResponseSizeBoundary(t *testing.T) {
	const limit = 16 << 20

	bodyOfSize := func(t *testing.T, total int) string {
		t.Helper()
		const prefix = `{"data":[{"embedding":[0.1]}],"pad":"`
		const suffix = `"}`
		pad := total - len(prefix) - len(suffix)
		if pad < 0 {
			t.Fatalf("body size %d is smaller than the JSON framing", total)
		}
		body := prefix + strings.Repeat("x", pad) + suffix
		if len(body) != total {
			t.Fatalf("body length = %d, want %d", len(body), total)
		}
		return body
	}

	embed := func(t *testing.T, body string) ([][]float32, error) {
		t.Helper()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, body)
		}))
		defer server.Close()
		embedder := NewHTTPEmbedder(HTTPEmbedConfig{
			BaseURL:    server.URL,
			Model:      "test",
			Dimensions: 1,
		})
		return embedder.Embed(context.Background(), []string{"hello"})
	}

	t.Run("exactly at limit", func(t *testing.T) {
		vectors, err := embed(t, bodyOfSize(t, limit))
		if err != nil {
			t.Fatalf("Embed rejected a response of exactly 16 MiB: %v", err)
		}
		if len(vectors) != 1 || len(vectors[0]) != 1 {
			t.Fatalf("Embed returned %d vectors with lengths %v", len(vectors), vectors)
		}
	})

	t.Run("one byte over limit", func(t *testing.T) {
		_, err := embed(t, bodyOfSize(t, limit+1))
		if err == nil {
			t.Fatal("Embed accepted a response one byte over the limit")
		}
		if !strings.Contains(err.Error(), "too large") {
			t.Fatalf("Embed error = %q, want a size-limit message", err)
		}
	})
}
