package soxchrome

import (
	"image/color"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Sizes measured from the sox binary: raster columns and rows per bird width.
var (
	testWidths = []int{100, 258, 514, 1026, 2050}
	testRows   = []int{65, 129, 257, 513, 1025}
)

func TestColourIndexTable(t *testing.T) {
	const rangeDB = 100.0
	tests := []struct {
		name string
		dB   float32
		want uint8
	}{
		{name: "just below the floor", dB: -rangeDB - 0.01, want: 4},
		{name: "at the floor", dB: -rangeDB, want: 5},
		{name: "full scale", dB: 0, want: 254},
		{name: "above full scale", dB: 5, want: 254},
		{name: "NaN is the floor", dB: float32(math.NaN()), want: 4},
		{name: "midpoint", dB: -rangeDB / 2, want: 129},
		{name: "just under full scale", dB: -0.001, want: 253},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ColourIndex(tt.dB, rangeDB))
		})
	}
}

func TestPaletteSizeAndFixedEntries(t *testing.T) {
	rgba := func(r, g, b uint8) color.Color { return color.RGBA{r, g, b, 0xff} }
	tests := []struct {
		name string
		spec PaletteSpec
		want [4]color.Color
	}{
		{name: "default", spec: PaletteSpec{}, want: [4]color.Color{rgba(0, 0, 0), rgba(0xff, 0xff, 0xff), rgba(0xbf, 0xbf, 0xbf), rgba(0x7f, 0x7f, 0x7f)}},
		{name: "high colour", spec: PaletteSpec{HighColour: true}, want: [4]color.Color{rgba(0, 0, 0), rgba(0xff, 0xff, 0xff), rgba(0xbf, 0xbf, 0xbf), rgba(0x7f, 0x7f, 0x7f)}},
		{name: "monochrome", spec: PaletteSpec{Monochrome: true}, want: [4]color.Color{rgba(0, 0, 0), rgba(0xff, 0xff, 0xff), rgba(0xbf, 0xbf, 0xbf), rgba(0x7f, 0x7f, 0x7f)}},
		{name: "monochrome light", spec: PaletteSpec{Monochrome: true, LightBackground: true}, want: [4]color.Color{rgba(0xdf, 0xdf, 0xdf), rgba(0, 0, 0), rgba(0x3f, 0x3f, 0x3f), rgba(0x3f, 0x3f, 0x3f)}},
		{name: "colour light", spec: PaletteSpec{LightBackground: true}, want: [4]color.Color{rgba(0xdd, 0xd8, 0xd0), rgba(0, 0, 0), rgba(0x3f, 0x3f, 0x3f), rgba(0x3f, 0x3f, 0x3f)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pal := Palette(tt.spec)
			require.Len(t, pal, 255)
			for i, want := range tt.want {
				assert.Equal(t, want, pal[i], "fixed entry %d", i)
			}
		})
	}
}

func TestPaletteSpectrumShape(t *testing.T) {
	grey := Palette(PaletteSpec{Monochrome: true})
	assert.Equal(t, color.RGBA{0, 0, 0, 0xff}, grey[4], "monochrome starts at black")
	assert.Equal(t, color.RGBA{0xff, 0xff, 0xff, 0xff}, grey[254], "monochrome ends at white")

	light := Palette(PaletteSpec{Monochrome: true, LightBackground: true})
	assert.Equal(t, color.RGBA{0xff, 0xff, 0xff, 0xff}, light[4], "light background inverts the levels")
	assert.Equal(t, color.RGBA{0, 0, 0, 0xff}, light[254])

	def := Palette(PaletteSpec{})
	assert.Equal(t, color.RGBA{0, 0, 0, 0xff}, def[4], "default starts at black")
	assert.Equal(t, color.RGBA{0xff, 0xff, 0xff, 0xff}, def[254], "default ends at white")
	assert.NotEqual(t, def[130], Palette(PaletteSpec{HighColour: true})[130], "high colour differs from default")
}

func TestAxisScaleCases(t *testing.T) {
	tests := []struct {
		name      string
		to        float64
		maxSteps  int
		wantStep  int
		wantLimit float64
		wantPre   string
	}{
		{name: "15 s over 1026 px", to: 15, maxSteps: 1026 / 27, wantStep: 5, wantLimit: 150},
		{name: "2 s over 258 px", to: 2, maxSteps: 258 / 27, wantStep: 5, wantLimit: 20},
		{name: "12 kHz over 513 rows", to: 12000, maxSteps: 512 / 18, wantStep: 5, wantLimit: 120, wantPre: "k"},
		{name: "128 kHz over 1025 rows", to: 128000, maxSteps: 1024 / 18, wantStep: 50, wantLimit: 1280, wantPre: "k"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step, limit, prefix := axisScale(tt.to, tt.maxSteps)
			assert.Equal(t, tt.wantStep, step)
			assert.InDelta(t, tt.wantLimit, limit, 1e-6)
			assert.Equal(t, tt.wantPre, prefix)
		})
	}
}

