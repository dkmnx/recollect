// capture_experience_test.go — the experience layer rides the typed capture
// ops: an add with type=experience stores a fact whose links resolve to the
// facts it was learned from, with scene support and scene-scoped search
// unchanged.

package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

import (
	"testing"

	"github.com/dkmnx/recollect/internal/memory"
)

// Test-audit record (test-audit skill):
// Protected behavior: a consolidation add with type=experience lands a fact
// typed experience, scene-tagged, whose links carry the facts it was
// learned from and resolve through the store; the experience is searchable
// under its scene like any fact; and an update whose only change is the
// type promotes an existing fact to the experience layer.
// Credible regression: ApplyCaptureOp dropping the links or the scene, the
// experience becoming invisible to scene-scoped search, or UpsertFact's
// no-change short-circuit swallowing a type-only update (retry-sticky:
// capture retries converge to the same no-op, so the promotion never lands).
// Coverage gap: capture tests exercise only the old four-type vocabulary;
// no other case sees the layer survive parse, normalize, and apply.
// Seam check: none — the test drives the production pipeline entries the
// capture coordinator calls (ParseCaptureResult, ApplyCaptureOp) and reads
// back through the store's ReadFact, ResolveLinks, and SearchFacts.
func TestCaptureExperienceLayer(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	if _, err := store.AppendFact(memory.Fact{
		Name:        "retry-timeout",
		Description: "retries time out at five seconds",
		Body:        "retries time out at five seconds",
		Scene:       "coding",
	}); err != nil {
		t.Fatalf("seed source fact: %v", err)
	}

	result, err := memory.ParseCaptureResult(`{"ops":[
		{"action":"add","type":"experience","key":"retry-backoff-approach","summary":"retry approach","content":"rerun the failing batch twice before widening the timeout","links":"retry-timeout","scene":"coding"},
		{"action":"add","type":"experience","key":"bare-experience","summary":"bare experience","content":"an approach with no source facts"}
	],"reason":"reusable approaches"}`)
	if err != nil {
		t.Fatalf("parse capture reply: %v", err)
	}
	if len(result.Ops) != 2 {
		t.Fatalf("parsed ops = %d, want 2", len(result.Ops))
	}
	for _, op := range result.Ops {
		if err := memory.ApplyCaptureOp(store, op, "session-1", deterministicNow); err != nil {
			t.Fatalf("apply op %s: %v", op.Key, err)
		}
	}

	experience, err := store.ReadFact("retry-backoff-approach")
	if err != nil {
		t.Fatalf("read experience: %v", err)
	}
	if experience.Type != memory.FactTypeExperience {
		t.Fatalf("experience fact type = %q, want experience", experience.Type)
	}
	if experience.Scene != "coding" {
		t.Fatalf("experience scene = %q, want coding", experience.Scene)
	}
	if len(experience.Links) != 1 || experience.Links[0] != "retry-timeout" {
		t.Fatalf("experience links = %v, want [retry-timeout]", experience.Links)
	}
	resolved, missing, err := store.ResolveLinks("retry-backoff-approach")
	if err != nil {
		t.Fatalf("resolve links: %v", err)
	}
	if len(resolved) != 1 || resolved[0].Name != "retry-timeout" || len(missing) != 0 {
		t.Fatalf("resolved = %v missing = %v, want [retry-timeout] with none missing", resolved, missing)
	}

	bare, err := store.ReadFact("bare-experience")
	if err != nil {
		t.Fatalf("read bare experience: %v", err)
	}
	if bare.Type != memory.FactTypeExperience || len(bare.Links) != 0 {
		t.Fatalf("bare experience = type %q links %v, want experience with no links", bare.Type, bare.Links)
	}

	matches, err := store.SearchFacts("rerun failing batch", "coding", 10)
	if err != nil {
		t.Fatalf("scoped search: %v", err)
	}
	found := false
	for _, match := range matches {
		if match.Name == "retry-backoff-approach" {
			found = true
		}
	}
	if !found {
		t.Fatalf("scene-scoped search misses the experience: %+v", matches)
	}

	// An update whose only change is the type promotes the fact to the
	// experience layer: UpsertFact's no-change short-circuit must treat a
	// type difference as a change.
	promote, err := memory.ParseCaptureResult(`{"ops":[
		{"action":"update","type":"experience","key":"retry-timeout","summary":"retries time out at five seconds","content":"retries time out at five seconds","scene":"coding"}
	]}`)
	if err != nil {
		t.Fatalf("parse promote reply: %v", err)
	}
	for _, op := range promote.Ops {
		if err := memory.ApplyCaptureOp(store, op, "session-1", deterministicNow); err != nil {
			t.Fatalf("apply promote op %s: %v", op.Key, err)
		}
	}
	source, err := store.ReadFact("retry-timeout")
	if err != nil {
		t.Fatalf("read promoted fact: %v", err)
	}
	if source.Type != memory.FactTypeExperience {
		t.Fatalf("type-only update left type %q, want experience", source.Type)
	}
}
