//go:build linux

package diskspace

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// Of reads the file system holding dir.
//
// Linux counts blocks in fragment-size units (f_frsize), which is not always the
// optimal transfer size in f_bsize; the C library's statvfs and df use the fragment
// size, and so does this.
func Of(dir string) (Usage, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return Usage{}, fmt.Errorf("diskspace: statfs %s: %w", dir, err)
	}
	size := uint64(st.Frsize)
	if size == 0 {
		size = uint64(st.Bsize)
	}
	volume, err := deviceOf(dir)
	if err != nil {
		return Usage{}, err
	}
	return fromBlocks(size, st.Blocks, st.Bfree, nonNegative(st.Bavail), volume), nil
}
