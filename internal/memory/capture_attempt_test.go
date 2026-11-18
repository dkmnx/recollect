package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

import (
	"recollect/internal/memory"
	"testing"
	"time"
)

// TestCaptureAttemptFileKeepsBothPhases pins the storage contract the
// diagnosis depends on: each phase owns a slot, so recording the digest
// after a rejected consolidation must not erase the rejection, and a store
// with no record reads as "no pass has run" instead of failing.
func TestCaptureAttemptFileKeepsBothPhases(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	missing, err := store.ReadCaptureAttempts()
	if err != nil {
		t.Fatalf("a store with no record must not fail: %v", err)
	}
	if !missing.Typed.At.IsZero() || !missing.Digest.At.IsZero() {
		t.Fatalf("missing record = %+v", missing)
	}

	stamp := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	typed := memory.CaptureAttempt{Phase: memory.CapturePhaseTyped, At: stamp, Attempts: 2, Bytes: 120, StopReason: "stop", Err: "MEMORY_IO: not valid JSON"}
	if err := store.WriteCaptureAttempt(memory.CapturePhaseTyped, typed); err != nil {
		t.Fatal(err)
	}
	digest := memory.CaptureAttempt{Phase: memory.CapturePhaseDigest, At: stamp.Add(time.Minute), Attempts: 1, Bytes: 320, StopReason: "stop"}
	if err := store.WriteCaptureAttempt(memory.CapturePhaseDigest, digest); err != nil {
		t.Fatal(err)
	}
	// A later pass that only ran one phase must not blank the other.
	if err := store.WriteCaptureAttempt(memory.CapturePhaseDigest, memory.CaptureAttempt{
		Phase: memory.CapturePhaseDigest, At: stamp.Add(2 * time.Minute), Attempts: 1, Bytes: 88, StopReason: "length", Truncated: true,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := store.ReadCaptureAttempts()
	if err != nil {
		t.Fatal(err)
	}
	if got.Typed != typed {
		t.Fatalf("typed record = %+v, want the recorded rejection intact", got.Typed)
	}
	if !got.Digest.Truncated || got.Digest.Bytes != 88 || !got.Digest.At.Equal(stamp.Add(2*time.Minute)) {
		t.Fatalf("digest record = %+v, want the later pass", got.Digest)
	}
	if got.Phase(memory.CapturePhaseTyped) != typed || got.Phase(memory.CapturePhaseDigest) != got.Digest {
		t.Fatalf("Phase lookup = %+v / %+v", got.Phase(memory.CapturePhaseTyped), got.Phase(memory.CapturePhaseDigest))
	}
}
