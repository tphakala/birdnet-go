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
	c.print(s, idx, func(i, r, k int) (int, int) { return x + i*soxchrome.CellW + k, y - r })
}

// PrintUp draws text rotated to read upward: cell row r is at column x+r and cell
// column k at bottom-up row y+k, advancing CellW per character.
func (c *canvas) PrintUp(x, y int, idx uint8, s string) {
	c.print(s, idx, func(i, r, k int) (int, int) { return x + r, y + i*soxchrome.CellW + k })
}

// print sets every ink pixel of s, placing character i, cell row r and cell
// column k at the position place returns.
func (c *canvas) print(s string, idx uint8, place func(i, r, k int) (x, y int)) {
	for i := range len(s) {
		for r, bits := range c.atlas.glyph(s[i]) {
			for k := range soxchrome.CellW {
				if bits&(1<<(soxchrome.CellW-1-k)) != 0 {
					x, y := place(i, r, k)
					c.Set(x, y, idx)
				}
			}
		}
	}
}
