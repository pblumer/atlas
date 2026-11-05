package epaper

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pblumer/atlas/connector/device"
)

// Pins are the header lines a panel's HAT uses besides the SPI bus, by BCM number.
type Pins struct {
	Reset, DC, Busy, Power int
}

// WaveshareHAT is the wiring of Waveshare's e-Paper HATs (epdconfig.py): reset on 17,
// data/command on 25, busy on 24 and, on the boards that have it, the panel's power
// switch on 18. Chip select is the SPI bus's own CE0 (8).
var WaveshareHAT = Pins{Reset: 17, DC: 25, Busy: 24, Power: 18}

// SPI is a bus that writes bytes with chip select held for each write.
type SPI interface {
	Write(p []byte) (int, error)
	Close() error
}

// maxTransfer is the most one write to spidev may carry: the kernel's default
// bufsiz. A picture (4000 bytes) fits; the bound keeps a larger one correct.
const maxTransfer = 4096

// busyTimeout bounds a wait on the busy line. A full refresh takes seconds; a panel
// that is busy for longer is not answering — no SPI, the wrong panel, no power.
const busyTimeout = 20 * time.Second

// busyPoll is how often the busy line is read while waiting.
const busyPoll = 10 * time.Millisecond

// HAT is the Link of a panel on a Pi's header: its control lines claimed on a GPIO
// chip, its bus on spidev.
type HAT struct {
	rst, dc, pwr device.OutputLine
	busy         device.InputLine
	spi          SPI
	chip         device.Chip
	pause        func(time.Duration)
}

// OpenHAT claims the HAT's lines on chip — reset and data/command low, power on, busy
// as an input with the pull-down Waveshare's driver gives it — and takes spi as its
// bus. pause waits; nil is the wall clock. If a line cannot be claimed, every line
// claimed so far is released and chip and spi closed.
func OpenHAT(chip device.Chip, spi SPI, pins Pins, pause func(time.Duration)) (*HAT, error) {
	if pause == nil {
		pause = time.Sleep
	}
	h := &HAT{chip: chip, spi: spi, pause: pause}
	var err error
	if h.rst, err = chip.RequestOutput(pins.Reset, false, "atlas-epaper"); err == nil {
		if h.dc, err = chip.RequestOutput(pins.DC, false, "atlas-epaper"); err == nil {
			if h.pwr, err = chip.RequestOutput(pins.Power, true, "atlas-epaper"); err == nil {
				h.busy, err = chip.RequestInput(pins.Busy, device.BiasPullDown, "atlas-epaper")
			}
		}
	}
	if err != nil {
		h.Close()
		return nil, fmt.Errorf("epaper: claim the HAT's lines: %w", err)
	}
	return h, nil
}

// Reset pulses the reset line.
func (h *HAT) Reset(high, low, settle time.Duration) error {
	for _, s := range []struct {
		level bool
		wait  time.Duration
	}{{true, high}, {false, low}, {true, settle}} {
		if err := h.rst.Set(s.level); err != nil {
			return err
		}
		h.pause(s.wait)
	}
	return nil
}

// Command sends a command byte with the data/command line low, then its data with
// the line high.
func (h *HAT) Command(c byte, data ...byte) error {
	if err := h.send(false, []byte{c}); err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	return h.send(true, data)
}

func (h *HAT) send(isData bool, p []byte) error {
	if err := h.dc.Set(isData); err != nil {
		return err
	}
	for len(p) > 0 {
		n := min(len(p), maxTransfer)
		if _, err := h.spi.Write(p[:n]); err != nil {
			return fmt.Errorf("spi: %w", err)
		}
		p = p[n:]
	}
	return nil
}

// ErrBusy is returned when the panel stays busy past busyTimeout.
var ErrBusy = errors.New("epaper: the panel stayed busy; is SPI enabled, the HAT seated and the panel model right?")

// WaitIdle polls the busy line, high while the panel works, until it is low.
func (h *HAT) WaitIdle(ctx context.Context) error {
	for waited := time.Duration(0); ; waited += busyPoll {
		busy, err := h.busy.Value()
		if err != nil {
			return err
		}
		if !busy {
			return nil
		}
		if waited >= busyTimeout {
			return ErrBusy
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		h.pause(busyPoll)
	}
}

// Pause waits.
func (h *HAT) Pause(d time.Duration) { h.pause(d) }

// Close drives reset, data/command and power low as Waveshare's module_exit does, and
// releases the lines, the chip and the bus. The picture stays on the glass.
func (h *HAT) Close() error {
	var errs []error
	for _, l := range []device.OutputLine{h.rst, h.dc, h.pwr} {
		if l != nil {
			errs = append(errs, l.Set(false), l.Close())
		}
	}
	if h.busy != nil {
		errs = append(errs, h.busy.Close())
	}
	errs = append(errs, h.spi.Close(), h.chip.Close())
	return errors.Join(errs...)
}
