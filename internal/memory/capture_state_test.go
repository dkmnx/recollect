package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

import (
	"recollect/internal/memory"
	"testing"
	"time"
)

func TestCaptureStateRoundTrip(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	zero, err := store.ReadCaptureState()
	if err != nil || zero.LastConsolidatedMessage != 0 || !zero.LastTypedAt.IsZero() {
		t.Fatalf("missing state = %+v (%v)", zero, err)
	}
	want := memory.CaptureState{
		LastConsolidatedMessage: 12,
		LastDigestMessage:       7,
		LastDigestAt:            time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC),
		LastTypedAt:             time.Date(2026, 9, 19, 10, 5, 0, 0, time.UTC),
	}
	if err := store.WriteCaptureState(want); err != nil {
		t.Fatal(err)
	}
	got, err := store.ReadCaptureState()
	if err != nil {
		t.Fatal(err)
	}
	if got.LastConsolidatedMessage != want.LastConsolidatedMessage ||
		got.LastDigestMessage != want.LastDigestMessage ||
		!got.LastDigestAt.Equal(want.LastDigestAt) ||
		!got.LastTypedAt.Equal(want.LastTypedAt) {
		t.Fatalf("round trip = %+v", got)
	}
}
