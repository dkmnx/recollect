package memory_test

// audit: behavior capture parsing preserves an unknown fact type instead of
// mapping it to project; code fences, op validation and redaction hold.
// audit: regression NormalizeFactType collapsing "environment" to project
// loses the writer's type before a capture result reaches apply.
// audit: coverage gap the original case asserted legacy-to-project mapping,
// so no parse test saw an unknown type survive normalization.
// audit: seam none; tests drive ParseCaptureResult and Redact directly.

import (
	"github.com/dkmnx/recollect/internal/memory"
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	got := memory.Redact("key is AKIAIOSFODNN7EXAMPLE and password = hunter2hunter2hunter2")
	if strings.Contains(got, "AKIAIOSFODNN7EXAMPLE") || strings.Contains(got, "hunter2hunter2hunter2") {
		t.Fatalf("secret survived redaction: %q", got)
	}
	if !strings.Contains(got, memory.RedactedPlaceholder) {
		t.Fatalf("placeholder missing: %q", got)
	}
	clean := "make check runs vet and the race suite"
	if memory.Redact(clean) != clean {
		t.Fatalf("clean text changed: %q", memory.Redact(clean))
	}
}

func TestParseCaptureResult(t *testing.T) {
	raw := "```json\n" +
		`{"ops":[{"action":"add","type":"environment","key":"build-check","summary":"make check is the gate","content":"Runs vet and race."},` +
		`{"action":"correct","key":"stream-retry","summary":"retries are two layers","content":"HTTP plus session."},` +
		`{"action":"bogus","summary":"drop me"},` +
		`{"action":"add","summary":"   ","content":"drop me too"}],"reason":""}` +
		"\n```"
	result, err := memory.ParseCaptureResult(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Ops) != 2 {
		t.Fatalf("ops = %+v", result.Ops)
	}
	if result.Ops[0].Key != "build-check" || result.Ops[0].Type != "environment" || result.Ops[0].Content != "Runs vet and race." {
		t.Errorf("op 0 = %+v", result.Ops[0])
	}
	if result.Ops[1].Action != "correct" || result.Ops[1].Key != "stream-retry" {
		t.Errorf("op 1 = %+v", result.Ops[1])
	}
}

func TestParseCaptureResultRejectsGarbage(t *testing.T) {
	if _, err := memory.ParseCaptureResult("no json here"); err == nil {
		t.Fatal("expected an error for a reply without JSON")
	}
	if _, err := memory.ParseCaptureResult(`{"ops": }`); err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
}

func TestParseCaptureDigest(t *testing.T) {
	topic, summary, err := memory.ParseCaptureDigest(`{"topic":"stream retry","summary":"Landed the fix. Next: watch flakes."}`)
	if err != nil || topic != "stream retry" || !strings.Contains(summary, "watch flakes") {
		t.Fatalf("digest = %q %q %v", topic, summary, err)
	}
	if _, _, err := memory.ParseCaptureDigest(`{"topic":"x"}`); err == nil {
		t.Fatal("expected an error for an empty digest summary")
	}
}
