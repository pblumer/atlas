// Package device is the device Worker Type
// (ADR-draft-device-workers-for-the-physical-world): a Worker Instance on a device
// drives its outputs and reads its inputs, addressed by the names its configuration
// gives them.
//
// It is the Worker Type whose failures are physical. Three rules follow, and this
// package enforces them on the device, because nothing upstream can:
//
//   - An output has a safe state. Its line is claimed already driving it, and the
//     device returns to it at shutdown and whenever Safe is called — by the worker
//     that has lost its server, for one.
//   - An output may carry a limit on how long it stays away from its safe state. The
//     device's own watchdog enforces it: a BPMN timer is the plan, the limit is the
//     guarantee, and it holds while the server, the network or the process is gone.
//   - A job is delivered at least once. A write and a read are idempotent; a pulse is
//     not, so the device remembers the job keys it pulsed for and does not pulse again
//     for one it already ran.
package device

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pblumer/atlas/connector/device/catalog"
)

// RefusedError is a job the device cannot run however often it is retried: a line
// its configuration does not name, a duration out of range. A worker fails such a job
// without retries, so it becomes an incident somebody reads, not a retry loop.
type RefusedError struct{ msg string }

func (e *RefusedError) Error() string { return e.msg }

func refused(format string, args ...any) error {
	return &RefusedError{msg: fmt.Sprintf("device: "+format, args...)}
}

// IsRefused reports whether err is a RefusedError.
func IsRefused(err error) bool {
	var r *RefusedError
	return errors.As(err, &r)
}

// ErrClosed is returned for work asked of a Device after Close.
var ErrClosed = errors.New("device: closed")

// dedupWindow is how many pulsed job keys the device remembers. A re-lease arrives
// within one lease of the first, so this bounds memory, not correctness — unless the
// device restarts in between: the memory is not persisted, and a pulse interrupted by
// a restart can be repeated. That case is the documented gap of this slice.
const dedupWindow = 4096

// consumer is the label the kernel shows for a line this package holds.
const consumer = "atlas"

// Device is one configured device with its lines claimed.
type Device struct {
	clock Clock
	chip  Chip

	mu       sync.Mutex // guards every field below, and every line write
	outputs  map[string]*output
	inputs   map[string]*input
	pulsed   map[uint64]struct{}
	order    []uint64
	closed   bool
	tripped  int
	released bool
}

type output struct {
	cfg  Output
	line OutputLine
	// turn serialises the operations on this output, so a write waits for a pulse to
	// end rather than cutting into it. The watchdog and Safe do not take it: they
	// win over any operation in progress.
	turn chan struct{}
	on   bool
	// dog is the running watchdog, gen tells a stale firing from the current one.
	dog Timer
	gen uint64
}

type input struct {
	cfg  Input
	line InputLine
}

// Open claims every configured line on chip: outputs already driving their safe
// state, inputs with their bias. If any line cannot be claimed, every line claimed so
// far is released and the chip closed, so a half-claimed device is never left behind.
func Open(cfg Config, chip Chip, clock Clock) (*Device, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	d := &Device{
		clock:   clock,
		chip:    chip,
		outputs: map[string]*output{},
		inputs:  map[string]*input{},
		pulsed:  map[uint64]struct{}{},
	}
	for _, o := range cfg.Outputs {
		line, err := chip.RequestOutput(o.Line, o.SafeOn() != o.ActiveLow, consumer)
		if err != nil {
			d.release()
			return nil, fmt.Errorf("device: output %s on line %d: %w", o.Name, o.Line, err)
		}
		d.outputs[o.Name] = &output{cfg: o, line: line, turn: make(chan struct{}, 1), on: o.SafeOn()}
	}
	for _, in := range cfg.Inputs {
		line, err := chip.RequestInput(in.Line, in.bias(), consumer)
		if err != nil {
			d.release()
			return nil, fmt.Errorf("device: input %s on line %d: %w", in.Name, in.Line, err)
		}
		d.inputs[in.Name] = &input{cfg: in, line: line}
	}
	return d, nil
}

// release closes every claimed line and the chip. Callers hold no lock.
func (d *Device) release() error {
	if d.released {
		return nil
	}
	d.released = true
	var errs []error
	for _, o := range d.outputs {
		errs = append(errs, o.line.Close())
	}
	for _, in := range d.inputs {
		errs = append(errs, in.line.Close())
	}
	errs = append(errs, d.chip.Close())
	return errors.Join(errs...)
}

func (d *Device) output(name string) (*output, error) {
	o, ok := d.outputs[name]
	if !ok {
		return nil, refused("%q is not an output of this device", name)
	}
	return o, nil
}

