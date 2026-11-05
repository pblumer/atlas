//go:build linux

package device

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// OpenChip opens /dev/<name>, a GPIO character device. Only a name the configuration
// accepted reaches here (gpiochipN), so no other file can be opened through it.
func OpenChip(name string) (Chip, error) {
	if !chipPattern.MatchString(name) {
		return nil, fmt.Errorf("device: chip %q is not a GPIO character device name", name)
	}
	fd, err := unix.Open("/dev/"+name, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("device: open /dev/%s: %w (is the account in the gpio group?)", name, err)
	}
	return &cdevChip{fd: fd, name: name}, nil
}

type cdevChip struct {
	fd   int
	name string
}

func ioctl(fd int, req uintptr, buf []byte) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, uintptr(unsafe.Pointer(&buf[0])))
	runtime.KeepAlive(buf)
	if errno != 0 {
		return errno
	}
	return nil
}

func (c *cdevChip) request(line int, flags uint64, output, initial bool, consumer string) (int, error) {
	buf := encodeLineRequest(line, flags, output, initial, consumer)
	if err := ioctl(c.fd, gpioV2GetLineIoctl, buf); err != nil {
		return -1, fmt.Errorf("device: %s line %d: %w", c.name, line, err)
	}
	return decodeFD(buf), nil
}

func (c *cdevChip) RequestOutput(line int, initial bool, consumer string) (OutputLine, error) {
	fd, err := c.request(line, gpioV2LineFlagOutput, true, initial, consumer)
	if err != nil {
		return nil, err
	}
	return cdevLine{fd: fd}, nil
}

func (c *cdevChip) RequestInput(line int, bias Bias, consumer string) (InputLine, error) {
	fd, err := c.request(line, inputFlags(bias), false, false, consumer)
	if err != nil {
		return nil, err
	}
	return cdevLine{fd: fd}, nil
}

func (c *cdevChip) Close() error { return unix.Close(c.fd) }

type cdevLine struct{ fd int }

func (l cdevLine) Set(high bool) error {
	return ioctl(l.fd, gpioV2LineSetValuesIoctl, encodeValues(high))
}

func (l cdevLine) Value() (bool, error) {
	buf := encodeValues(false)
	if err := ioctl(l.fd, gpioV2LineGetValuesIoctl, buf); err != nil {
		return false, err
	}
	return decodeValue(buf), nil
}

func (l cdevLine) Close() error { return unix.Close(l.fd) }
