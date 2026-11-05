package epaper

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/device"
)

// fakeSPI records each write with the level of the data/command line at that moment.
type fakeSPI struct {
	chip   *device.FakeChip
	writes []write
	fail   error
	closed bool
}

type write struct {
	data bool
	n    int
	b0   byte
}

func (s *fakeSPI) Write(p []byte) (int, error) {
	if s.fail != nil {
		return 0, s.fail
	}
	s.writes = append(s.writes, write{data: s.chip.Level(WaveshareHAT.DC), n: len(p), b0: p[0]})
	return len(p), nil
}

func (s *fakeSPI) Close() error { s.closed = true; return nil }

func openTestHAT(t *testing.T) (*HAT, *device.FakeChip, *fakeSPI, *[]time.Duration) {
	t.Helper()
	chip := device.NewFakeChip()
	spi := &fakeSPI{chip: chip}
	var paused []time.Duration
	h, err := OpenHAT(chip, spi, WaveshareHAT, func(d time.Duration) { paused = append(paused, d) })
	if err != nil {
		t.Fatal(err)
	}
	return h, chip, spi, &paused
}

func TestOpenHATClaimsTheLinesAsWaveshareDoes(t *testing.T) {
	_, chip, _, _ := openTestHAT(t)
	if !chip.Level(18) || chip.Level(17) || chip.Level(25) {
		t.Fatal("power on, reset and data/command low")
	}
	if !chip.Held(24) || chip.Bias(24) != device.BiasPullDown {
		t.Fatal("busy is an input with a pull-down")
	}
}

func TestOpenHATReleasesWhatItClaimedOnFailure(t *testing.T) {
	for _, busy := range []int{17, 25, 18, 24} {
		chip := device.NewFakeChip()
		chip.FailRequest = map[int]error{busy: errors.New("busy")}
		spi := &fakeSPI{chip: chip}
		if _, err := OpenHAT(chip, spi, WaveshareHAT, nil); err == nil {
			t.Fatalf("line %d busy: opened", busy)
		}
		for _, l := range []int{17, 25, 18, 24} {
			if chip.Held(l) {
				t.Errorf("line %d busy: %d still held", busy, l)
			}
		}
		if !spi.closed {
			t.Errorf("line %d busy: the bus was left open", busy)
		}
	}
}

func TestCommandsAndDataAreToldApartByTheDCLine(t *testing.T) {
	h, _, spi, _ := openTestHAT(t)
	if err := h.Command(0x44, 0x00, 0x0F); err != nil {
		t.Fatal(err)
	}
	if err := h.Command(0x20); err != nil {
		t.Fatal(err)
	}
	want := []write{{false, 1, 0x44}, {true, 2, 0x00}, {false, 1, 0x20}}
	if !slices.Equal(spi.writes, want) {
		t.Fatalf("writes = %+v", spi.writes)
	}
}

func TestALargeTransferIsSplit(t *testing.T) {
	h, _, spi, _ := openTestHAT(t)
	if err := h.Command(0x24, make([]byte, 9000)...); err != nil {
		t.Fatal(err)
	}
	var sizes []int
	for _, w := range spi.writes[1:] {
		sizes = append(sizes, w.n)
	}
	if !slices.Equal(sizes, []int{4096, 4096, 808}) {
		t.Fatalf("chunks = %v", sizes)
	}
}

func TestABusFailureIsReported(t *testing.T) {
	h, _, spi, _ := openTestHAT(t)
	spi.fail = errors.New("no such device")
	if err := h.Command(0x12); err == nil || !strings.Contains(err.Error(), "spi") {
		t.Fatalf("err = %v", err)
	}
}

func TestResetPulsesTheLine(t *testing.T) {
	h, chip, _, paused := openTestHAT(t)
	if err := h.Reset(200*time.Millisecond, 5*time.Millisecond, 200*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	var levels []bool
	for _, c := range chip.History() {
		if c.Line == 17 {
			levels = append(levels, c.High)
		}
	}
	if !slices.Equal(levels, []bool{false, true, false, true}) {
		t.Fatalf("reset levels = %v", levels)
	}
	if !slices.Equal(*paused, []time.Duration{200 * time.Millisecond, 5 * time.Millisecond, 200 * time.Millisecond}) {
		t.Fatalf("pauses = %v", *paused)
	}
}

func TestWaitIdlePollsTheBusyLine(t *testing.T) {
	chip := device.NewFakeChip()
	polls := 0
	h, err := OpenHAT(chip, &fakeSPI{chip: chip}, WaveshareHAT, func(time.Duration) {
		polls++
		if polls == 3 {
			chip.SetInput(24, false)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	chip.SetInput(24, true)
	if err := h.WaitIdle(context.Background()); err != nil || polls != 3 {
		t.Fatalf("err = %v after %d polls", err, polls)
	}
}

func TestWaitIdleGivesUp(t *testing.T) {
	h, chip, _, paused := openTestHAT(t)
	chip.SetInput(24, true)
	if err := h.WaitIdle(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v", err)
	}
	if got := time.Duration(len(*paused)) * busyPoll; got != busyTimeout {
		t.Fatalf("waited %s, want %s", got, busyTimeout)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.WaitIdle(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled wait: %v", err)
	}
	chip.FailValue = map[int]error{24: errors.New("i/o")}
	if err := h.WaitIdle(context.Background()); err == nil {
		t.Fatal("a busy line that cannot be read is an error")
	}
}

func TestCloseLeavesTheHATUnpowered(t *testing.T) {
	h, chip, spi, paused := openTestHAT(t)
	h.Pause(time.Second)
	if (*paused)[0] != time.Second {
		t.Fatal("Pause waits through the injected clock")
	}
	h.Command(0x10, 0x03) // data/command is left high by data
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if chip.Level(18) || chip.Level(25) || chip.Level(17) {
		t.Fatal("power, data/command and reset are left low")
	}
	for _, l := range []int{17, 25, 18, 24} {
		if chip.Held(l) {
			t.Errorf("line %d still held", l)
		}
	}
	if !spi.closed {
		t.Fatal("the bus is closed")
	}
}

func TestOpenHATDefaultsToTheWallClock(t *testing.T) {
	chip := device.NewFakeChip()
	h, err := OpenHAT(chip, &fakeSPI{chip: chip}, WaveshareHAT, nil)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	h.Pause(time.Millisecond)
	if time.Since(start) < time.Millisecond {
		t.Fatal("the default pause does not wait")
	}
	h.Close()
}

func TestADisplayOnTheHAT(t *testing.T) {
	h, chip, spi, _ := openTestHAT(t)
	_ = chip
	d := New(mustPanel(t, "waveshare-2in13-v4"), h)
	if err := d.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := d.Show(context.Background(), NewFrame()); err != nil {
		t.Fatal(err)
	}
	last := spi.writes[len(spi.writes)-1]
	if last.data || last.b0 != 0x20 {
		t.Fatalf("the last write is the refresh command: %+v", last)
	}
}
