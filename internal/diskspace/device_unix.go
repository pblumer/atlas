//go:build linux || darwin || freebsd

package diskspace

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// deviceOf names the device dir lives on: two directories on one file system share
// it, and a directory with another file system mounted on it does not.
func deviceOf(dir string) (string, error) {
	var st unix.Stat_t
	if err := unix.Stat(dir, &st); err != nil {
		return "", fmt.Errorf("diskspace: stat %s: %w", dir, err)
	}
	return fmt.Sprintf("dev:%d", st.Dev), nil
}
