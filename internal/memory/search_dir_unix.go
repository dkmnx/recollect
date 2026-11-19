//go:build !windows

package memory

import (
	"errors"
	"os"
)

func createPrivateDir(path string) error {
	return os.MkdirAll(path, 0o700)
}

func validatePrivateDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("search cache path is not a plain directory")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("search cache directory is accessible to other users")
	}
	return nil
}
