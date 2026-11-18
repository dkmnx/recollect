// Package oslock provides advisory locks that the operating system releases
// when a process exits. A lock file is created on demand.
package oslock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrBusy reports that the lock stayed held for the whole timeout. Callers
// can retry a busy lock later.
var ErrBusy = errors.New("oslock: busy")

const retryInterval = 10 * time.Millisecond

// Lock takes an exclusive advisory lock on path. The returned function
// releases it; the operating system also releases it on process exit.
func Lock(path string, timeout time.Duration) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("oslock: create lock directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("oslock: open %s: %w", path, err)
	}
	deadline := time.Now().Add(timeout)
	for {
		err := lockFile(file)
		if err == nil {
			return func() {
				_ = unlockFile(file)
				_ = file.Close()
			}, nil
		}
		if !isBusy(err) {
			_ = file.Close()
			return nil, fmt.Errorf("oslock: lock %s: %w", path, err)
		}
		if !time.Now().Before(deadline) {
			_ = file.Close()
			return nil, fmt.Errorf("%w: %s", ErrBusy, path)
		}
		time.Sleep(retryInterval)
	}
}
