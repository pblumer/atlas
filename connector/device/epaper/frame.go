package epaper

import (
	"image"
	"image/color"
)

// Frame is a black-and-white picture as a person reads the panel: landscape, Width
// across and Height down. It is a draw.Image, so text and shapes are drawn onto it
// with the standard library; a colour darker than middle grey is black.
type Frame struct {
	black [Width * Height]bool
}

// NewFrame returns a white frame.
func NewFrame() *Frame { return &Frame{} }

func (f *Frame) ColorModel() color.Model { return color.GrayModel }
func (f *Frame) Bounds() image.Rectangle { return image.Rect(0, 0, Width, Height) }

// At reports a pixel as black or white.
func (f *Frame) At(x, y int) color.Color {
	if f.Black(x, y) {
		return color.Gray{Y: 0}
	}
	return color.Gray{Y: 0xFF}
}

// Set paints a pixel; outside the frame it does nothing.
func (f *Frame) Set(x, y int, c color.Color) {
	if x < 0 || y < 0 || x >= Width || y >= Height {
		return
	}
	f.black[y*Width+x] = color.GrayModel.Convert(c).(color.Gray).Y < 0x80
}

// Black reports whether a pixel is black; outside the frame it is white.
func (f *Frame) Black(x, y int) bool {
	if x < 0 || y < 0 || x >= Width || y >= Height {
		return false
	}
	return f.black[y*Width+x]
}

// Rect paints the outline of a rectangle black, one pixel wide.
func (f *Frame) Rect(r image.Rectangle) {
	for x := r.Min.X; x < r.Max.X; x++ {
		f.Set(x, r.Min.Y, color.Black)
		f.Set(x, r.Max.Y-1, color.Black)
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		f.Set(r.Min.X, y, color.Black)
		f.Set(r.Max.X-1, y, color.Black)
	}
}

// encode lays the frame out as the controller's memory holds it: one controller row
// per column of the landscape picture, 16 bytes each, most significant bit first,
// a set bit white. A panel that fills its memory from the last row has the rows in
// reverse. This is what Waveshare's getbuffer produces for a landscape image.
func (f *Frame) encode(reversed bool) []byte {
	buf := make([]byte, BufferSize)
	for i := range buf {
		buf[i] = 0xFF
	}
	for x := 0; x < Width; x++ {
		row := Width - 1 - x
		if reversed {
			row = x
		}
		for y := 0; y < Height; y++ {
			if f.black[y*Width+x] {
				buf[row*rowBytes+y/8] &^= 0x80 >> (y % 8)
			}
		}
	}
	return buf
}
