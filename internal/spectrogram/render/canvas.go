package render

import (
	"image"

	"github.com/tphakala/birdnet-go/internal/spectrogram/soxchrome"
)

// canvas draws on a paletted image through bottom-up coordinates (y = 0 is the
// bottom image row), implementing soxchrome.Surface.
type canvas struct {
	img   *image.Paletted
	atlas *glyphAtlas
}

// Set writes one pixel, dropping writes outside the image.
func (c *canvas) Set(x, y int, idx uint8) {
	b := c.img.Rect
	row := b.Dy() - 1 - y
	if x < 0 || x >= b.Dx() || row < 0 || row >= b.Dy() {
		return
	}
	c.img.Pix[row*c.img.Stride+x] = idx
}

// PrintAt draws text left to right: cell row r is at bottom-up row y-r and cell
// column k at x+k, advancing CellW per character.
func (c *canvas) PrintAt(x, y int, idx uint8, s string) {
	for i := range len(s) {
		g := c.atlas.glyph(s[i])
		for r, bits := range g {
			for k := range soxchrome.CellW {
				if bits&(1<<(soxchrome.CellW-1-k)) != 0 {
					c.Set(x+k, y-r, idx)
				}
			}
		}
		x += soxchrome.CellW
	}
}

// PrintUp draws text rotated to read upward: cell row r is at column x+r and cell
// column k at bottom-up row y+k, advancing CellW per character.
func (c *canvas) PrintUp(x, y int, idx uint8, s string) {
	for i := range len(s) {
		g := c.atlas.glyph(s[i])
		for r, bits := range g {
			for k := range soxchrome.CellW {
				if bits&(1<<(soxchrome.CellW-1-k)) != 0 {
					c.Set(x+r, y+k, idx)
				}
			}
		}
		y += soxchrome.CellW
	}
}
