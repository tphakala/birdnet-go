// Package render draws the sox-style spectrogram image in Go. The numerics come
// from go-audio-dsp spectrogram.Fit; the palette and chrome layout come from
// soxchrome; this package wires them to a paletted canvas.
package render

import (
	"context"
	"fmt"
	"image"

	"github.com/tphakala/go-audio-dsp/spectrogram"
	"github.com/tphakala/go-audio-dsp/stft"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/spectrogram/soxchrome"
)

// Range limits for Options.RangeDB, the same as sox -z.
const (
	minRangeDB = 20
	maxRangeDB = 180

	// minRows is the smallest raster height Fit can analyse.
	minRows = 2

	// maxWidth and maxRows bound the raster, and with it the canvas and analysis
	// frame allocations. The largest preset is 2050 columns by 1025 rows; 4097 is
	// the height the generator derives for maxWidth.
	maxWidth = 8192
	maxRows  = 4097
)

// Sentinel errors. Render wraps them; test with errors.Is.
var (
	// ErrInvalidOptions marks options outside what the renderer accepts.
	ErrInvalidOptions = errors.NewStd("invalid spectrogram options")
	// ErrEmptyClip marks a source with no samples or no sample rate.
	ErrEmptyClip = errors.NewStd("spectrogram source holds no audio")
	// ErrNoColumns marks a clip too short to produce a single column.
	ErrNoColumns = errors.NewStd("clip too short for a spectrogram column")
)

// Source supplies the mono samples to analyse.
type Source interface {
	// SampleRate returns the rate in Hz of the streamed samples.
	SampleRate() int
	// SpanSamples returns the clip length in samples at SampleRate.
	SpanSamples() int64
	// Stream delivers the samples in order. fn must not keep the slice.
	Stream(ctx context.Context, fn func([]float32) error) error
}

// Options configures one render.
type Options struct {
	// Width is the column limit of the raster, from soxchrome.MinWidth to 8192.
	Width int
	// Rows is the raster height, 2 to 4097; the analysis frame is 2*(Rows-1), so
	// Rows-1 must be a power of two.
	Rows int
	// Raw draws the raster alone, without axes and legend.
	Raw bool
	// RangeDB is the display dynamic range in dB, 20 to 180.
	RangeDB int
	// Style selects the window and palette.
	Style Style
}

// Render draws the spectrogram of src. It returns the image, cropped to the
// columns the analysis produced, and that column count (at most Options.Width).
// Context errors are returned unchanged. Render is safe for concurrent use.
func Render(ctx context.Context, src Source, opt Options) (img image.Image, cols int, err error) {
	if err := validate(opt); err != nil {
		return nil, 0, err
	}
	rate, span := src.SampleRate(), src.SpanSamples()
	if rate <= 0 || span <= 0 {
		return nil, 0, ErrEmptyClip
	}
	gl, err := atlas()
	if err != nil {
		return nil, 0, err
	}

	fit, err := newFit(rate, span, opt)
	if err != nil {
		return nil, 0, err
	}

	fullW, fullH := soxchrome.Dims(opt.Width, opt.Rows, opt.Raw)
	pal := paletteFor(opt.Style.Palette)
	canvasImg := image.NewPaletted(image.Rect(0, 0, fullW, fullH), pal)
	cv := &canvas{img: canvasImg, atlas: gl}
	x0, y0 := 0, 0
	if !opt.Raw {
		x0, y0 = soxchrome.MarginLeft, soxchrome.MarginBelow
	}
	rangeDB := float64(opt.RangeDB)
	onColumn := func(col []float32, i int) {
		for j, v := range col {
			cv.Set(x0+i, y0+j, soxchrome.ColourIndex(v, rangeDB))
		}
		cols = i + 1
	}
	// No early stop: Fit discards input past the last useful sample, and stopping
	// would skip the resampler flush.
	if err := src.Stream(ctx, func(b []float32) error {
		fit.Feed(b, onColumn)
		return nil
	}); err != nil {
		return nil, 0, err
	}
	fit.Finish(onColumn)
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if cols == 0 {
		return nil, 0, ErrNoColumns
	}

	if !opt.Raw {
		soxchrome.DrawChrome(cv, soxchrome.ChromeParams{
			Cols: cols, Rows: opt.Rows, SampleRate: rate,
			Seconds: fit.SecondsSpanned(cols), RangeDB: rangeDB,
		})
	}
	w, h := soxchrome.Dims(cols, opt.Rows, opt.Raw)
	return canvasImg.SubImage(image.Rect(0, 0, w, h)), cols, nil
}

// validate checks the options.
func validate(opt Options) error {
	switch {
	case opt.Width < soxchrome.MinWidth:
		return fmt.Errorf("%w: width %d is below %d", ErrInvalidOptions, opt.Width, soxchrome.MinWidth)
	case opt.Width > maxWidth:
		return fmt.Errorf("%w: width %d is above %d", ErrInvalidOptions, opt.Width, maxWidth)
	case opt.Rows > maxRows:
		return fmt.Errorf("%w: rows %d is above %d", ErrInvalidOptions, opt.Rows, maxRows)
	case opt.Rows < minRows:
		return fmt.Errorf("%w: rows %d is below %d", ErrInvalidOptions, opt.Rows, minRows)
	case opt.RangeDB < minRangeDB || opt.RangeDB > maxRangeDB:
		return fmt.Errorf("%w: range %d dB is outside %d..%d", ErrInvalidOptions, opt.RangeDB, minRangeDB, maxRangeDB)
	}
	return nil
}

// newFit builds and resets the analyser for one render. A configuration Fit
// rejects is reported as ErrInvalidOptions with the cause kept.
func newFit(rate int, span int64, opt Options) (*spectrogram.Fit, error) {
	window := stft.WindowShape{}
	if opt.Style.Window == WindowDolph {
		window = stft.WindowShape{Family: stft.ShapeDolphChebyshev, AttenuationDB: spectrogram.DolphAttenuationDB(float64(opt.RangeDB), 0, 0)}
	}
	fit, err := spectrogram.NewFit(spectrogram.FitConfig{
		SampleRate: rate,
		FrameSize:  2 * (opt.Rows - 1),
		Columns:    opt.Width,
		Window:     window,
	})
	if err == nil {
		err = fit.Reset(span)
	}
	if err != nil {
		if errors.Is(err, spectrogram.ErrInvalidConfig) {
			return nil, fmt.Errorf("%w: %w", ErrInvalidOptions, err)
		}
		return nil, err
	}
	return fit, nil
}
