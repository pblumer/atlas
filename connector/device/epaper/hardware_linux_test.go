//go:build linux

package epaper

import (
	"context"
	"image/color"
	"os"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/device"
)

// hardwareEnv names the panel model of a real HAT on this device's header, as
// waveshare-2in13-v2, for the one test that draws on real glass. Unset, the test is
// skipped, so no CI machine runs it. It needs SPI enabled (/dev/spidev0.0) and the
// HAT's lines (17, 18, 24, 25) free.
const hardwareEnv = "ATLAS_EPAPER_HW"

// TestHardwareDrawsOnARealPanel shows a picture that answers three questions at a
// glance: does the panel come up at all (anything appears), is the orientation right
// (the filled square is top right, the text reads left to right), and do Umlauts
// render (Grüezi).
func TestHardwareDrawsOnARealPanel(t *testing.T) {
	model := os.Getenv(hardwareEnv)
	if model == "" {
		t.Skipf("%s is not set; set it to a panel model (%v) on a device with the HAT", hardwareEnv, Models())
	}
	panel, err := Lookup(model)
	if err != nil {
		t.Fatal(err)
	}
	chip, err := device.OpenChip("gpiochip0")
	if err != nil {
		t.Fatal(err)
	}
	spi, err := OpenSPI("spidev0.0")
	if err != nil {
		chip.Close()
		t.Fatal(err)
	}
	hat, err := OpenHAT(chip, spi, WaveshareHAT, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := New(panel, hat)
	defer d.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	start := time.Now()
	if err := d.Init(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("init took %s", time.Since(start).Round(time.Millisecond))

	f := NewFrame()
	f.Rect(f.Bounds())
	for x := Width - 18; x < Width-4; x++ {
		for y := 4; y < 18; y++ {
			f.Set(x, y, color.Black)
		}
	}
	lines := []struct {
		y    int
		size float64
		bold bool
		text string
	}{
		{32, 24, true, "Grüezi aus Atlas"},
		{58, 15, false, "Pi Zero 2 W · e-Paper 2.13\""},
		{80, 15, false, "Panel: " + model},
		{104, 15, false, "Stand: " + time.Now().Format("02.01.2006 15:04")},
	}
	for _, l := range lines {
		if _, err := f.Text(10, l.y, l.size, l.bold, l.text); err != nil {
			t.Fatal(err)
		}
	}

	start = time.Now()
	if err := d.Show(ctx, f); err != nil {
		t.Fatal(err)
	}
	t.Logf("refresh took %s", time.Since(start).Round(time.Millisecond))
	if err := d.Sleep(); err != nil {
		t.Fatal(err)
	}
}
