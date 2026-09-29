//go:build !windows

package sharedread

import "os"

// Open opens name for reading. Outside Windows an open file never stands in the way
// of a delete or a rename, so this is os.Open.
func Open(name string) (*os.File, error) {
	return os.Open(name)
}
