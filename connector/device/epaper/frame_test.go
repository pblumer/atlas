package epaper

import (
	"image"
	"image/color"
	"testing"
)

var blackC = color.Black

func TestAWhiteFrameIsAllOnes(t *testing.T) {
	buf := NewFrame().encode(false)
	if len(buf) != BufferSize || BufferSize != 4000 {
		t.Fatalf("len = %d", len(buf))
	}
	for i, b := range buf {
		if b != 0xFF {
			t.Fatalf("byte %d = %#x", i, b)
		}
	}
}

// Waveshare's getbuffer for a landscape image: V2 puts column x in controller row x,
// V3 and V4 rotate the image a quarter turn first, which puts it in row 249-x. Row y
// of the picture is bit y of the row, most significant first.
func TestFrameEncodingMatchesWavesharesGetbuffer(t *testing.T) {
	f := NewFrame()
	f.Set(0, 0, blackC)
	f.Set(249, 121, blackC)
	f.Set(10, 9, blackC)

	v2 := f.encode(true)
	if v2[0] != 0x7F || v2[249*16+15] != 0xBF || v2[10*16+1] != 0xBF {
		t.Fatalf("V2 layout: %#x %#x %#x", v2[0], v2[249*16+15], v2[10*16+1])
	}
	v3 := f.encode(false)
	if v3[249*16] != 0x7F || v3[15] != 0xBF || v3[239*16+1] != 0xBF {
		t.Fatalf("V3 layout: %#x %#x %#x", v3[249*16], v3[15], v3[239*16+1])
	}
}

func TestFramePixels(t *testing.T) {
	f := NewFrame()
	if f.ColorModel() != color.GrayModel || f.Bounds() != image.Rect(0, 0, 250, 122) {
		t.Fatal("a frame is 250×122 grey")
	}
	f.Set(-1, 0, blackC)
	f.Set(250, 0, blackC)
	f.Set(0, 122, blackC)
	if f.Black(-1, 0) || f.Black(0, 122) {
		t.Fatal("outside the frame is white and stays so")
	}
	f.Set(3, 4, color.Gray{Y: 0x7F})
	f.Set(5, 4, color.Gray{Y: 0x80})
	if !f.Black(3, 4) || f.Black(5, 4) {
		t.Fatal("darker than middle grey is black")
	}
	if f.At(3, 4) != (color.Gray{Y: 0}) || f.At(5, 4) != (color.Gray{Y: 0xFF}) {
		t.Fatal("At reads black and white")
	}
}

func TestRect(t *testing.T) {
	f := NewFrame()
	f.Rect(image.Rect(0, 0, 250, 122))
	for _, p := range []image.Point{{0, 0}, {249, 0}, {0, 121}, {249, 121}, {125, 0}, {0, 60}} {
		if !f.Black(p.X, p.Y) {
			t.Errorf("%v is not on the outline", p)
		}
	}
	if f.Black(1, 1) {
		t.Fatal("the inside is filled")
	}
}

func count(f *Frame) int {
	n := 0
	for _, b := range f.black {
		if b {
			n++
		}
	}
	return n
}

func TestTextRendersUmlauts(t *testing.T) {
	plain, umlaut := NewFrame(), NewFrame()
	end, err := plain.Text(4, 30, 24, false, "A")
	if err != nil {
		t.Fatal(err)
	}
	if end.X <= 4 || end.Y != 30 {
		t.Fatalf("the pen ended at %v", end)
	}
	if _, err := umlaut.Text(4, 30, 24, false, "Ä"); err != nil {
		t.Fatal(err)
	}
	if count(plain) == 0 || count(umlaut) <= count(plain) {
		t.Fatalf("A has %d black pixels, Ä %d: the dots are missing", count(plain), count(umlaut))
	}
	b := NewFrame()
	if _, err := b.Text(4, 30, 24, true, "A"); err != nil {
		t.Fatal(err)
	}
	if count(b) <= count(plain) {
		t.Fatal("bold is not heavier than regular")
	}
}
