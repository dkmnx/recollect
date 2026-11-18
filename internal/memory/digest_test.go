package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

import (
	"github.com/dkmnx/recollect/internal/memory"
	"strings"
	"testing"
	"time"
)

func TestDigestRoundTrip(t *testing.T) {
	date := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 9, 19, 11, 30, 0, 0, time.UTC)
	digest := memory.Digest{
		SessionID: "abc123",
		Topic:     "stream retry",
		Summary:   "Landed the retry fix.\nNext: watch flakes.",
		Date:      date,
		Updated:   updated,
		Fallback:  true,
	}
	out := memory.FormatDigest(digest)
	got, ok := memory.ParseDigest(out, "/p/sessions/x.md")
	if !ok {
		t.Fatalf("parse failed: %q", out)
	}
	if got.SessionID != digest.SessionID || got.Topic != digest.Topic || got.Summary != digest.Summary {
		t.Errorf("round trip = %+v", got)
	}
	if !got.Date.Equal(date) || !got.Updated.Equal(updated) || !got.Fallback {
		t.Errorf("round trip metadata = %+v", got)
	}
	if got.Path != "/p/sessions/x.md" {
		t.Errorf("path = %q", got.Path)
	}
}

func TestParseDigestWithoutSession(t *testing.T) {
	if _, ok := memory.ParseDigest("# topic\n\nsummary\n", "p"); ok {
		t.Fatal("expected ok=false without a Session line")
	}
}

func TestParseDigestToleratesMissingFields(t *testing.T) {
	got, ok := memory.ParseDigest("Session: s1\n\nJust a summary.\n", "p")
	if !ok || got.SessionID != "s1" || got.Summary != "Just a summary." || got.Fallback {
		t.Fatalf("tolerant parse = %+v ok=%v", got, ok)
	}
}

func TestFormatDigestOmitsEmptyOptionals(t *testing.T) {
	out := memory.FormatDigest(memory.Digest{SessionID: "s1", Summary: "x"})
	if strings.Contains(out, "Date:") || strings.Contains(out, "Updated:") || strings.Contains(out, "Fallback:") {
		t.Fatalf("optional fields rendered: %q", out)
	}
	if strings.HasPrefix(out, "# ") {
		t.Fatalf("topic heading rendered without a topic: %q", out)
	}
}
