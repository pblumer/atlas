package device

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/pblumer/atlas/connector/device/catalog"
)

// Config is one device Worker's wiring: which chip, and which named outputs and inputs
// sit on which of its lines. It is the Worker's configuration, never the model's — a
// model names a line, the configuration says where it is.
type Config struct {
	// Chip is the GPIO character device, as its name under /dev: gpiochip0.
	Chip    string   `json:"chip"`
	Outputs []Output `json:"outputs,omitempty"`
	Inputs  []Input  `json:"inputs,omitempty"`
}

// Output is a line the device drives.
type Output struct {
	Name string `json:"name"`
	// Line is the line's offset on the chip (on a Raspberry Pi header, the BCM number).
	Line int `json:"line"`
	// ActiveLow says the output is on when the wire is low, as on most relay boards.
	ActiveLow bool `json:"activeLow,omitempty"`
	// Safe is the state the device holds the output in when nothing has asked for
	// another, and returns it to at start, at shutdown and when the watchdog fires:
	// "off" (the default) or "on".
	Safe string `json:"safe,omitempty"`
	// MaxOnMs is the longest the output may stay away from its safe state, enforced
	// by the device itself, so a lost server, a cancelled process or a forgotten
	// write cannot leave a valve open. Zero means no limit.
	MaxOnMs int64 `json:"maxOnMs,omitempty"`
}

// Input is a line the device reads.
type Input struct {
	Name      string `json:"name"`
	Line      int    `json:"line"`
	ActiveLow bool   `json:"activeLow,omitempty"`
	// Bias is the line's internal resistor: "none" (the default), "pull-up" or
	// "pull-down". A button to ground wants pull-up with activeLow.
	Bias string `json:"bias,omitempty"`
}

// SafeOn reports whether the output's safe state is on.
func (o Output) SafeOn() bool { return o.Safe == "on" }

// MaxOn is MaxOnMs as a duration; zero is no limit.
func (o Output) MaxOn() time.Duration { return time.Duration(o.MaxOnMs) * time.Millisecond }

// maxLine bounds a line offset. No chip a Raspberry Pi carries comes near it; the
// bound keeps a typo from reaching the kernel as an absurd number.
const maxLine = 1023

// maxOnCeiling bounds the watchdog: a day. A limit longer than that is no limit
// anyone reasons about.
const maxOnCeiling = 24 * time.Hour

var chipPattern = regexp.MustCompile(`^gpiochip[0-9]{1,3}$`)

// ParseConfig reads a device Worker's configuration and checks it whole: unknown
// fields are refused, every name and line is unique, and every value is in range.
func ParseConfig(raw []byte) (Config, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var c Config
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("device: configuration: %w", err)
	}
	if dec.More() {
		return Config{}, fmt.Errorf("device: configuration: more than one JSON value")
	}
	return c, c.Validate()
}

// Validate checks the configuration whole.
func (c Config) Validate() error {
	if !chipPattern.MatchString(c.Chip) {
		return fmt.Errorf("device: chip %q is not a GPIO character device name such as gpiochip0", c.Chip)
	}
	if len(c.Outputs)+len(c.Inputs) == 0 {
		return fmt.Errorf("device: the configuration names no output and no input")
	}
	names := map[string]bool{}
	lines := map[int]string{}
	claim := func(name string, line int) error {
		if err := catalog.CheckName(name); err != nil {
			return err
		}
		if names[name] {
			return fmt.Errorf("device: the name %q is used twice", name)
		}
		if line < 0 || line > maxLine {
			return fmt.Errorf("device: %s: line %d is outside 0..%d", name, line, maxLine)
		}
		if other, taken := lines[line]; taken {
			return fmt.Errorf("device: %s and %s are both wired to line %d", other, name, line)
		}
		names[name] = true
		lines[line] = name
		return nil
	}
	for _, o := range c.Outputs {
		if err := claim(o.Name, o.Line); err != nil {
			return err
		}
		if o.Safe != "" && o.Safe != "off" && o.Safe != "on" {
			return fmt.Errorf("device: %s: safe is %q, want off or on", o.Name, o.Safe)
		}
		if o.MaxOnMs < 0 || o.MaxOn() > maxOnCeiling {
			return fmt.Errorf("device: %s: maxOnMs %d is outside 0..%d", o.Name, o.MaxOnMs, maxOnCeiling.Milliseconds())
		}
	}
	for _, in := range c.Inputs {
		if err := claim(in.Name, in.Line); err != nil {
			return err
		}
		switch in.Bias {
		case "", "none", "pull-up", "pull-down":
		default:
			return fmt.Errorf("device: %s: bias is %q, want none, pull-up or pull-down", in.Name, in.Bias)
		}
	}
	return nil
}

// bias maps an input's configured bias onto the chip's.
func (in Input) bias() Bias {
	switch in.Bias {
	case "pull-up":
		return BiasPullUp
	case "pull-down":
		return BiasPullDown
	}
	return BiasNone
}
