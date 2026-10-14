//go:build !windows

package ownerfile

import (
	"fmt"
	"os"
)

// Restrict makes path readable and writable by its owner only.
func Restrict(path string) error {
	return os.Chmod(path, 0o600)
}

// Check reports an error unless path is readable and writable by its owner only.
func Check(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		return fmt.Errorf("ownerfile: %s has mode %o, want 600", path, perm)
	}
	return nil
}
