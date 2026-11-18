package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

import (
	"strings"
	"testing"

	"recollect/internal/memory"
)

// TestSecretHoldback pins the tail length a streamed reader must withhold.
// The cases are the partial matches a buffer can end on; the negatives are
// the ordinary text that must keep flowing.
func TestSecretHoldback(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		// Anchored tokens: withhold from the lead while the body is short.
		{"sk complete-prefix", "key=sk-proj-AbCdEf", len("sk-proj-AbCdEf")},
		{"sk lead only", "token sk-", len("sk-")},
		{"sk full length", "key=sk-proj-AbCdEfGhIjKlMnOpQrStUvWx", 0},
		{"akia short", "aws=AKIAIOSFODNN7EXAMPL", len("AKIAIOSFODNN7EXAMPL")},
		{"akia complete", "aws=AKIAIOSFODNN7EXAMPLE", 0},
		{"github short", "ghp_AbCdEf", len("ghp_AbCdEf")},
		{"slack short", "xoxb-1234", len("xoxb-1234")},
		{"bearer spaced", "Authorization: Bearer eyJhbGci", len("Bearer eyJhbGci")},

		// JWT: both segments must be long enough, and the dot must exist.
		{"jwt first segment short", "eyJhbGciOiJIUzI1", len("eyJhbGciOiJIUzI1")},
		{"jwt no dot yet", "eyJhbGciOiJIUzI1NiIsInR5cCI6", len("eyJhbGciOiJIUzI1NiIsInR5cCI6")},
		{"jwt second segment short", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.abc", len("eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.abc")},

		// Assignment: the value is short and the name is secret-ish.
		{"token value short", "token=abc", len("abc")},
		{"password quoted short", `password="hun`, len(`"hun`)},
		{"api key compound", "API_WRITE_KEY=abcdef", len("abcdef")},
		{"assignment complete", "token=abcdefghijklmno", 0},
		{"not a secret name", "level:debug", 0},
		{"value with a slash", "path=/usr/local", 0},

		// URL userinfo: the password has not reached its @ yet.
		{"userinfo no at", "postgres://user:passwor", len("passwor")},
		{"userinfo complete", "postgres://user:password@host", 0},
		// The password class is [^/@\s], so punctuation still counts.
		{"userinfo ends on punctuation", "postgres://user:pa}", len("pa}")},

		// Nothing to withhold.
		{"empty", "", 0},
		{"plain text", "make check runs vet and the race suite\n", 0},
		{"trailing newline", "build ok\n", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := memory.SecretHoldback(tc.in); got != tc.want {
				t.Fatalf("SecretHoldback(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestSecretHoldbackCoversRedaction is the coupling guard for every pattern
// in secretPatterns: splitting a secret at any byte must never deliver its
// lead-in, and a completed match must be redacted rather than withheld
// forever. A pattern added without a matching lead fails here.
func TestSecretHoldbackCoversRedaction(t *testing.T) {
	fixtures := []struct {
		name   string
		secret string
		marker string // the first byte of secret material, after the lead
	}{
		{"aws", "AKIAIOSFODNN7EXAMPLE", "IOSFODNN7EXAMPLE"},
		{"openai", "sk-proj-AbCdEfGhIjKlMnOpQrStUvWxYz012345", "AbCdEfGhIj"},
		{"github", "ghp_abcdefghijklmnopqrstuvwxyz0123456789", "abcdefghijkl"},
		{"slack", "xoxb-1234567890-abcdefghij", "1234567890"},
		{"jwt", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0", "hbGciOiJIUzI1"},
		{"bearer", "Bearer abcdefghijklmnopqrstuvwxyz0123456789", "abcdefghij"},
		{"assignment", "token=hunter2hunter2hunter2", "hunter2"},
		{"userinfo", "postgres://user:hunter2@db.example.com/x", "hunter2"},
	}
	for _, f := range fixtures {
		if !memory.SecretLike(f.secret) {
			t.Errorf("%s: fixture is not secret-like, so the case proves nothing: %q", f.name, f.secret)
			continue
		}
		start := strings.Index(f.secret, f.marker)
		if start < 0 {
			t.Fatalf("%s: marker %q is not in the fixture", f.name, f.marker)
		}

		// A completed match is redacted, so there is nothing left to
		// withhold: a holdback here would stall a settled job's tail.
		if got := memory.SecretHoldback(memory.Redact(f.secret)); got != 0 {
			t.Errorf("%s: complete match withheld %d bytes instead of being redacted", f.name, got)
		}

		// Every split: when the whole-buffer clean has not redacted the
		// prefix, the delivered length must stop before the secret starts.
		// This runs the production order — clean first, then withhold — so
		// it also pins that the two passes compose.
		for cut := 1; cut < len(f.secret); cut++ {
			prefix := f.secret[:cut]
			cleaned := memory.Redact(prefix)
			if cleaned != prefix {
				continue // redaction fired; the match never reaches the reader
			}
			hold := memory.SecretHoldback(cleaned)
			if hold > len(cleaned) {
				t.Fatalf("%s: holdback %d exceeds the buffer %d", f.name, hold, len(cleaned))
			}
			if delivered := len(cleaned) - hold; delivered > start {
				t.Errorf("%s: split at %d delivered %q, which reaches the secret at %d",
					f.name, cut, cleaned[:delivered], start)
			}
		}
	}
}
