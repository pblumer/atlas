//go:build linux

package epaper

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSpidevIoctlNumbersAreTheKernels(t *testing.T) {
	for name, tc := range map[string]struct{ got, want uint }{
		"SPI_IOC_WR_MODE":          {spiIocWrMode, 0x40016B01},
		"SPI_IOC_WR_BITS_PER_WORD": {spiIocWrBitsPerWord, 0x40016B03},
		"SPI_IOC_WR_MAX_SPEED_HZ":  {spiIocWrMaxSpeedHz, 0x40046B04},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %#x, want %#x", name, tc.got, tc.want)
		}
	}
}

func TestOpenSPIRefusesAnythingButAnSpidevName(t *testing.T) {
	for _, bad := range []string{"../mem", "spidev", "spidev0.0/x"} {
		if _, err := OpenSPI(bad); err == nil || !strings.Contains(err.Error(), "not an spidev") {
			t.Errorf("OpenSPI(%q) = %v", bad, err)
		}
	}
	if _, err := OpenSPI("spidev9.9"); err == nil || !strings.Contains(err.Error(), "SPI enabled") {
		t.Fatalf("a missing bus: %v", err)
	}
}

// /dev/null opens but takes no spidev ioctl: the configuration reaches the kernel and
// is refused, and the file is not handed out half configured.
func TestABusThatCannotBeConfiguredIsRefused(t *testing.T) {
	if _, err := openSPI("/dev/null"); !errors.Is(err, unix.ENOTTY) || !strings.Contains(err.Error(), "configure") {
		t.Fatalf("err = %v", err)
	}
}
