//go:build windows

package fsatomic

import (
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// Windows cannot rename onto a file another process holds open without
// FILE_SHARE_DELETE, so every atomic write races the readers of its target:
// a second xi session, the resources populate tool, an editor's file watch.
// The rename fails with ERROR_ACCESS_DENIED or ERROR_SHARING_VIOLATION and
// the save is lost entirely — settings silently keep their old values.
//
// Retry instead. A holder clears in milliseconds — an editor's file watch, a
// virus scanner walking the temp file just written — so sample the window often
// rather than backing off: the same 900 ms spent as 36 quick retries wins far
// more often than 9 long ones. A genuine permission problem still surfaces,
// just after the budget rather than instantly.
const (
	replaceAttempts = 36
	replaceInterval = 25 * time.Millisecond
)

// Replace renames src onto dst, replacing dst if it exists, retrying while
// Windows reports the destination as busy.
func Replace(src, dst string) error {
	var err error
	for attempt := 0; attempt < replaceAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(replaceInterval)
		}
		if err = windows.MoveFileEx(windows.StringToUTF16Ptr(src), windows.StringToUTF16Ptr(dst),
			windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err == nil {
			return nil
		}
		if !destinationBusy(err) {
			return renameError(src, dst, err)
		}
	}
	return renameError(src, dst, err)
}

// destinationBusy reports whether err is the transient "someone else holds
// the destination open" refusal rather than a real failure to replace it.
func destinationBusy(err error) bool {
	if errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return true
	}
	// A path component that a reader holds open (a replaced parent, or a
	// directory another process is working in) reports the same two codes
	// through the wrapped LinkError.
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return errors.Is(linkErr.Err, windows.ERROR_SHARING_VIOLATION) ||
			errors.Is(linkErr.Err, windows.ERROR_ACCESS_DENIED)
	}
	return false
}

func renameError(src, dst string, err error) error {
	return &os.LinkError{Op: "rename", Old: src, New: dst, Err: fmt.Errorf("replace: %w", err)}
}
