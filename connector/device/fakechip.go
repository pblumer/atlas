package device

import (
	"errors"
	"fmt"
	"sync"
)

// FakeChip is a GPIO controller held in memory: what this package's tests run
// against, and what a device Worker's mock mode serves, so a model that switches a
// relay can run end to end on a machine with no pins. It records every level a line
// was driven to, in order.
type FakeChip struct {
	mu      sync.Mutex
	held    map[int]bool
	levels  map[int]bool
	bias    map[int]Bias
	history []Change
	closed  bool

	// FailRequest makes requesting that line fail, as a line another process holds.
	FailRequest map[int]error
	// FailSet makes driving that line fail, as a line whose controller went away.
	FailSet map[int]error
	// FailValue makes reading that line fail.
	FailValue map[int]error
}

// Change is one level a line was driven to.
type Change struct {
	Line int
	High bool
}

// NewFakeChip returns a chip whose lines are all low and free.
func NewFakeChip() *FakeChip {
	return &FakeChip{held: map[int]bool{}, levels: map[int]bool{}, bias: map[int]Bias{}}
}

func (c *FakeChip) claim(line int) error {
	if c.closed {
		return errors.New("fake chip: closed")
	}
	if err := c.FailRequest[line]; err != nil {
		return err
	}
	if c.held[line] {
		return fmt.Errorf("fake chip: line %d: device or resource busy", line)
	}
	c.held[line] = true
	return nil
}

// RequestOutput claims a line and drives initial at once.
func (c *FakeChip) RequestOutput(line int, initial bool, _ string) (OutputLine, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.claim(line); err != nil {
		return nil, err
	}
	c.levels[line] = initial
	c.history = append(c.history, Change{Line: line, High: initial})
	return &fakeLine{chip: c, line: line}, nil
}

// RequestInput claims a line to read.
func (c *FakeChip) RequestInput(line int, bias Bias, _ string) (InputLine, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.claim(line); err != nil {
		return nil, err
	}
	c.bias[line] = bias
	if bias == BiasPullUp {
		c.levels[line] = true // an open contact against a pull-up reads high
	}
	return &fakeLine{chip: c, line: line}, nil
}

// Close releases the chip and every line on it.
func (c *FakeChip) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.held = map[int]bool{}
	return nil
}

// SetInput sets the level the outside world puts on an input line.
func (c *FakeChip) SetInput(line int, high bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.levels[line] = high
}

// Level is the line's current level.
func (c *FakeChip) Level(line int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.levels[line]
}

// Held reports whether a line is claimed.
func (c *FakeChip) Held(line int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.held[line]
}

// Bias is the bias an input line was claimed with.
func (c *FakeChip) Bias(line int) Bias {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bias[line]
}

// History is every level driven onto output lines, in order.
func (c *FakeChip) History() []Change {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Change(nil), c.history...)
}

type fakeLine struct {
	chip *FakeChip
	line int
}

func (l *fakeLine) Set(high bool) error {
	c := l.chip
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.held[l.line] {
		return fmt.Errorf("fake chip: line %d is not held", l.line)
	}
	if err := c.FailSet[l.line]; err != nil {
		return err
	}
	c.levels[l.line] = high
	c.history = append(c.history, Change{Line: l.line, High: high})
	return nil
}

func (l *fakeLine) Value() (bool, error) {
	c := l.chip
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.held[l.line] {
		return false, fmt.Errorf("fake chip: line %d is not held", l.line)
	}
	if err := c.FailValue[l.line]; err != nil {
		return false, err
	}
	return c.levels[l.line], nil
}

func (l *fakeLine) Close() error {
	c := l.chip
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.held, l.line)
	return nil
}
