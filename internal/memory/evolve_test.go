// evolve_test.go — the sweep's op policing: malformed ops count as
// dropped at parse time, out-of-contract actions count as skipped, and a
// drop that targets a fact the same reply corrected is refused as
// self-contradictory instead of deleting the survivor it just rewrote.

package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

import (
	"testing"

	"recollect/internal/memory"
)

// Test-audit record (test-audit skill):
// Protected behavior: a sweep reply's malformed ops count in Dropped, the
// filter keeps correct and drop ops, and a drop whose key the same reply
// corrected is refused and counted, so a self-contradictory reply can
// never delete the survivor it just rewrote.
// Credible regression: the filter passing a correct-plus-drop pair through
// in order (the correction lands, the drop then deletes the survivor, and
// the outcome reports success), or the dropped count vanishing (a reply
// that tried a merge reads as "found nothing").
// Coverage gap: the app E2E drives a well-formed reply only; the pair
// rejection and the dropped count are parse-and-filter seams the E2E
// cannot see.
// Seam check: none — the test drives the production ParseCaptureResult and
// FilterEvolveOps entries the command calls.
func TestFilterEvolveOps(t *testing.T) {
	result, err := memory.ParseCaptureResult(`{"ops":[
		{"action":"correct","type":"project","key":"survivor","summary":"merged scope"},
		{"action":"drop","type":"project","key":"survivor"},
		{"action":"drop","type":"project","key":"duplicate"},
		{"action":"merge","type":"project","key":"whatever","summary":"unknown action"},
		{"action":"add","type":"project","key":"fresh","summary":"no new facts","content":"x"}
	],"reason":"sweep"}`)
	if err != nil {
		t.Fatalf("parse sweep reply: %v", err)
	}
	if result.Dropped != 1 {
		t.Fatalf("dropped = %d, want the unknown-action op counted", result.Dropped)
	}
	ops, skipped := memory.FilterEvolveOps(result)
	if len(ops) != 2 {
		t.Fatalf("ops = %+v, want the survivor correction and the duplicate drop", ops)
	}
	if ops[0].Action != "correct" || ops[0].Key != "survivor" {
		t.Fatalf("ops[0] = %+v, want the survivor correction", ops[0])
	}
	if ops[1].Action != "drop" || ops[1].Key != "duplicate" {
		t.Fatalf("ops[1] = %+v, want the duplicate drop", ops[1])
	}
	if skipped != 2 {
		t.Fatalf("skipped = %d, want the contradictory drop and the add", skipped)
	}
}