func TestDims(t *testing.T) {
	// Non-raw sizes measured from sox 14.4.2: sm 402x207, md 658x335, lg 1170x591,
	// xl 2194x1103 for rows 129, 257, 513, 1025.
	measured := []struct{ cols, rows, w, h int }{
		{258, 129, 402, 207},
		{514, 257, 658, 335},
		{1026, 513, 1170, 591},
		{2050, 1025, 2194, 1103},
	}
	for _, m := range measured {
		w, h := Dims(m.cols, m.rows, false)
		assert.Equal(t, m.w, w)
		assert.Equal(t, m.h, h)
		w, h = Dims(m.cols, m.rows, true)
		assert.Equal(t, m.cols, w)
		assert.Equal(t, m.rows, h)
	}
	w, h := Dims(100, 65, false)
	assert.Equal(t, 244, w)
	assert.Equal(t, 143, h)
}

// textCall is one recorded PrintAt or PrintUp.
type textCall struct {
	x, y int
	idx  uint8
	s    string
	up   bool
}

// recorder is a Surface that records every call.
type recorder struct {
	sets  [][2]int
	texts []textCall
}

func (r *recorder) Set(x, y int, _ uint8) { r.sets = append(r.sets, [2]int{x, y}) }
func (r *recorder) PrintAt(x, y int, idx uint8, s string) {
	r.texts = append(r.texts, textCall{x: x, y: y, idx: idx, s: s})
}
func (r *recorder) PrintUp(x, y int, idx uint8, s string) {
	r.texts = append(r.texts, textCall{x: x, y: y, idx: idx, s: s, up: true})
}

// rect returns the pixel rectangle [x0,x1) x [y0,y1) a text call can cover.
func (c textCall) rect() (x0, y0, x1, y1 int) {
	n := len(c.s) * CellW
	if c.up {
		return c.x, c.y, c.x + CellH, c.y + n
	}
	return c.x, c.y - CellH + 1, c.x + n, c.y + 1
}

func TestDrawChromeStaysOutsideRaster(t *testing.T) {
	for i, cols := range testWidths {
		rows := testRows[i]
		for _, rangeDB := range []float64{80, 100, 120} {
			for _, rate := range []int{24000, 256000} {
				var r recorder
				DrawChrome(&r, ChromeParams{Cols: cols, Rows: rows, SampleRate: rate, Seconds: 15, RangeDB: rangeDB})
				w, h := Dims(cols, rows, false)
				inRaster := func(x, y int) bool {
					return x >= MarginLeft && x < MarginLeft+cols && y >= MarginBelow && y < MarginBelow+rows
				}
				for _, p := range r.sets {
					require.Falsef(t, inRaster(p[0], p[1]), "set inside raster at %v (cols %d range %v rate %d)", p, cols, rangeDB, rate)
					require.Truef(t, p[0] >= 0 && p[0] < w && p[1] >= 0 && p[1] < h, "set outside image at %v", p)
				}
				for _, c := range r.texts {
					x0, y0, x1, y1 := c.rect()
					overlap := x0 < MarginLeft+cols && x1 > MarginLeft && y0 < MarginBelow+rows && y1 > MarginBelow
					require.Falsef(t, overlap, "text %q overlaps the raster (cols %d)", c.s, cols)
					require.Truef(t, x0 >= 0 && x1 <= w && y1 <= h, "text %q outside the image: x %d..%d y %d..%d of %dx%d", c.s, x0, x1, y0, y1, w, h)
				}
			}
		}
	}
}

func TestDrawChromeDrawsDCAndBothSides(t *testing.T) {
	const cols, rows = 258, 129
	var r recorder
	DrawChrome(&r, ChromeParams{Cols: cols, Rows: rows, SampleRate: 24000, Seconds: 2, RangeDB: 100})

	var left, right, below, above, titles bool
	for _, c := range r.texts {
		switch {
		case c.s == "   DC" && c.x == MarginLeft-4-CellW*5:
			left = true
		case c.s == "DC" && c.x == MarginLeft+cols+6:
			right = true
		case c.idx == IndexLabels && c.y == MarginBelow-6:
			below = true
		case c.idx == IndexLabels && c.y == MarginBelow+rows+14:
			above = true
		}
		if strings.HasPrefix(c.s, "Time (") || strings.HasPrefix(c.s, "Frequency (") || c.s == "dBFS" {
			assert.Equal(t, IndexText, c.idx, c.s)
			titles = true
		}
	}
	assert.True(t, left, "left DC label")
	assert.True(t, right, "right DC label")
	assert.True(t, below, "tick labels under the raster")
	assert.True(t, above, "tick labels over the raster")
	assert.True(t, titles, "axis and legend titles")
	texts := make([]string, 0, len(r.texts))
	for _, c := range r.texts {
		texts = append(texts, c.s)
	}
	assert.Contains(t, texts, "Time (s)")
	assert.Contains(t, texts, "Frequency (kHz)")
	assert.Contains(t, texts, "-100")
	assert.Contains(t, texts, "+0")
}

// A zero-length axis must still yield a finite scale, so the DC tick is drawn
// instead of the loop bound turning into NaN.
func TestAxisScaleZeroLength(t *testing.T) {
	step, limit, prefix := axisScale(0, 10)
	assert.Equal(t, 1, step)
	assert.InDelta(t, 0, limit, 0)
	assert.Empty(t, prefix)
}

// A non-positive level range must not hang the legend loop.
func TestDrawChromeZeroRangeTerminates(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		var r recorder
		DrawChrome(&r, ChromeParams{Cols: 258, Rows: 129, SampleRate: 24000, Seconds: 2, RangeDB: 0})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("DrawChrome did not return for RangeDB 0")
	}
}
