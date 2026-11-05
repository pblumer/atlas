package epaper

import (
	"fmt"
	"image"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// The Go fonts cover Latin-1 and beyond, so a German or French status text renders
// with its Umlauts and accents, which no built-in bitmap font of a microcontroller
// does. They ship with golang.org/x/image under a BSD licence.
var (
	parseOnce     sync.Once
	regular, bold *opentype.Font
	parseErr      error
)

func fonts() (*opentype.Font, *opentype.Font, error) {
	parseOnce.Do(func() {
		if regular, parseErr = opentype.Parse(goregular.TTF); parseErr == nil {
			bold, parseErr = opentype.Parse(gobold.TTF)
		}
	})
	return regular, bold, parseErr
}

// Text draws s with its baseline at (x, y), in the Go font at size pixels, bold or
// regular, and returns the point after its last glyph.
func (f *Frame) Text(x, y int, size float64, isBold bool, s string) (image.Point, error) {
	reg, b, err := fonts()
	if err != nil {
		return image.Point{}, fmt.Errorf("epaper: font: %w", err)
	}
	src := reg
	if isBold {
		src = b
	}
	face, err := opentype.NewFace(src, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return image.Point{}, fmt.Errorf("epaper: font face: %w", err)
	}
	defer face.Close()
	d := font.Drawer{Dst: f, Src: image.Black, Face: face, Dot: fixed.P(x, y)}
	d.DrawString(s)
	return image.Pt(d.Dot.X.Round(), d.Dot.Y.Round()), nil
}
