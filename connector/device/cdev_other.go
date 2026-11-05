//go:build !linux

package device

import (
	"fmt"
	"runtime"
)

// OpenChip refuses: the GPIO character device is Linux's. A device Worker is
// served on a Linux device, or with the fake chip of its mock mode anywhere.
func OpenChip(name string) (Chip, error) {
	return nil, fmt.Errorf("device: chip %q: GPIO needs Linux, and this is %s", name, runtime.GOOS)
}
