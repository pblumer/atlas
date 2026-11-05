package epaper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// recorder is a Link that writes down what a Display asks of it.
type recorder struct {
	ops     []string
	payload map[byte][]byte
	failOn  string
	closed  bool
}

func (r *recorder) note(op string) error {
	r.ops = append(r.ops, op)
	if r.failOn != "" && strings.HasPrefix(op, r.failOn) {
		return errors.New("link down")
	}
	return nil
}

func (r *recorder) Reset(high, low, settle time.Duration) error {
	return r.note(fmt.Sprintf("reset %d/%d/%d", high.Milliseconds(), low.Milliseconds(), settle.Milliseconds()))
}

func (r *recorder) Command(c byte, data ...byte) error {
	if r.payload == nil {
		r.payload = map[byte][]byte{}
	}
	r.payload[c] = append([]byte(nil), data...)
	if len(data) > 8 {
		return r.note(fmt.Sprintf("%02X +%d", c, len(data)))
	}
	return r.note(strings.TrimSpace(fmt.Sprintf("%02X % X", c, data)))
}

func (r *recorder) WaitIdle(context.Context) error { return r.note("wait") }
func (r *recorder) Pause(d time.Duration)          { r.note(fmt.Sprintf("pause %d", d.Milliseconds())) }
func (r *recorder) Close() error                   { r.closed = true; return nil }

func mustPanel(t *testing.T, model string) Panel {
	t.Helper()
	p, err := Lookup(model)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestModels(t *testing.T) {
	want := []string{"waveshare-2in13-v2", "waveshare-2in13-v3", "waveshare-2in13-v4"}
	if got := Models(); !slices.Equal(got, want) {
		t.Fatalf("Models() = %v", got)
	}
	if _, err := Lookup("waveshare-7in5"); err == nil || !strings.Contains(err.Error(), "waveshare-2in13-v2") {
		t.Fatalf("an unknown panel: %v", err)
	}
}

// The expectations below restate Waveshare's epd2in13_V*.py init() line by line, so a
// slip in the tables in panel.go fails here rather than as a blank panel.
func TestTheV2InitIsWaveshares(t *testing.T) {
	r := &recorder{}
	if err := New(mustPanel(t, "waveshare-2in13-v2"), r).Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"reset 200/5/200", "wait", "12", "wait",
		"74 54", "7E 3B", "01 F9 00 00", "11 01", "44 00 0F", "45 F9 00 00 00",
		"3C 03", "2C 55", "03 15", "04 41 A8 32", "3A 30", "3B 0A", "32 +70",
		"4E 00", "4F F9 00", "wait",
	}
	if !slices.Equal(r.ops, want) {
		t.Fatalf("V2 init\n got %q\nwant %q", r.ops, want)
	}
	if !bytes.Equal(r.payload[0x32][:7], []byte{0x80, 0x60, 0x40, 0, 0, 0, 0}) || r.payload[0x32][69] != 0x00 {
		t.Fatal("the V2 waveform is not Waveshare's")
	}
}

func TestTheV3InitIsWaveshares(t *testing.T) {
	r := &recorder{}
	if err := New(mustPanel(t, "waveshare-2in13-v3"), r).Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"reset 20/2/20", "wait", "12", "wait", "01 F9 00 00", "11 03",
		"44 00 0F", "45 00 00 F9 00", "4E 00", "4F 00 00",
		"3C 05", "21 00 80", "18 80", "wait",
		"32 +153", "wait", "3F 22", "03 17", "04 41 00 32", "2C 36",
	}
	if !slices.Equal(r.ops, want) {
		t.Fatalf("V3 init\n got %q\nwant %q", r.ops, want)
	}
	if len(lutV3) != 159 || len(lutV2) != 76 {
		t.Fatalf("waveform lengths %d and %d, want 159 and 76", len(lutV3), len(lutV2))
	}
}

func TestTheV4InitUsesTheControllersOwnWaveform(t *testing.T) {
	r := &recorder{}
	d := New(mustPanel(t, "waveshare-2in13-v4"), r)
	if err := d.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(r.ops, func(op string) bool { return strings.HasPrefix(op, "32") }) {
		t.Fatal("the V4 panel was sent a waveform")
	}
	r.ops = nil
	if err := d.Show(context.Background(), NewFrame()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"24 +4000", "22 F7", "20", "wait"}; !slices.Equal(r.ops, want) {
		t.Fatalf("V4 show = %q", r.ops)
	}
}

func TestShowWritesThePictureAndRefreshes(t *testing.T) {
	r := &recorder{}
	d := New(mustPanel(t, "waveshare-2in13-v2"), r)
	if err := d.Show(context.Background(), NewFrame()); !errors.Is(err, ErrNotInitialised) {
		t.Fatalf("Show before Init = %v", err)
	}
	d.Init(context.Background())
	r.ops = nil
	f := NewFrame()
	f.Set(0, 0, blackC)
	if err := d.Show(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	if want := []string{"24 +4000", "22 C7", "20", "wait"}; !slices.Equal(r.ops, want) {
		t.Fatalf("show = %q", r.ops)
	}
	if r.payload[0x24][0] != 0x7F {
		t.Fatalf("the picture sent is not the frame: first byte %#x", r.payload[0x24][0])
	}
}

func TestSleepAndClose(t *testing.T) {
	r := &recorder{}
	d := New(mustPanel(t, "waveshare-2in13-v2"), r)
	d.Init(context.Background())
	r.ops = nil
	if err := d.Sleep(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"10 03", "pause 2000"}; !slices.Equal(r.ops, want) {
		t.Fatalf("sleep = %q", r.ops)
	}
	if err := d.Show(context.Background(), NewFrame()); !errors.Is(err, ErrNotInitialised) {
		t.Fatal("a sleeping panel must be initialised again before it shows a picture")
	}
	if err := d.Close(); err != nil || !r.closed {
		t.Fatal("Close did not release the link")
	}
}

func TestLinkFailuresAreReported(t *testing.T) {
	for _, tc := range []struct{ failOn, call, want string }{
		{"reset", "init", "reset"},
		{"74", "init", "init"},
		{"24", "show", "write picture"},
		{"22", "show", "refresh"},
		{"10", "sleep", "sleep"},
	} {
		r := &recorder{}
		d := New(mustPanel(t, "waveshare-2in13-v2"), r)
		var err error
		if tc.call != "init" {
			d.Init(context.Background())
		}
		r.failOn = tc.failOn
		switch tc.call {
		case "init":
			err = d.Init(context.Background())
		case "show":
			err = d.Show(context.Background(), NewFrame())
		case "sleep":
			err = d.Sleep()
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("failing %s during %s: %v", tc.failOn, tc.call, err)
		}
	}
}
