package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	httpEmbedTimeout          = 30 * time.Second
	httpEmbedMaxResponseBytes = 16 << 20
)

// HTTPEmbedConfig identifies a host-supplied embeddings endpoint.
type HTTPEmbedConfig struct {
	BaseURL    string
	Model      string
	APIKey     string
	Dimensions int
}

type httpEmbedder struct {
	config HTTPEmbedConfig
	client *http.Client
}

// NewHTTPEmbedder returns an embedder that calls an OpenAI-compatible endpoint.
func NewHTTPEmbedder(config HTTPEmbedConfig) EmbeddingProvider {
	return &httpEmbedder{config: config, client: &http.Client{Timeout: httpEmbedTimeout}}
}

func (e *httpEmbedder) Dimensions() int { return e.config.Dimensions }
func (e *httpEmbedder) Name() string    { return e.config.Model }

func (e *httpEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(map[string]any{"model": e.config.Model, "input": texts})
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(strings.TrimSpace(e.config.BaseURL), "/") + "/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.config.APIKey)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("embeddings: status %d: %s", resp.StatusCode, data)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, httpEmbedMaxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > httpEmbedMaxResponseBytes {
		return nil, fmt.Errorf("embeddings: response too large (limit %d bytes)", httpEmbedMaxResponseBytes)
	}
	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if len(result.Data) != len(texts) {
		return nil, fmt.Errorf("embeddings: got %d vectors for %d texts", len(result.Data), len(texts))
	}
	out := make([][]float32, len(texts))
	for i, item := range result.Data {
		if len(item.Embedding) != e.config.Dimensions {
			return nil, fmt.Errorf("embeddings: dimension %d, want %d", len(item.Embedding), e.config.Dimensions)
		}
		out[i] = item.Embedding
	}
	return out, nil
}
