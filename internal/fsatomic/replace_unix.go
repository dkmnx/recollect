//go:build !windows

package fsatomic

import "os"

// Replace renames src onto dst, replacing dst if it exists. Unix rename is
// atomic and ignores open handles on dst, so there is nothing to retry.
func Replace(src, dst string) error {
	return os.Rename(src, dst)
}
