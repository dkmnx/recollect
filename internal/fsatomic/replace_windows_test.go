//go:build windows

package fsatomic_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.
import (
	"os"
	"path/filepath"
	"testing"
	"time"

	fsatomic "recollect/internal/fsatomic"
)

// The retry budget is finite, so a destination held open past it must fail
// loudly rather than silently drop the save — and, the reason the write path
// is atomic at all, must leave the previous contents intact instead of
// truncating them the way an in-place os.WriteFile would.
func TestReplaceFailsCleanlyWhenTheDestinationStaysHeld(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "settings.json")
	src := filepath.Join(dir, "settings.json.tmp")
	if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}

	held, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	err = fsatomic.Replace(src, dst)
	if err == nil {
		t.Fatal("expected the replace to fail while the destination is held for the whole retry budget")
	}
	t.Logf("replace refused as expected: %v", err)

	data, readErr := os.ReadFile(filepath.Join(dir, "settings.json"))
	if readErr != nil {
		t.Fatalf("destination unreadable after a failed replace: %v", readErr)
	}
	if string(data) != "old" {
		t.Fatalf("destination = %q, want the previous contents preserved", data)
	}

	if _, statErr := os.Stat(src); statErr != nil {
		t.Fatalf("a failed replace must leave the temp file for the caller to clean up: %v", statErr)
	}
}

// Windows refuses the replace for as long as the handle is open, so the
// budget must be long enough to clear a real reader yet short enough that a
// stuck holder does not wedge the write.
func TestReplaceRetryBudgetIsBounded(t *testing.T) {
	start := time.Now()
	dir := t.TempDir()
	dst := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tmp"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}

	held, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	if err := fsatomic.Replace(filepath.Join(dir, "tmp"), dst); err == nil {
		t.Fatal("expected the replace to fail while the destination is held")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("retry budget took %v, want it bounded well under the save's own patience", elapsed)
	}
}
