package soxchrome

import (
	"image/color"
	"math"
	"strings"
	"testing"

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

// callBudget is how many drawing calls a bounded surface allows before it
// aborts; a real chrome draw makes a few hundred.
const callBudget = 100000

// errBudget is the panic value of a bounded surface that ran out of calls.
type errBudget struct{}

// boundedSurface counts drawing calls and panics with errBudget past callBudget,
// so a drawing loop that never ends fails a test instead of hanging it.
type boundedSurface struct{ calls int }

func (b *boundedSurface) spend() {
	b.calls++
	if b.calls > callBudget {
		panic(errBudget{})
	}
}
func (b *boundedSurface) Set(int, int, uint8)             { b.spend() }
func (b *boundedSurface) PrintAt(int, int, uint8, string) { b.spend() }
func (b *boundedSurface) PrintUp(int, int, uint8, string) { b.spend() }

// drawsWithinBudget reports whether DrawChrome returns for p within callBudget calls.
func drawsWithinBudget(p ChromeParams) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			if _, isBudget := r.(errBudget); !isBudget {
				panic(r)
			}
			ok = false
		}
	}()
	DrawChrome(&boundedSurface{}, p)
	return true
}

// Level ranges, axis lengths and sizes that are zero, negative, NaN, infinite or
// too large for int arithmetic must not hang a drawing loop. Converting an
// infinite float to int saturates on some architectures and wraps on others, so
// several of these cases only fail where it saturates (arm64).
func TestDrawChromeNonFiniteInputsTerminate(t *testing.T) {
	base := ChromeParams{Cols: 258, Rows: 129, SampleRate: 24000, Seconds: 2, RangeDB: 100}
	cases := map[string]func(p *ChromeParams){
		"zero range":                func(p *ChromeParams) { p.RangeDB = 0 },
		"negative range":            func(p *ChromeParams) { p.RangeDB = -10 },
		"NaN range":                 func(p *ChromeParams) { p.RangeDB = math.NaN() },
		"infinite range":            func(p *ChromeParams) { p.RangeDB = math.Inf(1) },
		"infinite time":             func(p *ChromeParams) { p.Seconds = math.Inf(1) },
		"NaN time":                  func(p *ChromeParams) { p.Seconds = math.NaN() },
		"tiny time":                 func(p *ChromeParams) { p.Seconds = math.SmallestNonzeroFloat64 },
		"huge time":                 func(p *ChromeParams) { p.Seconds = math.MaxFloat64 },
		"huge time, no tick budget": func(p *ChromeParams) { p.Seconds, p.Cols = 1e25, 0 },
		"huge rate, no tick budget": func(p *ChromeParams) { p.SampleRate, p.Rows = 1<<62, 1 },
		"zero time":                 func(p *ChromeParams) { p.Seconds = 0 },
		"zero sample rate":          func(p *ChromeParams) { p.SampleRate = 0 },
		"negative columns":          func(p *ChromeParams) { p.Cols = -100 },
		"negative rows":             func(p *ChromeParams) { p.Rows = -100 },
		"huge range":                func(p *ChromeParams) { p.RangeDB = 1e300 },
		"range near 2^63":           func(p *ChromeParams) { p.RangeDB = 9e18 },
		"range past 2^63":           func(p *ChromeParams) { p.RangeDB = 2.5e20 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := base
			mutate(&p)
			assert.True(t, drawsWithinBudget(p), "DrawChrome made more than %d calls", callBudget)
		})
	}
}

// Whatever the length and tick budget, the scale must be one the tick loops can
// finish: a positive step and a finite limit that is a bounded number of steps
// away.
func TestAxisScaleAlwaysUsable(t *testing.T) {
	const maxTicks = 1e6
	lengths := []float64{math.Inf(1), math.Inf(-1), math.NaN(), math.MaxFloat64, math.SmallestNonzeroFloat64, 1e25, 1e-300, 0, -5, 2}
	for _, to := range lengths {
		for _, maxSteps := range []int{-1, 0, 10, 1000} {
			step, limit, _ := axisScale(to, maxSteps)
			require.GreaterOrEqual(t, step, 1, "length %v maxSteps %d", to, maxSteps)
			require.False(t, math.IsNaN(limit) || math.IsInf(limit, 0), "length %v maxSteps %d limit %v", to, maxSteps, limit)
			require.LessOrEqual(t, limit/float64(step), maxTicks, "length %v maxSteps %d", to, maxSteps)
		}
	}
}

// A length that cannot be scaled returns the degenerate axis: one step, no limit.
func TestAxisScaleUnusableLength(t *testing.T) {
	for _, to := range []float64{math.Inf(1), math.Inf(-1), math.NaN(), math.MaxFloat64, math.SmallestNonzeroFloat64} {
		step, limit, prefix := axisScale(to, 10)
		assert.Equal(t, 1, step, "length %v", to)
		assert.InDelta(t, 0, limit, 0, "length %v", to)
		assert.Empty(t, prefix, "length %v", to)
	}
	step, limit, prefix := axisScale(1e25, 0)
	assert.Equal(t, 1, step)
	assert.InDelta(t, 0, limit, 0)
	assert.Empty(t, prefix)
}
