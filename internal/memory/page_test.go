// page_test.go — the knowledge-page store: pages persist under pages/ as
// derived engine state, only catalog slugs are writable, bodies clamp, and
// staleness recomputes from disk.

package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"recollect/internal/memory"
)

// Test-audit record (test-audit skill):
// Protected behavior: UpsertPage writes a page under pages/<slug>.md that
// ListPages reads back with its fields and mtime, only catalog slugs are
// accepted, an oversized body clamps instead of failing (pages are derived
// state), and a store holding pages still enumerates zero facts.
// Credible regression: pages leaking into fact enumeration (a future walk
// change would corrupt the index and search), a non-catalog slug creating
// an unbounded page namespace, or a clamp turning into a size refusal
// against the derived-state contract.
// Coverage gap: no page store exists; nothing else can catch these.
// Seam check: none — the test drives the store's production write and read
// entries.
func TestPageStore(t *testing.T) {
	store, _, projectDir := newTestStore(t, memory.Limits{})
	written, err := store.UpsertPage(memory.Page{
		Name:      "conventions",
		Sources:   []string{"go-conventions", "test-layout"},
		Body:      "Tables stay with the table formatter.",
		UpdatedOn: "2026-10-23",
	})
	if err != nil {
		t.Fatalf("upsert page: %v", err)
	}
	if written.Question != "Which conventions does this project follow?" {
		t.Fatalf("question = %q, want the catalog question filled in", written.Question)
	}
	if len(written.Sources) != 2 || written.Sources[1] != "test-layout" {
		t.Fatalf("sources = %v, want both kept in order", written.Sources)
	}

	pages, err := store.ListPages()
	if err != nil {
		t.Fatalf("list pages: %v", err)
	}
	if len(pages) != 1 || pages[0].Name != "conventions" || pages[0].Body != "Tables stay with the table formatter." {
		t.Fatalf("pages = %+v, want the written page read back", pages)
	}
	if pages[0].ModTime.IsZero() {
		t.Fatal("page ModTime is zero; staleness would never fire")
	}

	if _, err := store.UpsertPage(memory.Page{Name: "not-in-catalog", Body: "x"}); err == nil {
		t.Fatal("non-catalog slug accepted; the page namespace must stay closed")
	}

	facts, err := store.ListFacts()
	if err != nil {
		t.Fatalf("list facts: %v", err)
	}
	if len(facts) != 0 {
		t.Fatalf("facts = %+v, want pages/ invisible to fact enumeration", facts)
	}
	if _, err := store.UpsertPage(memory.Page{Name: "conventions", Body: strings.Repeat("x", 3000)}); err != nil {
		t.Fatalf("oversized body refused: %v", err)
	}
	pages, err = store.ListPages()
	if err != nil {
		t.Fatalf("relist pages: %v", err)
	}
	if len(pages[0].Body) > 2*1024 {
		t.Fatalf("body = %d bytes, want clamped at 2 KiB", len(pages[0].Body))
	}
	_ = projectDir
}

