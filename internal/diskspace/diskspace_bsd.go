//go:build darwin || freebsd

package diskspace

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// Of reads the file system holding dir. On macOS and FreeBSD the block counts are in
// units of f_bsize, the file system's fundamental block size.
func Of(dir string) (Usage, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return Usage{}, fmt.Errorf("diskspace: statfs %s: %w", dir, err)
	}
	volume, err := deviceOf(dir)
	if err != nil {
		return Usage{}, err
	}
	return fromBlocks(uint64(st.Bsize), st.Blocks, st.Bfree, nonNegative(st.Bavail), volume), nil
}
