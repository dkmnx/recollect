package oslock_test

// audit: behavior - An advisory lock excludes a second holder and frees on release.
// audit: regression - the lock path is created on demand and the release is repeatable safe.
// audit: coverage gap - cross-handle exclusion and bounded busy timeout were not covered.
// audit: seam - the test calls only the public Lock API on a temp-directory file.
import (
	"errors"
	"os"
	"path/filepath"
	"recollect/internal/oslock"
	"testing"
	"time"
)

func TestLockExcludesSecondHolderUntilRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	release, err := oslock.Lock(path, 2*time.Second)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lock file was not created: %v", err)
	}
	if _, err := oslock.Lock(path, 50*time.Millisecond); !errors.Is(err, oslock.ErrBusy) {
		t.Fatalf("second lock: got %v, want busy", err)
	}
	release()
	second, err := oslock.Lock(path, 2*time.Second)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	second()
}
