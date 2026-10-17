package render

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"math"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/go-audio-dsp/spectrogram"
	"github.com/tphakala/go-audio-dsp/stft"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/spectrogram/soxchrome"
)

const (
	testStreamChunk = 4096
	testRangeDB     = 100

	// aboveMaxWidth is a width past the renderer's upper bound (8192).
	aboveMaxWidth = 8193
	// aboveMaxRows is a power of two plus one past the upper bound (4097).
	aboveMaxRows = 8193

	// burstEdgeTolerance is the allowed error, in columns, of a burst edge.
	burstEdgeTolerance = 3
)

// sliceSource is a Source over an in-memory signal.
type sliceSource struct {
	rate    int
	samples []float32
	// span overrides SpanSamples when non-zero.
	span int64
}

func (s *sliceSource) SampleRate() int { return s.rate }
func (s *sliceSource) SpanSamples() int64 {
	if s.span != 0 {
		return s.span
	}
	return int64(len(s.samples))
}
func (s *sliceSource) Stream(ctx context.Context, fn func([]float32) error) error {
	for off := 0; off < len(s.samples); off += testStreamChunk {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fn(s.samples[off:min(off+testStreamChunk, len(s.samples))]); err != nil {
			return err
		}
	}
	return nil
}

// rowsFor returns the raster height the generator uses for a width.
func rowsFor(width int) int {
	n := 1
	for n+1 < width/2 {
		n *= 2
	}
	return n + 1
}

// toneSource returns a sine of the given frequency and amplitude.
func toneSource(rate int, seconds, freq, amp float64) *sliceSource {
	n := int(seconds * float64(rate))
	x := make([]float32, n)
	for i := range x {
		x[i] = float32(amp * math.Sin(2*math.Pi*freq*float64(i)/float64(rate)))
	}
	return &sliceSource{rate: rate, samples: x}
}

// noiseSource returns deterministic pseudo-noise.
func noiseSource(rate int, seconds float64, seed uint32) *sliceSource {
	n := int(seconds * float64(rate))
	x := make([]float32, n)
	state := seed
	for i := range x {
		state = state*1664525 + 1013904223
		x[i] = float32(int32(state>>8)-(1<<23)) / (1 << 24)
	}
	return &sliceSource{rate: rate, samples: x}
}

func pix(img image.Image, x, y int) uint8 {
	return img.(*image.Paletted).ColorIndexAt(x, y)
}

func TestGlyphAtlasFitsSoxCell(t *testing.T) {
	a, err := atlas()
	require.NoError(t, err)
	for _, r := range chromeRunes {
		g := a.glyph(byte(r))
		for row, bits := range g {
			// Column 5 (the lowest bit) is the gap column and must stay empty.
			assert.Zerof(t, bits&1, "rune %q row %d uses the gap column", r, row)
		}
	}
	for _, r := range "0123456789+-.DCTimeFrqunckHzdBSy()" {
		var ink int
		for _, bits := range a.glyph(byte(r)) {
			for ; bits != 0; bits &= bits - 1 {
				ink++
			}
		}
		assert.Positivef(t, ink, "rune %q has no ink", r)
	}
	// Digits must not touch: ink within the five columns leaves one gap column.
	assert.Equal(t, a.glyph('?'), a.glyph(0x01), "unknown runes draw the fallback glyph")
}

func TestStyleForPresets(t *testing.T) {
	tests := []struct {
		name string
		want Style
	}{
		{conf.SpectrogramStyleDefault, Style{}},
		{conf.SpectrogramStyleScientificDark, Style{Window: WindowDolph, Palette: soxchrome.PaletteSpec{Monochrome: true}}},
		{conf.SpectrogramStyleHighContrastDark, Style{Palette: soxchrome.PaletteSpec{HighColour: true}}},
		{conf.SpectrogramStyleScientific, Style{Window: WindowDolph, Palette: soxchrome.PaletteSpec{Monochrome: true, LightBackground: true}}},
		{"", Style{}},
		{"bogus", Style{}},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, StyleFor(tt.name), tt.name)
	}
}

func TestRenderDims(t *testing.T) {
	src := toneSource(24000, 15, 3000, 0.5)
	for _, width := range []int{100, 258, 514, 1026, 2050} {
		for _, raw := range []bool{false, true} {
			rows := rowsFor(width)
			img, cols, err := Render(t.Context(), src, Options{Width: width, Rows: rows, Raw: raw, RangeDB: testRangeDB})
			require.NoError(t, err)
			w, h := soxchrome.Dims(cols, rows, raw)
			assert.Equal(t, image.Rect(0, 0, w, h), img.Bounds(), "width %d raw %v", width, raw)
			assert.LessOrEqual(t, cols, width)
		}
	}
}