// take waits for the output's turn, or for ctx.
func (o *output) take(ctx context.Context) error {
	select {
	case o.turn <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (o *output) give() { <-o.turn }

// drive sets an output to a logical state and arms or disarms its watchdog. It holds
// d.mu for the line write, so the watchdog and Safe never interleave with it.
func (d *Device) drive(o *output, on bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return ErrClosed
	}
	if err := o.line.Set(on != o.cfg.ActiveLow); err != nil {
		return fmt.Errorf("device: output %s: %w", o.cfg.Name, err)
	}
	o.on = on
	d.disarm(o)
	if on != o.cfg.SafeOn() && o.cfg.MaxOn() > 0 {
		gen := o.gen
		o.dog = d.clock.AfterFunc(o.cfg.MaxOn(), func() { d.trip(o, gen) })
	}
	return nil
}

// disarm stops the output's watchdog; d.mu is held.
func (d *Device) disarm(o *output) {
	o.gen++
	if o.dog != nil {
		o.dog.Stop()
		o.dog = nil
	}
}

// trip is the watchdog firing: the output has been away from its safe state for its
// whole limit, and goes back to it whatever is in progress.
func (d *Device) trip(o *output, gen uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || gen != o.gen {
		return // disarmed, or rearmed, after this firing was scheduled
	}
	o.dog = nil
	if err := o.line.Set(o.cfg.SafeOn() != o.cfg.ActiveLow); err == nil {
		o.on = o.cfg.SafeOn()
	}
	d.tripped++
}

// Write drives a named output on or off. It is idempotent.
func (d *Device) Write(ctx context.Context, name string, on bool) error {
	o, err := d.output(name)
	if err != nil {
		return err
	}
	if err := o.take(ctx); err != nil {
		return err
	}
	defer o.give()
	return d.drive(o, on)
}

// Pulse drives a named output to on for duration, then returns it to its safe
// state. jobKey identifies the job asking: a pulse for a job key this device already
// pulsed for returns at once, with deduplicated true and nothing driven.
//
// If ctx ends during the pulse the output goes back to its safe state at once and the
// job key stays remembered: the effect has begun, and repeating it on a retry is the
// failure this exists to prevent.
func (d *Device) Pulse(ctx context.Context, jobKey uint64, name string, on bool, duration time.Duration) (deduplicated bool, err error) {
	o, err := d.output(name)
	if err != nil {
		return false, err
	}
	if err := catalog.CheckDuration(duration.Milliseconds()); err != nil || duration%time.Millisecond != 0 {
		if err == nil {
			err = fmt.Errorf("device: a pulse is whole milliseconds, not %s", duration)
		}
		return false, &RefusedError{msg: err.Error()}
	}
	if on == o.cfg.SafeOn() {
		return false, refused("a pulse of %s to its safe state would change nothing", name)
	}
	if limit := o.cfg.MaxOn(); limit > 0 && duration > limit {
		return false, refused("a pulse of %s on %s is longer than its limit of %s", duration, name, limit)
	}
	if err := o.take(ctx); err != nil {
		return false, err
	}
	defer o.give()

	if !d.remember(jobKey) {
		return true, nil
	}
	if err := d.drive(o, on); err != nil {
		d.forget(jobKey) // nothing was driven, so a retry may try again
		return false, err
	}
	done := make(chan struct{})
	timer := d.clock.AfterFunc(duration, func() { close(done) })
	var waitErr error
	select {
	case <-done:
	case <-ctx.Done():
		timer.Stop()
		waitErr = ctx.Err()
	}
	if err := d.drive(o, o.cfg.SafeOn()); err != nil {
		return false, fmt.Errorf("device: %s did not return to its safe state after a pulse: %w", name, err)
	}
	return false, waitErr
}

// remember records a pulsed job key; false when it was already recorded.
func (d *Device) remember(key uint64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, seen := d.pulsed[key]; seen {
		return false
	}
	d.pulsed[key] = struct{}{}
	d.order = append(d.order, key)
	if len(d.order) > dedupWindow {
		delete(d.pulsed, d.order[0])
		d.order = d.order[1:]
	}
	return true
}

func (d *Device) forget(key uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.pulsed, key)
	for i, k := range d.order {
		if k == key {
			d.order = append(d.order[:i], d.order[i+1:]...)
			break
		}
	}
}

// Read reports whether a named line is on: an output's last driven state, an input's
// level as its activeLow reads it.
func (d *Device) Read(name string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return false, ErrClosed
	}
	if o, ok := d.outputs[name]; ok {
		return o.on, nil
	}
	in, ok := d.inputs[name]
	if !ok {
		return false, refused("%q is not a line of this device", name)
	}
	high, err := in.line.Value()
	if err != nil {
		return false, fmt.Errorf("device: input %s: %w", name, err)
	}
	return high != in.cfg.ActiveLow, nil
}

// Safe drives every output to its safe state at once, without waiting for an
// operation in progress: what a worker calls when it has lost its server for longer
// than it may act without one. A pulse in progress returns to its safe state too
// when it ends.
func (d *Device) Safe() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return ErrClosed
	}
	return d.safeLocked()
}

func (d *Device) safeLocked() error {
	var errs []error
	for _, o := range d.outputs {
		d.disarm(o)
		if err := o.line.Set(o.cfg.SafeOn() != o.cfg.ActiveLow); err != nil {
			errs = append(errs, fmt.Errorf("device: output %s: %w", o.cfg.Name, err))
			continue
		}
		o.on = o.cfg.SafeOn()
	}
	return errors.Join(errs...)
}

// Trips is how often a watchdog has returned an output to its safe state.
func (d *Device) Trips() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.tripped
}

// Close drives every output to its safe state and releases the lines and the chip.
// A Device is not usable afterwards.
func (d *Device) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	safeErr := d.safeLocked()
	d.closed = true
	d.mu.Unlock()
	return errors.Join(safeErr, d.release())
}