// Test-audit record (test-audit skill):
// Protected behavior: staleness recomputes from disk — every catalog page
// is stale while missing, a page whose cited facts are older stays fresh,
// a fact touched or dropped after the page write makes the page stale
// again, rewriting clears it, and a sourceless page never goes stale.
// Credible regression: staleness inverted (the sweep rewrites fresh pages
// forever, or never rewrites a stale one), a dropped cited fact leaving the
// page fresh, or the stale set escaping the catalog.
// Coverage gap: no staleness rule exists; nothing else can catch these.
// Seam check: none — the rule reads only production store state (fact
// files and page files).
func TestStalePages(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})

	all := []string{"component-map", "core-concepts", "conventions", "key-decisions", "pitfalls"}
	stale, err := store.StalePages()
	if err != nil {
		t.Fatalf("stale pages: %v", err)
	}
	if len(stale) != len(all) {
		t.Fatalf("stale = %v, want every missing catalog page", stale)
	}

	if _, err := store.AppendFact(memory.Fact{Name: "go-conventions", Description: "tabs", Body: "tabs"}); err != nil {
		t.Fatalf("append fact: %v", err)
	}
	if _, err := store.UpsertPage(memory.Page{Name: "conventions", Sources: []string{"go-conventions"}, Body: "use tabs"}); err != nil {
		t.Fatalf("upsert page: %v", err)
	}
	stale, _ = store.StalePages()
	for _, name := range stale {
		if name == "conventions" {
			t.Fatalf("stale = %v, want the fresh page absent", stale)
		}
	}

	if _, err := store.UpdateFact("go-conventions", "", "use tabs, not spaces", nil); err != nil {
		t.Fatalf("update fact: %v", err)
	}
	if stale, _ = store.StalePages(); !contains(stale, "conventions") {
		t.Fatalf("stale = %v, want the page stale after its source changed", stale)
	}

	if _, err := store.UpsertPage(memory.Page{Name: "conventions", Sources: []string{"go-conventions"}, Body: "use tabs v2", UpdatedOn: "2026-10-23"}); err != nil {
		t.Fatalf("rewrite page: %v", err)
	}
	if _, err := store.DeleteFact("go-conventions"); err != nil {
		t.Fatalf("delete fact: %v", err)
	}
	stale, _ = store.StalePages()
	if !contains(stale, "conventions") {
		t.Fatalf("stale = %v, want the page stale after its source dropped", stale)
	}

	if _, err := store.UpsertPage(memory.Page{Name: "pitfalls", Body: "hand written, no sources"}); err != nil {
		t.Fatalf("upsert sourceless page: %v", err)
	}
	stale, _ = store.StalePages()
	if contains(stale, "pitfalls") {
		t.Fatalf("stale = %v, want a sourceless page never stale", stale)
	}
}

// TestStalePagesOrder pins the rewrite order: missing pages first in
// catalog order, then stale existing pages oldest UpdatedOn first.
//
// Test-audit record (test-audit skill):
// Protected behavior: the stale list orders missing pages by catalog order
// ahead of existing stale pages sorted by UpdatedOn ascending, so the
// two-per-pass sweep converges deterministically.
// Credible regression: an unordered set making the sweep rewrite the same
// two pages every pass while others starve.
// Coverage gap: the ordering is invisible to TestStalePages, which only
// checks membership.
// Seam check: none — order reads from production page files.
func TestStalePagesOrder(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	fact, err := store.AppendFact(memory.Fact{Name: "a", Description: "a", Body: "a"})
	if err != nil {
		t.Fatalf("append fact: %v", err)
	}
	if _, err := store.UpsertPage(memory.Page{Name: "conventions", Sources: []string{"a"}, Body: "c", UpdatedOn: "2026-02-01"}); err != nil {
		t.Fatalf("upsert conventions: %v", err)
	}
	if _, err := store.UpsertPage(memory.Page{Name: "pitfalls", Sources: []string{"a"}, Body: "p", UpdatedOn: "2026-01-01"}); err != nil {
		t.Fatalf("upsert pitfalls: %v", err)
	}
	// The staleness rule orders the fact file against the page files by
	// mtime. Two writes inside one filesystem clock tick carry the same
	// time, which orders nothing. The fixture states the order the contract
	// describes instead of racing for it. Pages sit an hour behind their
	// source fact, and a real update lands newest of all. The hour gap makes
	// the result independent of the machine's clock resolution. The fact
	// keeps the older stamp, so the assertion still fails when an update
	// stops rewriting the fact file.
	now := time.Now()
	for _, name := range []string{"conventions", "pitfalls"} {
		path := filepath.Join(memory.PagesDir(store.ProjectDir()), name+".md")
		if err := os.Chtimes(path, now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
			t.Fatalf("backdate page %s: %v", name, err)
		}
	}
	factPath := filepath.Join(store.ProjectDir(), fact.RelPath())
	if err := os.Chtimes(factPath, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("backdate fact: %v", err)
	}
	if _, err := store.UpdateFact("a", "", "b", nil); err != nil {
		t.Fatalf("update fact: %v", err)
	}
	stale, err := store.StalePages()
	if err != nil {
		t.Fatalf("stale pages: %v", err)
	}
	want := []string{"component-map", "core-concepts", "key-decisions", "pitfalls", "conventions"}
	if len(stale) != len(want) {
		t.Fatalf("stale = %v, want %v", stale, want)
	}
	for i := range want {
		if stale[i] != want[i] {
			t.Fatalf("stale[%d] = %q, want %q (full list %v)", i, stale[i], want[i], want)
		}
	}
}

func contains(list []string, name string) bool {
	for _, entry := range list {
		if entry == name {
			return true
		}
	}
	return false
}