func TestRenderColumnsMatchFit(t *testing.T) {
	durations := []float64{0.02, 0.1, 0.5, 1, 2.34, 7.77, 14.6, 20}
	widths := []int{100, 258, 514, 1026, 2050}
	for _, rate := range []int{24000, 256000} {
		for _, window := range []WindowKind{WindowHann, WindowDolph} {
			for _, width := range widths {
				for _, secs := range durations {
					if rate == 256000 && secs > 7 {
						continue // keep the table fast
					}
					n := int(secs * float64(rate))
					if n < 2 {
						continue
					}
					src := noiseSource(rate, secs, 7)
					rows := rowsFor(width)
					opt := Options{Width: width, Rows: rows, RangeDB: testRangeDB, Style: Style{Window: window}}
					img, cols, err := Render(t.Context(), src, opt)
					if errors.Is(err, ErrNoColumns) {
						continue // clip too short for this width; covered by TestRenderValidation
					}
					require.NoError(t, err)

					shape := stft.WindowShape{}
					if window == WindowDolph {
						shape = stft.WindowShape{Family: stft.ShapeDolphChebyshev, AttenuationDB: spectrogram.DolphAttenuationDB(testRangeDB, 0, 0)}
					}
					fit, err := spectrogram.NewFit(spectrogram.FitConfig{SampleRate: rate, FrameSize: 2 * (rows - 1), Columns: width, Window: shape})
					require.NoError(t, err)
					require.NoError(t, fit.Reset(int64(n)))
					assert.Equal(t, fit.NumColumns(int64(n)), cols, "rate %d width %d secs %v", rate, width, secs)
					assert.LessOrEqual(t, cols, width)
					w, _ := soxchrome.Dims(cols, rows, false)
					assert.Equal(t, w, img.Bounds().Dx())
				}
			}
		}
	}
}

func TestRenderCropKeepsLegendInside(t *testing.T) {
	// A short clip yields fewer columns than the width.
	src := noiseSource(24000, 0.1, 3)
	const width = 1026
	rows := rowsFor(width)
	img, cols, err := Render(t.Context(), src, Options{Width: width, Rows: rows, RangeDB: testRangeDB})
	require.NoError(t, err)
	require.Less(t, cols, width, "clip must be short enough to crop")

	w := img.Bounds().Dx()
	pal := img.(*image.Paletted)
	mid := rows/2 + soxchrome.MarginBelow
	imgRow := img.Bounds().Dy() - 1 - mid
	// The legend bar is LegendWidth columns wide, ending MarginRight+1 columns
	// from the right edge (sox cols - right - 1 - i, i < 14).
	for i := range soxchrome.LegendWidth {
		x := w - soxchrome.MarginRight - 1 - i
		assert.GreaterOrEqual(t, pal.ColorIndexAt(x, imgRow), soxchrome.IndexFirstSpectrum, "legend column %d", x)
	}
	assert.Equal(t, w-49, w-soxchrome.MarginRight-soxchrome.LegendWidth)
	// The pixel just left of the bar is background.
	assert.Equal(t, soxchrome.IndexBackground, pal.ColorIndexAt(w-soxchrome.MarginRight-soxchrome.LegendWidth-1, imgRow))
	// The cropped canvas keeps nothing beyond w.
	assert.Equal(t, w, pal.Bounds().Dx())
}

func TestRenderToneRow(t *testing.T) {
	tests := []struct {
		name         string
		rate         int
		freq, nyquis float64
	}{
		{name: "bird 3 kHz at 24 kHz", rate: 24000, freq: 3000, nyquis: 12000},
		{name: "bat 50 kHz at 256 kHz", rate: 256000, freq: 50000, nyquis: 128000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secs := 2.0
			if tt.rate > 100000 {
				secs = 0.5
			}
			src := toneSource(tt.rate, secs, tt.freq, 0.5)
			const width = 514
			rows := rowsFor(width)
			img, cols, err := Render(t.Context(), src, Options{Width: width, Rows: rows, Raw: true, RangeDB: testRangeDB})
			require.NoError(t, err)
			x := cols / 2
			best, bestRow := -1, -1
			for row := range rows {
				if v := int(pix(img, x, row)); v > best {
					best, bestRow = v, row
				}
			}
			// Image row 0 is Nyquist, so a frequency f sits at row (1 - f/nyq)*(rows-1).
			want := int(math.Round((1 - tt.freq/tt.nyquis) * float64(rows-1)))
			assert.InDelta(t, want, bestRow, 1)
		})
	}
}

