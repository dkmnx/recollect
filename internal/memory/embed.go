package memory

import (
	"context"
	"errors"
	"hash/fnv"
	"math"
)

// The host owns the provider; the engine never loads a model or holds credentials.
type EmbeddingProvider interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Dimensions() int
	Name() string
}

var evalSynonyms = map[string]string{
	"configuration": "config", "config": "config", "setup": "config",
	"deploy": "deploy", "deployment": "deploy", "rollout": "deploy",
	"concise": "concise", "brief": "concise", "terse": "concise",
	"pipeline": "pipeline", "workflow": "pipeline", "notes": "pipeline",
}

// NewDeterministicEmbedder is deterministic and offline, not a replacement for a real model.
func NewDeterministicEmbedder(dimensions int) EmbeddingProvider {
	if dimensions <= 0 {
		dimensions = 256
	}
	return deterministicEmbedder{dimensions: dimensions}
}

type deterministicEmbedder struct{ dimensions int }

func (e deterministicEmbedder) Dimensions() int { return e.dimensions }
func (e deterministicEmbedder) Name() string    { return "deterministic-eval" }

func (e deterministicEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i, text := range texts {
		vector := make([]float32, e.dimensions)
		for _, token := range tokenize(text) {
			if canonical, ok := evalSynonyms[token]; ok {
				token = canonical
			}
			h := fnv.New32a()
			h.Write([]byte(token))
			vector[int(h.Sum32()%uint32(e.dimensions))]++
		}
		var norm float64
		for _, value := range vector {
			norm += float64(value) * float64(value)
		}
		if norm > 0 {
			scale := float32(1 / math.Sqrt(norm))
			for j := range vector {
				vector[j] *= scale
			}
		}
		result[i] = vector
	}
	return result, nil
}

var errNoEmbedder = errors.New("no embedding provider configured")
