// Package epaper drives the e-paper panels a device Worker may carry
// (ADR-draft-device-workers-for-the-physical-world, slice IOT3): it initialises a
// panel, writes a picture into its memory and refreshes it.
//
// "E-paper" is not one driver. Each panel generation has its own controller and its
// own initialisation, and a sequence written for one leaves another blank or garbled.
// So a panel is named by its model, and this package knows a closed list of them.
//
// The register sequences of the Waveshare 2.13inch models are those of Waveshare's
// own drivers (epd2in13_V2.py, epd2in13_V3.py, epd2in13_V4.py in
// github.com/waveshareteam/e-Paper, MIT licence, copyright the Waveshare team),
// restated here as data; THIRD_PARTY_NOTICES.md records them.
package epaper

import (
	"fmt"
	"sort"
	"time"
)

// The panels' resolution. Every model here has the same 2.13inch glass: 122 pixels
// across the controller's rows, 250 rows. A Frame is the picture as a person reads it,
// landscape: Width across, Height down.
const (
	Width  = 250
	Height = 122

	// rowBytes is one controller row: 122 pixels, padded to whole bytes.
	rowBytes = (Height + 7) / 8 // 16
	// BufferSize is a whole picture in the controller's memory.
	BufferSize = rowBytes * Width // 4000
)

// Panel is one model: how to reset it, what to send to bring it up, how to show a
// picture and how to put it to sleep.
type Panel struct {
	Model string
	// Reset is the reset pin's timing: high, low, high again.
	ResetHigh, ResetLow, ResetSettle time.Duration
	// init is the register sequence after the hardware reset.
	init []step
	// refresh is the value of the display update control (0x22) for a full refresh.
	refresh byte
	// sleep is the value of deep sleep (0x10).
	sleep byte
	// reversed says the controller's memory is filled from its last row to its first,
	// as the V2 controller's data entry mode (0x01, Y decrementing) has it.
	reversed bool
}

// step is one command with its data, or a wait for the panel to be idle.
type step struct {
	cmd  byte
	data []byte
	wait bool
}

func cmd(c byte, data ...byte) step { return step{cmd: c, data: data} }

var waitIdle = step{wait: true}

// The full-update waveform of the V2 panel: 70 bytes to the LUT register (0x32), then
// the gate voltage, the three source voltages, the dummy line and the gate time.
var lutV2 = []byte{
	0x80, 0x60, 0x40, 0x00, 0x00, 0x00, 0x00, // LUT0: BB
	0x10, 0x60, 0x20, 0x00, 0x00, 0x00, 0x00, // LUT1: BW
	0x80, 0x60, 0x40, 0x00, 0x00, 0x00, 0x00, // LUT2: WB
	0x10, 0x60, 0x20, 0x00, 0x00, 0x00, 0x00, // LUT3: WW
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // LUT4: VCOM
	0x03, 0x03, 0x00, 0x00, 0x02, // TP0
	0x09, 0x09, 0x00, 0x00, 0x02, // TP1
	0x03, 0x03, 0x00, 0x00, 0x02, // TP2
	0x00, 0x00, 0x00, 0x00, 0x00, // TP3
	0x00, 0x00, 0x00, 0x00, 0x00, // TP4
	0x00, 0x00, 0x00, 0x00, 0x00, // TP5
	0x00, 0x00, 0x00, 0x00, 0x00, // TP6
	0x15, 0x41, 0xA8, 0x32, 0x30, 0x0A,
}

// The full-update waveform of the V3 panel: 153 bytes to the LUT register, then the
// end option, gate voltage, the three source voltages and VCOM.
var lutV3 = []byte{
	0x80, 0x4A, 0x40, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0,
	0x40, 0x4A, 0x80, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0,
	0x80, 0x4A, 0x40, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0,
	0x40, 0x4A, 0x80, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0,
	0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0,
	0xF, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0,
	0xF, 0x0, 0x0, 0xF, 0x0, 0x0, 0x2,
	0xF, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0,
	0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0,
	0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0,
	0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0,
	0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0,
	0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0,
	0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0,
	0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0,
	0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0,
	0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0,
	0x22, 0x22, 0x22, 0x22, 0x22, 0x22, 0x0, 0x0, 0x0,
	0x22, 0x17, 0x41, 0x0, 0x32, 0x36,
}

