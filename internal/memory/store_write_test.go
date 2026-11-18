// store_write_test.go — the store's atomic write must survive a transient
// handle on its target. A reader, a file watcher, or a virus scanner can
// hold the destination open for a few milliseconds. Windows cannot rename
// onto a held file, so a plain rename loses the save.
//
// Audit record.
// Protected behavior: the memory store's write lands while another process
// holds the target file open.
// Credible regression: a rename that does not retry fails at once with
// "Access is denied" on Windows, and the capture markers keep the value
// they held before the write.
// Coverage gap: the fsatomic tests cover the retry in the helper, but no
// memory test holds the capture state file open while the store writes it.
// Seam check: the test drives Store.WriteCaptureState and
// Store.ReadCaptureState through the exported API, so no test-only export
// was added.

package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

import (
	"os"
	"testing"
	"time"

	"github.com/dkmnx/recollect/internal/memory"
)

// TestStoreWriteRidesOutAHeldDestination holds the capture state file open
// the way a watcher holds it, releases the handle a moment later, and pins
// that the write still lands. Without the retry the write fails at once
// with "Access is denied" and the capture markers stay where they were.
func TestStoreWriteRidesOutAHeldDestination(t *testing.T) {
	store, _, projectDir := newTestStore(t, memory.Limits{})
	if err := store.WriteCaptureState(memory.CaptureState{LastConsolidatedMessage: 1}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	handle, err := os.OpenFile(memory.CaptureStateFile(projectDir), os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open target: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	// The hold clears well inside the retry budget, so the write waits for
	// it rather than failing.
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = handle.Close()
	}()
	if err := store.WriteCaptureState(memory.CaptureState{LastConsolidatedMessage: 2}); err != nil {
		t.Fatalf("write under a held target: %v", err)
	}
	state, err := store.ReadCaptureState()
	if err != nil {
		t.Fatal(err)
	}
	if state.LastConsolidatedMessage != 2 {
		t.Fatalf("marker = %d, want 2", state.LastConsolidatedMessage)
	}
}
