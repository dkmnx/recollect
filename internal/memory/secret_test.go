package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

import (
	"github.com/dkmnx/recollect/internal/memory"
	"testing"
)

func TestSecretLikePositives(t *testing.T) {
	cases := []string{
		"AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
		"api_key: abcdef0123456789",
		"password = hunter2hunter2hunter2",
		"sk-abcdefghijklmnopqrstuvwxyz012345",
		"ghp_abcdefghijklmnopqrstuvwxyz0123456789",
		"xoxb-1234567890-abcdefghijkl",
		"-----BEGIN RSA PRIVATE KEY-----",
		"Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.payloadpart",
	}
	for _, text := range cases {
		if !memory.SecretLike(text) {
			t.Errorf("expected secret-like: %q", text)
		}
	}
}

func TestSecretLikeNegatives(t *testing.T) {
	cases := []string{
		"The token limit for this model is 200k",
		"password policies are documented in AGENTS.md",
		"Use make check before every commit",
		"sk- short",
		"// TODO: add auth later",
	}
	for _, text := range cases {
		if memory.SecretLike(text) {
			t.Errorf("false positive: %q", text)
		}
	}
}
