// capture_scene_test.go — scenes flow through the capture pipeline: a
// consolidation reply's scene stamps the stored fact, an op without a scene
// stores unscoped, and a correct op preserves the fact's scene.

package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

import (
	"strings"
	"testing"

	"github.com/dkmnx/recollect/internal/memory"
)

// Test-audit record (test-audit skill):
// Protected behavior: the capture pipeline carries a consolidation reply's
// scene onto the stored fact — an add with scene stores it scoped, an add
// without scene stores unscoped, an update re-tags the fact (including an
// update whose only change is the scene), and a correct rewrites a fact in
// place without touching its scene even when the op carries one.
// Credible regression: normalizeCaptureOp dropping or mangling the scene
// field (every captured fact silently lands unscoped and scoping dies),
// ApplyCaptureOp forgetting to stamp it, UpsertFact's no-change
// short-circuit swallowing a scene-only update, or a correct wiping the
// scene it must preserve.
// Coverage gap: capture_parse_test and capture_apply_test drive ops without
// a scene, so no existing case can see the field survive parse, normalize,
// and apply.
// Seam check: none — the test drives the production pipeline entries the
// capture coordinator calls (ParseCaptureResult, ApplyCaptureOp) and reads
// back through the store.
func TestCaptureSceneTagging(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	result, err := memory.ParseCaptureResult(`{"ops":[
		{"action":"add","type":"project","key":"retry-timeout","summary":"retry timeout","content":"retries time out at five seconds","scene":"coding"},
		{"action":"add","type":"user","key":"brief-answers","summary":"brief answers","content":"the user wants brief answers"}
	],"reason":"durable conventions"}`)
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

	fact, err := store.ReadFact("retry-timeout")
	if err != nil {
		t.Fatalf("read retry-timeout: %v", err)
	}
	if fact.Scene != "coding" {
		t.Fatalf("captured fact scene = %q, want coding", fact.Scene)
	}
	unscoped, err := store.ReadFact("brief-answers")
	if err != nil {
		t.Fatalf("read brief-answers: %v", err)
	}
	if unscoped.Scene != "" {
		t.Fatalf("scene-less captured fact scene = %q, want empty", unscoped.Scene)
	}

	matches, err := store.SearchFacts("timeout", "coding", 10)
	if err != nil {
		t.Fatalf("scoped search: %v", err)
	}
	if len(matches) != 1 || matches[0].Name != "retry-timeout" || matches[0].Scene != "coding" {
		t.Fatalf("coding search matches = %+v, want only retry-timeout", matches)
	}

	corrected, err := memory.ParseCaptureResult(`{"ops":[
		{"action":"correct","key":"retry-timeout","summary":"retry timeout","content":"retries time out at ten seconds"}
	]}`)
	if err != nil {
		t.Fatalf("parse correct reply: %v", err)
	}
	for _, op := range corrected.Ops {
		if err := memory.ApplyCaptureOp(store, op, "session-1", deterministicNow); err != nil {
			t.Fatalf("apply correct op %s: %v", op.Key, err)
		}
	}
	fact, err = store.ReadFact("retry-timeout")
	if err != nil {
		t.Fatalf("reread retry-timeout: %v", err)
	}
	if fact.Scene != "coding" {
		t.Fatalf("correct changed scene to %q, want coding kept", fact.Scene)
	}
	if !strings.Contains(fact.Body, "ten seconds") {
		t.Fatalf("correct body = %q, want the rewritten content", fact.Body)
	}

	// An update carrying a scene re-tags the fact, and a scene-only change
	// still lands: UpsertFact's no-change short-circuit must treat a scene
	// difference as a change.
	retag, err := memory.ParseCaptureResult(`{"ops":[
		{"action":"update","key":"brief-answers","summary":"brief answers","content":"the user wants brief answers","scene":"chat"}
	]}`)
	if err != nil {
		t.Fatalf("parse update reply: %v", err)
	}
	for _, op := range retag.Ops {
		if err := memory.ApplyCaptureOp(store, op, "session-1", deterministicNow); err != nil {
			t.Fatalf("apply update op %s: %v", op.Key, err)
		}
	}
	fact, err = store.ReadFact("brief-answers")
	if err != nil {
		t.Fatalf("read retagged fact: %v", err)
	}
	if fact.Scene != "chat" {
		t.Fatalf("update scene = %q, want chat", fact.Scene)
	}

	sceneOnly, err := memory.ParseCaptureResult(`{"ops":[
		{"action":"update","key":"brief-answers","summary":"brief answers","content":"the user wants brief answers","scene":"working"}
	]}`)
	if err != nil {
		t.Fatalf("parse scene-only reply: %v", err)
	}
	for _, op := range sceneOnly.Ops {
		if err := memory.ApplyCaptureOp(store, op, "session-1", deterministicNow); err != nil {
			t.Fatalf("apply scene-only op %s: %v", op.Key, err)
		}
	}
	fact, err = store.ReadFact("brief-answers")
	if err != nil {
		t.Fatalf("read scene-only fact: %v", err)
	}
	if fact.Scene != "working" {
		t.Fatalf("scene-only update left scene %q, want working", fact.Scene)
	}

	// A correct that carries a scene leaves the fact's scene alone.
	correctScene, err := memory.ParseCaptureResult(`{"ops":[
		{"action":"correct","key":"retry-timeout","summary":"retry timeout","content":"retries time out at twenty seconds","scene":"chat"}
	]}`)
	if err != nil {
		t.Fatalf("parse correct-with-scene reply: %v", err)
	}
	for _, op := range correctScene.Ops {
		if err := memory.ApplyCaptureOp(store, op, "session-1", deterministicNow); err != nil {
			t.Fatalf("apply correct-with-scene op %s: %v", op.Key, err)
		}
	}
	fact, err = store.ReadFact("retry-timeout")
	if err != nil {
		t.Fatalf("read corrected-with-scene fact: %v", err)
	}
	if fact.Scene != "coding" {
		t.Fatalf("correct-with-scene changed scene to %q, want coding kept", fact.Scene)
	}
}