func TestRenderBurstNearEnd(t *testing.T) {
	const rate, secs = 24000, 15.0
	src := noiseSource(rate, secs, 1)
	for i := range src.samples {
		if t0 := float64(i) / rate; t0 < 14.0 || t0 >= 14.5 {
			src.samples[i] = 0
		}
	}
	const width = 1026
	rows := rowsFor(width)
	img, cols, err := Render(t.Context(), src, Options{Width: width, Rows: rows, Raw: true, RangeDB: testRangeDB})
	require.NoError(t, err)

	// A column is lit when its mean palette index is above half of the loudest
	// column's: the window smears a burst edge over a few columns at low levels.
	means := make([]float64, cols)
	peak := 0.0
	for x := range cols {
		sum := 0
		for row := range rows {
			sum += int(pix(img, x, row))
		}
		means[x] = float64(sum) / float64(rows)
		peak = max(peak, means[x])
	}
	first, last := -1, -1
	for x, m := range means {
		if m > peak/2 {
			if first < 0 {
				first = x
			}
			last = x
		}
	}
	require.GreaterOrEqual(t, first, 0)
	// The 1024-sample analysis frame spans about three 351-sample columns, so
	// the edges of a burst land within a few columns of the exact position.
	assert.InDelta(t, 14.0/secs*float64(cols), float64(first), burstEdgeTolerance)
	assert.InDelta(t, 14.5/secs*float64(cols), float64(last), burstEdgeTolerance)
}

func TestRenderValidation(t *testing.T) {
	src := toneSource(24000, 2, 1000, 0.5)
	good := Options{Width: 258, Rows: 129, RangeDB: testRangeDB}
	tests := []struct {
		name string
		src  Source
		opt  func(*Options)
		want error
	}{
		{"width below minimum", src, func(o *Options) { o.Width = 99 }, ErrInvalidOptions},
		{"width above maximum", src, func(o *Options) { o.Width = aboveMaxWidth }, ErrInvalidOptions},
		{"rows above maximum", src, func(o *Options) { o.Rows = aboveMaxRows }, ErrInvalidOptions},
		{"range too small", src, func(o *Options) { o.RangeDB = 0 }, ErrInvalidOptions},
		{"range too large", src, func(o *Options) { o.RangeDB = 200 }, ErrInvalidOptions},
		{"rows not a power of two plus one", src, func(o *Options) { o.Rows = 130 }, ErrInvalidOptions},
		{"span zero", &sliceSource{rate: 24000, samples: src.samples, span: -1}, func(*Options) {}, ErrEmptyClip},
		{"no samples", &sliceSource{rate: 24000}, func(*Options) {}, ErrEmptyClip},
		{"one sample at lg", &sliceSource{rate: 24000, samples: []float32{0.5}}, func(o *Options) { o.Width, o.Rows = 1026, 513 }, ErrNoColumns},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opt := good
			tt.opt(&opt)
			_, _, err := Render(t.Context(), tt.src, opt)
			require.ErrorIs(t, err, tt.want)
		})
	}
}

func TestRenderCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, err := Render(ctx, toneSource(24000, 2, 1000, 0.5), Options{Width: 258, Rows: 129, RangeDB: testRangeDB})
	require.ErrorIs(t, err, context.Canceled)
}

