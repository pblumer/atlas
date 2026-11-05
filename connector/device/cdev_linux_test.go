//go:build linux

package device

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestOpenChipRefusesAnythingButAChipName(t *testing.T) {
	if _, err := OpenChip("../mem"); err == nil || !strings.Contains(err.Error(), "not a GPIO") {
		t.Fatalf("err = %v", err)
	}
}

// No test machine has a GPIO chip, so the one thing that can be shown here is that a
// chip that is not there is reported with its path and the usual cause.
func TestOpenChipReportsAMissingDevice(t *testing.T) {
	_, err := OpenChip("gpiochip999")
	if err == nil || !strings.Contains(err.Error(), "/dev/gpiochip999") || !strings.Contains(err.Error(), "gpio group") {
		t.Fatalf("err = %v", err)
	}
}

// /dev/null takes no GPIO ioctl: every request made on it reaches the kernel through
// the real system call and comes back refused, which is as far as a machine without a
// chip can follow the path.
func TestRequestsOnAFileThatIsNoChipAreRefusedByTheKernel(t *testing.T) {
	fd, err := unix.Open("/dev/null", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Skip("no /dev/null:", err)
	}
	chip := &cdevChip{fd: fd, name: "gpiochip0"}
	if _, err := chip.RequestOutput(17, true, "atlas"); !errors.Is(err, unix.ENOTTY) || !strings.Contains(err.Error(), "line 17") {
		t.Errorf("RequestOutput = %v", err)
	}
	if _, err := chip.RequestInput(27, BiasPullUp, "atlas"); !errors.Is(err, unix.ENOTTY) {
		t.Errorf("RequestInput = %v", err)
	}
	line := cdevLine{fd: fd}
	if err := line.Set(true); !errors.Is(err, unix.ENOTTY) {
		t.Errorf("Set = %v", err)
	}
	if _, err := line.Value(); !errors.Is(err, unix.ENOTTY) {
		t.Errorf("Value = %v", err)
	}
	dup, err := unix.Dup(fd)
	if err != nil {
		t.Fatal(err)
	}
	if err := (cdevLine{fd: dup}).Close(); err != nil {
		t.Errorf("line Close = %v", err)
	}
	if err := chip.Close(); err != nil {
		t.Errorf("chip Close = %v", err)
	}
}
