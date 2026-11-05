//go:build linux

package epaper

import (
	"fmt"
	"os"
	"regexp"

	"golang.org/x/sys/unix"
)

// spidev's ioctls (linux/spi/spidev.h): _IOW('k', nr, size) is
// 1<<30 | size<<16 | 'k'<<8 | nr on every architecture a Raspberry Pi runs.
func iow(nr, size uint) uint { return 1<<30 | size<<16 | 0x6B<<8 | nr }

var (
	spiIocWrMode        = iow(1, 1) // __u8
	spiIocWrBitsPerWord = iow(3, 1) // __u8
	spiIocWrMaxSpeedHz  = iow(4, 4) // __u32
)

// spiSpeed is the clock Waveshare's driver runs the panel at.
const spiSpeed = 4_000_000

var spidevPattern = regexp.MustCompile(`^spidev[0-9]\.[0-9]$`)

// OpenSPI opens /dev/<name> (spidev0.0 for CE0) in mode 0, 8 bits a word, at 4 MHz.
func OpenSPI(name string) (SPI, error) {
	if !spidevPattern.MatchString(name) {
		return nil, fmt.Errorf("epaper: %q is not an spidev name such as spidev0.0", name)
	}
	return openSPI("/dev/" + name)
}

// openSPI opens and configures a bus by path. IoctlSetPointerInt hands the kernel an
// int32; the mode and word size are single bytes, which on a little-endian Pi are
// that int32's first byte.
func openSPI(path string) (SPI, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("epaper: open %s: %w (is SPI enabled, and the account in the spi group?)", path, err)
	}
	fd := int(f.Fd())
	for _, set := range []struct {
		req   uint
		value int
	}{{spiIocWrMode, 0}, {spiIocWrBitsPerWord, 8}, {spiIocWrMaxSpeedHz, spiSpeed}} {
		if err := unix.IoctlSetPointerInt(fd, set.req, set.value); err != nil {
			f.Close()
			return nil, fmt.Errorf("epaper: configure %s: %w", path, err)
		}
	}
	return f, nil
}