// encoded renders and PNG-encodes, failing the test on error.
func encoded(t *testing.T, src Source, opt Options) []byte {
	t.Helper()
	img, _, err := Render(t.Context(), src, opt)
	require.NoError(t, err)
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func TestRenderRerunAndReorder(t *testing.T) {
	srcA := noiseSource(24000, 3, 5)
	srcB := toneSource(256000, 0.3, 40000, 0.4)
	optA := Options{Width: 258, Rows: 129, RangeDB: testRangeDB}
	optB := Options{Width: 514, Rows: 257, RangeDB: 120, Style: StyleFor(conf.SpectrogramStyleScientific)}

	first := encoded(t, srcA, optA)
	_ = encoded(t, srcB, optB)
	assert.Equal(t, first, encoded(t, srcA, optA), "rerun after another render")

	var wg sync.WaitGroup
	results := make([][]byte, 4)
	errs := make([]error, len(results))
	for i := range results {
		wg.Go(func() {
			src, opt := Source(srcA), optA
			if i%2 != 0 {
				src, opt = srcB, optB
			}
			// Workers return their errors: FailNow must not run off the test goroutine.
			img, _, err := Render(t.Context(), src, opt)
			if err == nil {
				var buf bytes.Buffer
				err = png.Encode(&buf, img)
				results[i] = buf.Bytes()
			}
			errs[i] = err
		})
	}
	wg.Wait()
	for i, err := range errs {
		require.NoError(t, err, "render %d", i)
	}
	assert.Equal(t, first, results[0])
	assert.Equal(t, first, results[2])
}

func TestRenderRangeChangesWindowAndLegend(t *testing.T) {
	src := noiseSource(24000, 2, 9)
	base := Options{Width: 258, Rows: 129, RangeDB: 80, Style: StyleFor(conf.SpectrogramStyleScientificDark)}
	wide := base
	wide.RangeDB = 120
	a, b := encoded(t, src, base), encoded(t, src, wide)
	assert.NotEqual(t, a, b, "range changes the Dolph window and the legend labels")

	imgA, _, err := Render(t.Context(), src, base)
	require.NoError(t, err)
	imgB, _, err := Render(t.Context(), src, wide)
	require.NoError(t, err)
	// The bar is normalized to the range, so only the level labels to its right
	// tell the ranges apart.
	w, h := imgA.Bounds().Dx(), imgA.Bounds().Dy()
	differs := false
	for y := range h {
		for x := w - soxchrome.MarginRight + 1; x < w; x++ {
			if pix(imgA, x, y) != pix(imgB, x, y) {
				differs = true
			}
		}
	}
	assert.True(t, differs, "legend labels must change with the range")
}

func TestRenderTimeAxisUsesColumnsProduced(t *testing.T) {
	// A short clip yields fewer columns than the width, so the seconds the axis
	// spans must come from the columns produced, not from the width.
	const width, rows = 1026, 513
	src := noiseSource(24000, 0.1, 3)
	opt := Options{Width: width, Rows: rows, RangeDB: testRangeDB}
	img, cols, err := Render(t.Context(), src, opt)
	require.NoError(t, err)
	require.Less(t, cols, width)

	fit, err := newFit(src.rate, src.SpanSamples(), opt)
	require.NoError(t, err)
	gl, err := atlas()
	require.NoError(t, err)
	w, h := soxchrome.Dims(cols, rows, false)
	want := image.NewPaletted(image.Rect(0, 0, w, h), soxchrome.Palette(opt.Style.Palette))
	soxchrome.DrawChrome(&canvas{img: want, atlas: gl}, soxchrome.ChromeParams{
		Cols: cols, Rows: rows, SampleRate: src.rate, Seconds: fit.SecondsSpanned(cols), RangeDB: testRangeDB,
	})

	got := img.(*image.Paletted)
	for y := range h {
		for x := range w {
			inRaster := x >= soxchrome.MarginLeft && x < soxchrome.MarginLeft+cols &&
				y >= soxchrome.MarginAbove && y < soxchrome.MarginAbove+rows
			if !inRaster {
				require.Equalf(t, want.ColorIndexAt(x, y), got.ColorIndexAt(x, y), "chrome pixel %d,%d", x, y)
			}
		}
	}
}

func TestCanvasTextOrientation(t *testing.T) {
	a, err := atlas()
	require.NoError(t, err)
	const size = 40
	newCanvas := func() (*canvas, *image.Paletted) {
		img := image.NewPaletted(image.Rect(0, 0, size, size), soxchrome.Palette(soxchrome.PaletteSpec{}))
		return &canvas{img: img, atlas: a}, img
	}
	g := a.glyph('L')
	ink := 0
	for _, bits := range g {
		for ; bits != 0; bits &= bits - 1 {
			ink++
		}
	}
	require.Positive(t, ink)
	set := func(bits uint8, k int) bool { return bits&(1<<(soxchrome.CellW-1-k)) != 0 }

	t.Run("PrintAt reads left to right, top row first, y up", func(t *testing.T) {
		cv, img := newCanvas()
		const x, y = 5, 20
		cv.PrintAt(x, y, soxchrome.IndexText, "L")
		for r, bits := range g {
			for k := range soxchrome.CellW {
				got := img.ColorIndexAt(x+k, size-1-(y-r)) == soxchrome.IndexText
				require.Equalf(t, set(bits, k), got, "cell row %d column %d", r, k)
			}
		}
	})
	t.Run("PrintUp reads upward with the cell top on the left", func(t *testing.T) {
		cv, img := newCanvas()
		const x, y = 10, 5
		cv.PrintUp(x, y, soxchrome.IndexText, "L")
		for r, bits := range g {
			for k := range soxchrome.CellW {
				got := img.ColorIndexAt(x+r, size-1-(y+k)) == soxchrome.IndexText
				require.Equalf(t, set(bits, k), got, "cell row %d column %d", r, k)
			}
		}
	})
}

// cancelAfterStream cancels its context once the samples are delivered, then
// reports success, as a source that does not watch the context would.
type cancelAfterStream struct {
	sliceSource
	cancel context.CancelFunc
}

func (s *cancelAfterStream) Stream(ctx context.Context, fn func([]float32) error) error {
	err := s.sliceSource.Stream(ctx, fn)
	s.cancel()
	return err
}

func TestRenderCancelDuringLastChunk(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	src := &cancelAfterStream{sliceSource: *toneSource(24000, 2, 1000, 0.5), cancel: cancel}

	_, _, err := Render(ctx, src, Options{Width: 258, Rows: 129, RangeDB: testRangeDB})

	require.ErrorIs(t, err, context.Canceled)
}
