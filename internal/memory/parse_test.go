package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

import (
	"github.com/dkmnx/recollect/internal/memory"
	"strings"
	"testing"
)

func TestNormalizeNewlines(t *testing.T) {
	if got := memory.NormalizeNewlines("a\r\nb\rc"); got != "a\nb\nc" {
		t.Fatalf("NormalizeNewlines = %q", got)
	}
	if got := memory.NormalizeNewlines("plain"); got != "plain" {
		t.Fatalf("NormalizeNewlines changed plain text: %q", got)
	}
}

func TestTailBytes(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		max       int
		want      string
		truncated bool
	}{
		{"fits exactly", "abc", 3, "abc", false},
		{"cuts ascii", "hello", 3, "llo", true},
		{"cuts before multibyte", "héllo", 4, "llo", true},
		{"aligns on multibyte", "héllo", 5, "éllo", true},
		{"zero max", "abc", 0, "", true},
		{"empty", "", 0, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, truncated := memory.TailBytes(tc.input, tc.max)
			if got != tc.want || truncated != tc.truncated {
				t.Fatalf("TailBytes(%q, %d) = (%q, %v), want (%q, %v)", tc.input, tc.max, got, truncated, tc.want, tc.truncated)
			}
			if got != "" && !utf8Valid(got) {
				t.Fatalf("TailBytes(%q, %d) produced invalid UTF-8 %q", tc.input, tc.max, got)
			}
		})
	}
}

func TestHeadBytes(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		max       int
		want      string
		truncated bool
	}{
		{"fits exactly", "abc", 3, "abc", false},
		{"cuts ascii", "hello", 3, "hel", true},
		{"cut inside multibyte backs off", "héllo", 2, "h", true},
		{"multibyte fits", "héllo", 3, "hé", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, truncated := memory.HeadBytes(tc.input, tc.max)
			if got != tc.want || truncated != tc.truncated {
				t.Fatalf("HeadBytes(%q, %d) = (%q, %v), want (%q, %v)", tc.input, tc.max, got, truncated, tc.want, tc.truncated)
			}
			if got != "" && !utf8Valid(got) {
				t.Fatalf("HeadBytes(%q, %d) produced invalid UTF-8 %q", tc.input, tc.max, got)
			}
		})
	}
}

func TestCollapseLine(t *testing.T) {
	if got := memory.CollapseLine("  a\nb\tc  "); got != "a b c" {
		t.Fatalf("CollapseLine = %q", got)
	}
	if got := memory.CollapseLine(""); got != "" {
		t.Fatalf("CollapseLine empty = %q", got)
	}
}

func utf8Valid(s string) bool {
	return len(s) > 0 && strings.ToValidUTF8(s, "") == s
}
