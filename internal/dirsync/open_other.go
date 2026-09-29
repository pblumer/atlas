//go:build !windows

package dirsync

import "os"

// Open opens dir read-only, which is all fsync on a directory needs outside
// Windows.
func Open(dir string) (*os.File, error) {
	return os.Open(dir)
}
