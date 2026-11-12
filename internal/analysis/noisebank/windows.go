package noisebank

import (
	"context"

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
