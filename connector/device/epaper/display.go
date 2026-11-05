package epaper

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Link is the wire to a panel: its reset line, commands and data on the bus, and its
// busy line. The hardware link is the HAT on a Pi's header; tests record the calls.
type Link interface {
	// Reset drives the reset line high for high, low for low, then high and waits
	// settle.
	Reset(high, low, settle time.Duration) error
	// Command sends one command byte and its data.
	Command(c byte, data ...byte) error
	// WaitIdle returns once the panel is no longer busy, or fails when ctx ends.
	WaitIdle(ctx context.Context) error
	// Pause waits; the deep-sleep entry asks for it.
	Pause(d time.Duration)
	Close() error
}

// Display is one panel on its link.
type Display struct {
	panel Panel
	link  Link
	ready bool
}

// New returns a display of that panel on that link. Nothing is sent until Init.
func New(panel Panel, link Link) *Display { return &Display{panel: panel, link: link} }

// Init resets the panel and sends its initialisation.
func (d *Display) Init(ctx context.Context) error {
	p := d.panel
	if err := d.link.Reset(p.ResetHigh, p.ResetLow, p.ResetSettle); err != nil {
		return fmt.Errorf("epaper: reset: %w", err)
	}
	for _, s := range p.init {
		if err := d.run(ctx, s); err != nil {
			return fmt.Errorf("epaper: %s init: %w", p.Model, err)
		}
	}
	d.ready = true
	return nil
}

func (d *Display) run(ctx context.Context, s step) error {
	if s.wait {
		return d.link.WaitIdle(ctx)
	}
	return d.link.Command(s.cmd, s.data...)
}

// ErrNotInitialised is returned by Show before Init.
var ErrNotInitialised = errors.New("epaper: the panel is not initialised")

// Show writes the frame into the panel's memory and runs a full refresh. It returns
// once the panel reports the refresh finished — seconds, on e-paper.
func (d *Display) Show(ctx context.Context, f *Frame) error {
	if !d.ready {
		return ErrNotInitialised
	}
	if err := d.link.Command(0x24, f.encode(d.panel.reversed)...); err != nil {
		return fmt.Errorf("epaper: write picture: %w", err)
	}
	for _, s := range []step{cmd(0x22, d.panel.refresh), cmd(0x20), waitIdle} {
		if err := d.run(ctx, s); err != nil {
			return fmt.Errorf("epaper: refresh: %w", err)
		}
	}
	return nil
}

// Sleep puts the panel into deep sleep; the picture stays. Init wakes it again.
func (d *Display) Sleep() error {
	d.ready = false
	if err := d.link.Command(0x10, d.panel.sleep); err != nil {
		return fmt.Errorf("epaper: sleep: %w", err)
	}
	d.link.Pause(2 * time.Second)
	return nil
}

// Close releases the link; the picture stays on the glass without power.
func (d *Display) Close() error { return d.link.Close() }
