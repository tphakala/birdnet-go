//go:build normcompare

package normbench

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
)

// TestLoudnormFilter pins the reference loudnorm chain this harness builds.
//
// It exists because the two tests that used to pin this logic
// (TestBuildTwoPassLoudnormFilter and TestLoudnormGateFallbackOffset_NilStats)
// were deleted along with the production code they covered, leaving the
// reimplementation in loudnormFilterFromStats unpinned. That is the one piece of
// this package that must not drift silently: it produces the "before" column
// every measurement in the loudnorm-removal work is quoted from. A typo that
// dropped linear=true, or reordered the measured_* fields, would put FFmpeg into
// dynamic mode and the harness would report a fabricated improvement rather than
// a real one.
//
// It needs no ffmpeg binary and no corpus, so unlike the rest of the package it
// is a genuine unit test.
func TestLoudnormFilter(t *testing.T) {
	t.Parallel()

	// Base targets, shared by every branch.
	const base = "loudnorm=I=-23.0:TP=-2.0:LRA=7.0"

	usable := &ffmpeg.LoudnessStats{
		InputI:       "-15.0",
		InputTP:      "-1.0",
		InputLRA:     "5.0",
		InputThresh:  "-25.0",
		TargetOffset: "0.5",
	}

	t.Run("two-pass when every measured field is finite", func(t *testing.T) {
		t.Parallel()
		got := loudnormFilterFromStats(usable)

		// linear=true is the whole point: without it FFmpeg is free to compress,
		// which is precisely the behaviour the native path deliberately does not
		// reproduce. If this assertion ever goes, the comparison is meaningless.
		assert.Contains(t, got, "linear=true")
		assert.Equal(t,
			base+":measured_I=-15.0:measured_LRA=5.0:measured_TP=-1.0:measured_thresh=-25.0:linear=true:offset=0.5",
			got,
			"field order and naming must match the removed buildTwoPassLoudnormFilter exactly")
	})

	t.Run("nil stats fall back to single pass", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, base, loudnormFilterFromStats(nil))
	})

	t.Run("gated clip anchors an offset to true-peak headroom", func(t *testing.T) {
		t.Parallel()
		// R128 gated the clip to nothing, so input_i and input_thresh are -inf,
		// but the true peak is still measurable. Offset is targetTruePeak minus
		// the measured peak: -2.0 - (-30.0) = +28.0.
		got := loudnormFilterFromStats(&ffmpeg.LoudnessStats{
			InputI:       "-inf",
			InputTP:      "-30.0",
			InputLRA:     "0.0",
			InputThresh:  "-inf",
			TargetOffset: "0.0",
		})
		assert.Equal(t, base+":offset=28.0", got)
		assert.NotContains(t, got, "linear=true", "the gated fallback is single-pass")
	})

	t.Run("gated clip with no measurable peak falls back to single pass", func(t *testing.T) {
		t.Parallel()
		// Digital silence: nothing to anchor to.
		assert.Equal(t, base, loudnormFilterFromStats(&ffmpeg.LoudnessStats{
			InputI:       "-inf",
			InputTP:      "-inf",
			InputLRA:     "0.0",
			InputThresh:  "-inf",
			TargetOffset: "0.0",
		}))
	})

	t.Run("offset inside the dead band is dropped", func(t *testing.T) {
		t.Parallel()
		// -2.0 - (-2.02) = 0.02, below the 0.05 dead band the removed
		// loudnormGateFallbackOffset applied, so no offset is emitted.
		assert.Equal(t, base, loudnormFilterFromStats(&ffmpeg.LoudnessStats{
			InputI:       "-inf",
			InputTP:      "-2.02",
			InputLRA:     "0.0",
			InputThresh:  "-inf",
			TargetOffset: "0.0",
		}))
	})

	t.Run("offset is clamped to the ffmpeg ceiling", func(t *testing.T) {
		t.Parallel()
		// -2.0 - (-200.0) = 198, far past ffmpeg.MaxGainDB (60).
		assert.Equal(t, base+":offset=60.0", loudnormFilterFromStats(&ffmpeg.LoudnessStats{
			InputI:       "-inf",
			InputTP:      "-200.0",
			InputLRA:     "0.0",
			InputThresh:  "-inf",
			TargetOffset: "0.0",
		}))
	})

	t.Run("an unparseable field is not usable", func(t *testing.T) {
		t.Parallel()
		// The removed path gated on all five fields, not just input_i. A clip
		// whose input_i parses but whose thresh does not must take the fallback,
		// otherwise the harness overstates how often two-pass really ran.
		assert.Equal(t, base+":offset=28.0", loudnormFilterFromStats(&ffmpeg.LoudnessStats{
			InputI:       "-15.0",
			InputTP:      "-30.0",
			InputLRA:     "5.0",
			InputThresh:  "n/a",
			TargetOffset: "0.5",
		}))
	})
}
