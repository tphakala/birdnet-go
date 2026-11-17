package bsgbat

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Synthetic window: two tone bursts on a known span of frames, so each lands on a
// known mel bin. The bins depend on the sample rate, the mel range and scale, and
// the framing, which is what the test pins.
const (
	windowBurstAmp = 0.5
	// windowFirstBurstHz and windowSecondBurstHz are the burst frequencies, and the
	// frame spans are in frames of HopSize samples.
	windowFirstBurstHz   = 50000
	windowSecondBurstHz  = 100000
	windowFirstBurstFrom = 100
	windowSecondFrom     = 300
	windowBurstFrames    = 100
	// windowFirstBurstBin and windowSecondBurstBin are the mel bins whose Slaney
	// filter centers are nearest the burst frequencies, from the Slaney mel formula
	// over 9 to 150 kHz in 128 bands (filter k is centered on edge k+1 of the 130
	// evenly spaced mel edges), not read back from this package.
	windowFirstBurstBin  = 78
	windowSecondBurstBin = 109
	// windowBinSpread is how many bins either side of the center a burst may leak
	// into above windowLitLevel after normalization.
	windowBinSpread = 4
	windowLitLevel  = 0.5
	// windowSum is the sum of the normalized matrix for the synthetic window. It is
	// a regression pin only, recorded from an implementation whose logits match the
	// reference (the onnx parity test is the parity proof), so any change to the mel
	// configuration or the normalization moves it. windowSumTolerance is its relative
	// tolerance, wide enough for float32 differences between architectures.
	windowSum          = 6910.78
	windowSumTolerance = 1e-3
)

// syntheticWindow returns the test window described above: silence with a 50 kHz
// burst and a later 100 kHz burst.
func syntheticWindow() []float32 {
	samples := make([]float32, ClipSamples)
	burst := func(from int, hz float64) {
		for i := from * HopSize; i < (from+windowBurstFrames)*HopSize; i++ {
			samples[i] = float32(windowBurstAmp * math.Sin(2*math.Pi*hz*float64(i)/SampleRate))
		}
	}
	burst(windowFirstBurstFrom, windowFirstBurstHz)
	burst(windowSecondFrom, windowSecondBurstHz)
	return samples
}

func TestFrontEnd_Window(t *testing.T) {
	t.Parallel()

	t.Run("bursts land on the mel bins of their frequencies", func(t *testing.T) {
		t.Parallel()
		fe, err := NewFrontEnd()
		require.NoError(t, err)
		out, err := fe.Window(syntheticWindow())
		require.NoError(t, err)
		require.Len(t, out, InputSize)

		// The middle of a burst span is unaffected by its edges.
		mid := windowBurstFrames / 2
		for _, burst := range []struct {
			name  string
			frame int
			bin   int
		}{
			{"50 kHz", windowFirstBurstFrom + mid, windowFirstBurstBin},
			{"100 kHz", windowSecondFrom + mid, windowSecondBurstBin},
		} {
			col := out[burst.frame*NumMels : (burst.frame+1)*NumMels]
			assert.Greaterf(t, col[burst.bin], float32(windowLitLevel), "%s: the center bin must be lit", burst.name)
			for m, v := range col {
				if v > windowLitLevel {
					assert.LessOrEqualf(t, absInt(m-burst.bin), windowBinSpread, "%s: bin %d is lit far from the center", burst.name, m)
				}
			}
		}
		for _, v := range out {
			require.GreaterOrEqual(t, v, float32(ClipMin))
			require.LessOrEqual(t, v, float32(ClipMax))
		}
	})

	t.Run("matrix checksum", func(t *testing.T) {
		t.Parallel()
		fe, err := NewFrontEnd()
		require.NoError(t, err)
		out, err := fe.Window(syntheticWindow())
		require.NoError(t, err)
		var sum float64
		for _, v := range out {
			sum += float64(v)
		}
		assert.InEpsilon(t, windowSum, sum, windowSumTolerance)
	})

	t.Run("digital silence gives zeros", func(t *testing.T) {
		t.Parallel()
		fe, err := NewFrontEnd()
		require.NoError(t, err)
		out, err := fe.Window(make([]float32, ClipSamples))
		require.NoError(t, err)
		for i, v := range out {
			require.Zerof(t, v, "value %d must be zero for silence", i)
		}
	})

	t.Run("a window of the wrong length is rejected", func(t *testing.T) {
		t.Parallel()
		fe, err := NewFrontEnd()
		require.NoError(t, err)
		_, err = fe.Window(make([]float32, ClipSamples-1))
		require.Error(t, err)
	})
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