// windowAndCursor is SetWindow(0, 0, 121, 249) and SetCursor(0, 0) of the V3 and V4
// drivers.
var windowAndCursor = []step{
	cmd(0x44, 0x00, (Height-1)>>3),
	cmd(0x45, 0x00, 0x00, (Width-1)&0xFF, (Width-1)>>8),
	cmd(0x4E, 0x00),
	cmd(0x4F, 0x00, 0x00),
}

func join(parts ...[]step) []step {
	var out []step
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

var panels = map[string]Panel{
	"waveshare-2in13-v2": {
		Model:     "waveshare-2in13-v2",
		ResetHigh: 200 * time.Millisecond, ResetLow: 5 * time.Millisecond, ResetSettle: 200 * time.Millisecond,
		init: []step{
			waitIdle,
			cmd(0x12), // soft reset
			waitIdle,
			cmd(0x74, 0x54),             // analog block control
			cmd(0x7E, 0x3B),             // digital block control
			cmd(0x01, 0xF9, 0x00, 0x00), // driver output control: 250 gate lines
			cmd(0x11, 0x01),             // data entry: X increment, Y decrement
			cmd(0x44, 0x00, 0x0F),       // RAM X 0..15 (128 px)
			cmd(0x45, 0xF9, 0x00, 0x00, 0x00),
			cmd(0x3C, 0x03), // border waveform
			cmd(0x2C, 0x55), // VCOM
			cmd(0x03, lutV2[70]),
			cmd(0x04, lutV2[71], lutV2[72], lutV2[73]),
			cmd(0x3A, lutV2[74]), // dummy line
			cmd(0x3B, lutV2[75]), // gate time
			cmd(0x32, lutV2[:70]...),
			cmd(0x4E, 0x00),
			cmd(0x4F, 0xF9, 0x00),
			waitIdle,
		},
		refresh:  0xC7,
		sleep:    0x03,
		reversed: true,
	},
	"waveshare-2in13-v3": {
		Model:     "waveshare-2in13-v3",
		ResetHigh: 20 * time.Millisecond, ResetLow: 2 * time.Millisecond, ResetSettle: 20 * time.Millisecond,
		init: join(
			[]step{waitIdle, cmd(0x12), waitIdle, cmd(0x01, 0xF9, 0x00, 0x00), cmd(0x11, 0x03)},
			windowAndCursor,
			[]step{
				cmd(0x3C, 0x05),
				cmd(0x21, 0x00, 0x80),
				cmd(0x18, 0x80),
				waitIdle,
				cmd(0x32, lutV3[:153]...),
				waitIdle,
				cmd(0x3F, lutV3[153]),
				cmd(0x03, lutV3[154]),
				cmd(0x04, lutV3[155], lutV3[156], lutV3[157]),
				cmd(0x2C, lutV3[158]),
			},
		),
		refresh: 0xC7,
		sleep:   0x01,
	},
	"waveshare-2in13-v4": {
		Model:     "waveshare-2in13-v4",
		ResetHigh: 20 * time.Millisecond, ResetLow: 2 * time.Millisecond, ResetSettle: 20 * time.Millisecond,
		init: join(
			[]step{waitIdle, cmd(0x12), waitIdle, cmd(0x01, 0xF9, 0x00, 0x00), cmd(0x11, 0x03)},
			windowAndCursor,
			[]step{cmd(0x3C, 0x05), cmd(0x21, 0x00, 0x80), cmd(0x18, 0x80), waitIdle},
		),
		// The V4 panel carries its waveform in the controller's OTP; 0xF7 loads it.
		refresh: 0xF7,
		sleep:   0x01,
	},
}

// Lookup returns the panel of that model.
func Lookup(model string) (Panel, error) {
	p, ok := panels[model]
	if !ok {
		return Panel{}, fmt.Errorf("epaper: unknown panel %q (want one of %v)", model, Models())
	}
	return p, nil
}

// Models lists the panels this package drives, sorted.
func Models() []string {
	out := make([]string, 0, len(panels))
	for m := range panels {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}
