//go:build !linux

package device

import (
	"strings"
	"testing"
)

func TestOpenChipNeedsLinux(t *testing.T) {
	if _, err := OpenChip("gpiochip0"); err == nil || !strings.Contains(err.Error(), "needs Linux") {
		t.Fatalf("err = %v", err)
	}
}
