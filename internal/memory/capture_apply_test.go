package memory_test

// audit: behavior capture apply preserves unknown fact types instead of
// relabeling them to project; the five-type vocabulary and all actions hold.
// audit: regression a writer-side NormalizeFactType collapsing "fact" or
// "environment" to project silently changes the type of an existing fact.
// audit: coverage gap the original cases asserted legacy-to-project mapping,
// so no test saw an unknown type survive parse and apply.
// audit: seam none; tests drive ParseCaptureResult and ApplyCaptureOp.

import (
	"recollect/internal/memory"
	"strings"
	"testing"
	"time"
)

func TestApplyCaptureOpToleratesDuplicateBody(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	op := memory.CaptureOp{Action: "add", Key: "build-cmd", Summary: "Build uses make check", Content: "Run make check.", Type: "project"}
	if err := memory.ApplyCaptureOp(store, op, "sess", time.Time{}); err != nil {
		t.Fatal(err)
	}
	alias := memory.CaptureOp{Action: "add", Key: "build-cmd-alias", Summary: "Another name", Content: "Run make check."}
	if err := memory.ApplyCaptureOp(store, alias, "sess", time.Time{}); err != nil {
		t.Fatalf("duplicate add should succeed as a no-op: %v", err)
	}
	if _, err := store.ReadFact("build-cmd-alias"); err == nil {
		t.Fatal("duplicate add wrote a second fact")
	}
}

func TestApplyCaptureOpStampsObservedDate(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	observed := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	op := memory.CaptureOp{Action: "add", Key: "dated", Summary: "dated fact", Content: "observed content"}
	if err := memory.ApplyCaptureOp(store, op, "sess", observed); err != nil {
		t.Fatal(err)
	}
	if fact, _ := store.ReadFact("dated"); fact.CapturedOn != "2026-10-01" {
		t.Fatalf("capturedOn = %q, want the observation date", fact.CapturedOn)
	}
	undated := memory.CaptureOp{Action: "add", Key: "undated", Summary: "undated fact", Content: "other content"}
	if err := memory.ApplyCaptureOp(store, undated, "sess", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if fact, _ := store.ReadFact("undated"); fact.CapturedOn != "2026-09-23" {
		t.Fatalf("capturedOn = %q, want the store clock date", fact.CapturedOn)
	}
}

func TestApplyCaptureOps(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	const session = "sess_capture"

	add := memory.CaptureOp{Action: "add", Key: "build-cmd", Summary: "Build uses make check", Content: "Run make check.", Type: "fact"}
	if err := memory.ApplyCaptureOp(store, add, session, time.Time{}); err != nil {
		t.Fatal(err)
	}
	fact, err := store.ReadFact("build-cmd")
	if err != nil {
		t.Fatal(err)
	}
	if fact.Type != "fact" || fact.OriginSessionID != session {
		t.Fatalf("add = %+v", fact)
	}

	update := memory.CaptureOp{Action: "update", Key: "build-cmd", Summary: "Build uses make check", Content: "Run make check first."}
	if err := memory.ApplyCaptureOp(store, update, session, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if fact, _ = store.ReadFact("build-cmd"); fact.Body != "Run make check first." {
		t.Fatalf("update = %+v", fact)
	}

	correct := memory.CaptureOp{Action: "correct", Key: "build-cmd", Summary: "Build is make check, not make build"}
	if err := memory.ApplyCaptureOp(store, correct, session, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if fact, _ = store.ReadFact("build-cmd"); fact.Description != "Build is make check, not make build" {
		t.Fatalf("correct = %+v", fact)
	}

	demote := memory.CaptureOp{Action: "demote", Key: "build-cmd"}
	if err := memory.ApplyCaptureOp(store, demote, session, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if fact, _ = store.ReadFact("build-cmd"); fact.Path != "topics/"+memory.CaptureOverflowTopic {
		t.Fatalf("demote = %+v", fact)
	}

	drop := memory.CaptureOp{Action: "drop", Key: "build-cmd"}
	if err := memory.ApplyCaptureOp(store, drop, session, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadFact("build-cmd"); memory.CodeOf(err) != memory.CodeFactNotFound {
		t.Fatalf("drop left the fact: %v", err)
	}
	// A retried drop converges.
	if err := memory.ApplyCaptureOp(store, drop, session, time.Time{}); err != nil {
		t.Fatalf("retried drop = %v", err)
	}
}

func TestApplyCaptureOpUserType(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	op := memory.CaptureOp{Action: "add", Key: "pref", Summary: "Prefers terse answers", Type: "feedback"}
	if err := memory.ApplyCaptureOp(store, op, "", time.Time{}); err != nil {
		t.Fatal(err)
	}
	fact, err := store.ReadFact("pref")
	if err != nil || fact.Type != memory.FactTypeFeedback {
		t.Fatalf("fact = %+v (%v)", fact, err)
	}
}

func TestCaptureOpsFromPromptShape(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	result, err := memory.ParseCaptureResult(`Sure! {"ops":[{"action":"add","type":"environment","key":"Go Version","summary":"Go 1.24","content":"toolchain go1.24"}],"reason":"env"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Ops) != 1 || result.Ops[0].Key != "go-version" {
		t.Fatalf("ops = %+v", result.Ops)
	}
	if err := memory.ApplyCaptureOp(store, result.Ops[0], "s", time.Time{}); err != nil {
		t.Fatal(err)
	}
	fact, err := store.ReadFact("go-version")
	if err != nil || fact.Type != "environment" {
		t.Fatalf("unknown type should be preserved: %+v (%v)", fact, err)
	}
}

func TestCapturePromptMentionsFactNames(t *testing.T) {
	if !strings.Contains(memory.CaptureConsolidationPrompt, `"type":"project|user|feedback|reference|experience"`) {
		t.Fatal("consolidation prompt does not carry the shared type vocabulary")
	}
	if !strings.Contains(memory.CaptureConsolidationPrompt, "capture-overflow") {
		t.Fatal("consolidation prompt does not name the overflow topic")
	}
}

func TestApplyCaptureOpCorrectSetsLinks(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	if err := memory.ApplyCaptureOp(store, memory.CaptureOp{Action: "add", Key: "contradicted", Summary: "old claim", Content: "old"}, "sess", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := memory.ApplyCaptureOp(store, memory.CaptureOp{Action: "add", Key: "newer", Summary: "new claim", Content: "new"}, "sess", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := memory.ApplyCaptureOp(store, memory.CaptureOp{Action: "correct", Key: "contradicted", Summary: "superseded claim", Links: []string{"newer"}}, "sess", time.Time{}); err != nil {
		t.Fatal(err)
	}
	fact, err := store.ReadFact("contradicted")
	if err != nil {
		t.Fatal(err)
	}
	if len(fact.Links) != 1 || fact.Links[0] != "newer" {
		t.Fatalf("correct dropped links: %v", fact.Links)
	}
	if err := memory.ApplyCaptureOp(store, memory.CaptureOp{Action: "correct", Key: "contradicted", Summary: "no links this time"}, "sess", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if fact, _ = store.ReadFact("contradicted"); len(fact.Links) != 1 {
		t.Fatalf("linkless correct clobbered links: %v", fact.Links)
	}
}
