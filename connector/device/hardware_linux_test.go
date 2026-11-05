//go:build linux

package device

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// hardwareEnv names a chip and a line on a real device, as gpiochip0:26, for the one
// test that drives a real pin. The line must have nothing wired to it: the test
// drives it high and low. Unset, the test is skipped, so no CI machine runs it.
const hardwareEnv = "ATLAS_DEVICE_HW"

func hardwareLine(t *testing.T) (string, int) {
	t.Helper()
	spec := os.Getenv(hardwareEnv)
	if spec == "" {
		t.Skipf("%s is not set; set it to chip:line of a free pin, e.g. gpiochip0:26, on a device", hardwareEnv)
	}
	name, raw, ok := strings.Cut(spec, ":")
	line, err := strconv.Atoi(raw)
	if !ok || err != nil {
		t.Fatalf("%s=%q: want chip:line, e.g. gpiochip0:26", hardwareEnv, spec)
	}
	return name, line
}

// settle gives a pull resistor time to move an open line. A real pin needs well under
// a millisecond; this is the one place in the package that waits on the wall clock,
// and it runs only on a device.
func settle() { time.Sleep(10 * time.Millisecond) }

// TestHardwareLineOnARealChip walks the character-device path the other tests can only
// reach as far as a refusal: claiming a line, its bias, the initial level of an
// output, setting and reading values, the kernel's exclusivity, and a Device on top.
func TestHardwareLineOnARealChip(t *testing.T) {
	name, line := hardwareLine(t)
	chip, err := OpenChip(name)
	if err != nil {
		t.Fatal(err)
	}
	defer chip.Close()

	for _, tc := range []struct {
		bias Bias
		want bool
	}{{BiasPullUp, true}, {BiasPullDown, false}} {
		in, err := chip.RequestInput(line, tc.bias, "atlas-test")
		if err != nil {
			t.Fatalf("input with bias %d: %v", tc.bias, err)
		}
		settle()
		got, err := in.Value()
		in.Close()
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("an open line with bias %d reads %v, want %v (is something wired to line %d?)", tc.bias, got, tc.want, line)
		}
	}

	out, err := chip.RequestOutput(line, true, "atlas-test")
	if err != nil {
		t.Fatal(err)
	}
	read := func() bool {
		t.Helper()
		v, err := out.(cdevLine).Value()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if !read() {
		t.Error("an output claimed high does not read high")
	}
	if _, err := chip.RequestOutput(line, false, "atlas-test"); !errors.Is(err, unix.EBUSY) {
		t.Errorf("a second claim of a held line = %v, want EBUSY", err)
	}
	for _, level := range []bool{false, true, false} {
		if err := out.Set(level); err != nil {
			t.Fatal(err)
		}
		if read() != level {
			t.Errorf("set %v, read %v", level, !level)
		}
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}

	// A Device on the same chip: claimed in its safe state, driven, and released.
	chip2, err := OpenChip(name)
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(Config{Chip: name, Outputs: []Output{{Name: "test", Line: line, ActiveLow: true}}}, chip2, SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Write(context.Background(), "test", true); err != nil {
		t.Fatal(err)
	}
	if on, _ := d.Read("test"); !on {
		t.Error("the device does not read its output as on")
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := chip.RequestInput(line, BiasNone, "atlas-test")
	if err != nil {
		t.Fatalf("the line was not released by Close: %v", err)
	}
	again.Close()
}
