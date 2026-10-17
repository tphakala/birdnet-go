package render

import (
	"image"
	"image/png"
	"io"
)

// Encoder writes a rendered image in one file format. It is the seam for formats
// other than PNG.
type Encoder interface {
	// Encode writes img to w.
	Encode(w io.Writer, img image.Image) error
}

// PNG encodes images as PNG with the standard library defaults.
type PNG struct{}

// Encode writes img to w as a PNG.
func (PNG) Encode(w io.Writer, img image.Image) error {
	enc := png.Encoder{}
	return enc.Encode(w, img)
}
