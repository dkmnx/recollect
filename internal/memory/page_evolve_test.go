// page_evolve_test.go — the page pass's reply policing: malformed drafts
// count as dropped at parse time, and the filter strips drafts the fact
// snapshot cannot support so a page is never born stale or secret-bearing.

package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

import (
	"testing"

	"github.com/dkmnx/recollect/internal/memory"
)

// Test-audit record (test-audit skill):
// Protected behavior: a page reply's malformed drafts count in Dropped (a
// slug outside the catalog, a draft with no content), known drafts keep
// their sources in both the array and comma-separated string shapes, and
// fences or prose around the JSON parse through the shared tolerant
// extractor.
// Credible regression: a model-invented slug creating a page outside the
// fixed registry (unbounded namespace, unpredictable injection budget), or
// a parse failure losing valid sibling drafts.
// Coverage gap: the app E2E drives a well-formed reply only; the drop
// counting and shape tolerance are parse seams the E2E cannot see.
// Seam check: none — the test drives the production ParsePageDrafts entry
// the sweep calls.
func TestParsePageDrafts(t *testing.T) {
	result, err := memory.ParsePageDrafts("```json\n{\"pages\":[" +
		"{\"name\":\"conventions\",\"sources\":[\"a\",\"b\"],\"content\":\"tabs\"}," +
		"{\"name\":\"invented-slug\",\"sources\":\"a\",\"content\":\"x\"}," +
		"{\"name\":\"pitfalls\",\"sources\":\"a\",\"content\":\"\"}," +
		"{\"name\":\"key-decisions\",\"sources\":\"a, b\",\"content\":\"decided\"}" +
		"],\"reason\":\"two of four\"}\n```")
	if err != nil {
		t.Fatalf("parse page reply: %v", err)
	}
	if result.Dropped != 2 {
		t.Fatalf("dropped = %d, want the invented slug and the empty body counted", result.Dropped)
	}
	if len(result.Drafts) != 2 {
		t.Fatalf("drafts = %+v, want conventions and key-decisions", result.Drafts)
	}
	if result.Drafts[0].Name != "conventions" || len(result.Drafts[0].Sources) != 2 || result.Drafts[0].Content != "tabs" {
		t.Fatalf("drafts[0] = %+v, want the array-shaped sources kept", result.Drafts[0])
	}
	if result.Drafts[1].Name != "key-decisions" || len(result.Drafts[1].Sources) != 2 {
		t.Fatalf("drafts[1] = %+v, want the string-shaped sources kept", result.Drafts[1])
	}
	if result.Reason != "two of four" {
		t.Fatalf("reason = %q, want the reply's reason", result.Reason)
	}
}

// Test-audit record (test-audit skill):
// Protected behavior: the filter drops a draft's sources the fact snapshot
// does not name, skips a draft left without any valid source, skips a
// secret-bearing body, skips a draft for a slug the pass did not request,
// and passes a fully supported requested draft through with its sources
// intact.
// Credible regression: a page born citing a hallucinated fact name — the
// staleness rule would read that dangling source as eternal staleness and
// rewrite the page on every pass, a rewrite loop — a secret from a diff or
// note reaching the injected prompt through a page body, or a chatty
// reply rewriting pages the two-per-pass bound never requested.
// Coverage gap: the app E2E uses a reply whose sources all exist and whose
// slugs are requested; the dangling-source, secret, and unrequested paths
// are filter seams the E2E cannot see.
// Seam check: none — the filter runs against caller-supplied fact-name and
// requested-slug sets and the production secret matcher.
func TestFilterPageDrafts(t *testing.T) {
	result, err := memory.ParsePageDrafts(`{"pages":[
		{"name":"conventions","sources":"real, phantom","content":"tabs over spaces"},
		{"name":"pitfalls","sources":"phantom-a, phantom-b","content":"never retry in a funnel handler"},
		{"name":"key-decisions","sources":"real","content":"token = \"abcdefghijklmnopqrst\""},
		{"name":"component-map","sources":"real","content":"app owns the funnel"},
		{"name":"core-concepts","sources":"real","content":"unrequested draft"}
	],"reason":"filter"}`)
	if err != nil {
		t.Fatalf("parse page reply: %v", err)
	}
	factNames := map[string]bool{"real": true}
	requested := map[string]bool{"conventions": true, "component-map": true, "pitfalls": true, "key-decisions": true}
	pages, skipped := memory.FilterPageDrafts(result, factNames, requested)
	if skipped != 3 {
		t.Fatalf("skipped = %d, want the all-phantom draft, the secret body, and the unrequested draft counted", skipped)
	}
	if len(pages) != 2 {
		t.Fatalf("pages = %+v, want the conventions and component-map drafts", pages)
	}
	if pages[0].Name != "conventions" || len(pages[0].Sources) != 1 || pages[0].Sources[0] != "real" {
		t.Fatalf("pages[0] = %+v, want the phantom source stripped and the real one kept", pages[0])
	}
	if pages[1].Name != "component-map" || pages[1].Body != "app owns the funnel" || len(pages[1].Sources) != 1 {
		t.Fatalf("pages[1] = %+v, want the fully supported draft passed through untouched", pages[1])
	}
}
