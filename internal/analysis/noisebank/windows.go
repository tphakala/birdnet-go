package noisebank

import (
	"context"
	"math"

	"github.com/tphakala/birdnet-go/internal/audiocore/transcode"
)

// monoStream is the part of a decoded clip the window reader needs.
type monoStream interface {
	Stream(ctx context.Context, fn func([]float32) error) error
}

var _ monoStream = (*transcode.MonoSource)(nil)

// windowGrid describes where the windows of a clip start: every hop samples from
// the first sample, each window samples long. The grid depends only on the model,
// never on the clip content, so the same clip always yields the same windows.
type windowGrid struct {
	window int
	hop    int
	rate   int
}

// count returns the number of whole windows a clip of span samples holds. The tail
// shorter than a window is not a window.
func (g windowGrid) count(span int64) int {
	if g.window <= 0 || g.hop <= 0 || span < int64(g.window) {
		return 0
	}
	return int((span-int64(g.window))/int64(g.hop)) + 1
}

// maxRangeSamples bounds a range edge before it is converted to samples, so an
// absurdly large request cannot overflow the conversion.
const maxRangeSamples = 1 << 53

// sampleSpan converts the ends of r to sample offsets at the grid's rate, rounded
// to the nearest sample so an edge written in decimal seconds still lands on the
// grid line it names.
func (g windowGrid) sampleSpan(r ClipRange) (from, to int64) {
	toSamples := func(seconds float64) int64 {
		return int64(math.Round(math.Min(math.Max(seconds*float64(g.rate), 0), maxRangeSamples)))
	}
	return toSamples(r.Start), toSamples(r.End)
}

// isFinite reports whether x is neither NaN nor infinite.
func isFinite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

// firstOffsetFrom returns the first window start of the grid at or after sample
// offset from.
func (g windowGrid) firstOffsetFrom(from int64) int64 {
	hop := int64(g.hop)
	return (from + hop - 1) / hop * hop
}

// hasWindowIn reports whether the grid has a window wholly inside r, wherever the
// clip ends. A range with an edge that is not a finite number holds none.
func (g windowGrid) hasWindowIn(r ClipRange) bool {
	if g.window <= 0 || g.hop <= 0 || g.rate <= 0 || !isFinite(r.Start) || !isFinite(r.End) {
		return false
	}
	from, to := g.sampleSpan(r)
	return g.firstOffsetFrom(from)+int64(g.window) <= to
}

// countIn returns the number of whole windows of a clip of span samples that lie
// wholly inside r.
func (g windowGrid) countIn(span int64, r ClipRange) int {
	if g.window <= 0 || g.hop <= 0 || g.rate <= 0 {
		return 0
	}
	from, to := g.sampleSpan(r)
	first := g.firstOffsetFrom(from)
	last := min(to, span) - int64(g.window)
	if last < first {
		return 0
	}
	return int((last-first)/int64(g.hop)) + 1
}

// offsetMs returns the start of the window at sample offset in milliseconds of
// clip time, rounded down.
func (g windowGrid) offsetMs(offset int64) int {
	return int(offset * msPerSecond / int64(g.rate))
}

// forEachWindow cuts the samples of src into the windows of the grid and calls fn
// for each, with the sample offset of its start. Every call gets a fresh slice
// that fn may keep. It stops at the first error from fn or the stream.
func forEachWindow(ctx context.Context, src monoStream, g windowGrid, fn func(offset int64, samples []float32) error) error {
	pending := make([]float32, 0, 2*g.window)
	var offset int64
	return src.Stream(ctx, func(chunk []float32) error {
		pending = append(pending, chunk...)
		for len(pending) >= g.window {
			if err := ctx.Err(); err != nil {
				return err
			}
			window := make([]float32, g.window)
			copy(window, pending)
			if err := fn(offset, window); err != nil {
				return err
			}
			n := copy(pending, pending[g.hop:])
			pending = pending[:n]
			offset += int64(g.hop)
		}
		return nil
	})
}
