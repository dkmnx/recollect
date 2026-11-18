package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

import (
	"recollect/internal/memory"
	"testing"
)

func TestSearchLines(t *testing.T) {
	content := "alpha\nBeta value\ngamma\nbeta again"
	matches := memory.SearchLines(content, "beta", 0)
	if len(matches) != 2 {
		t.Fatalf("matches = %d, want 2", len(matches))
	}
	if matches[0].Line != 2 || matches[1].Line != 4 {
		t.Fatalf("line numbers = %d, %d", matches[0].Line, matches[1].Line)
	}
	if matches[0].Text != "Beta value" {
		t.Fatalf("first match text = %q", matches[0].Text)
	}
	if len(matches[0].Context) != 3 || matches[0].Context[0] != "alpha" {
		t.Fatalf("context = %#v", matches[0].Context)
	}
}

func TestSearchLinesCapsResults(t *testing.T) {
	content := "hit\nhit\nhit\nhit"
	if got := len(memory.SearchLines(content, "hit", 2)); got != 2 {
		t.Fatalf("capped matches = %d, want 2", got)
	}
}

func TestSearchLinesEmptyQueryAndNoMatch(t *testing.T) {
	if got := memory.SearchLines("abc", "  ", 0); got != nil {
		t.Fatalf("blank query returned %#v", got)
	}
	if got := memory.SearchLines("abc", "zzz", 0); got != nil {
		t.Fatalf("no match returned %#v", got)
	}
}

func TestSearchLinesContextAtEdges(t *testing.T) {
	matches := memory.SearchLines("first\nsecond", "first", 0)
	if len(matches) != 1 || len(matches[0].Context) != 2 {
		t.Fatalf("edge context = %#v", matches)
	}
}
