package noisebank

import (
	"context"
	"encoding/binary"
	"math"
	"math/rand/v2"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/resample"
	"github.com/tphakala/birdnet-go/internal/audiocore/transcode"
	"github.com/tphakala/birdnet-go/internal/audiocore/wav"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// sliceStream delivers a fixed signal in chunks of a given size.
type sliceStream struct {
	samples []float32
	chunk   int
	rate    int
}

func (s sliceStream) SampleRate() int    { return s.rate }
func (s sliceStream) SpanSamples() int64 { return int64(len(s.samples)) }
func (s sliceStream) Stream(ctx context.Context, fn func([]float32) error) error {
	for off := 0; off < len(s.samples); off += s.chunk {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fn(s.samples[off:min(off+s.chunk, len(s.samples))]); err != nil {
			return err
		}
	}
	return nil
}

// TestForEachWindow pins the grid: windows start at sample 0 and every hop, they
// are whole windows only, and where a chunk boundary falls does not matter.
func TestForEachWindow(t *testing.T) {
	g := windowGrid{window: 10, hop: 4, rate: 1000}
	signal := make([]float32, 25)
	for i := range signal {
		signal[i] = float32(i)
	}

	for _, chunk := range []int{1, 3, 4, 10, 11, 25, 100} {
		t.Run("chunk "+string(rune('0'+chunk%10)), func(t *testing.T) {
			var offsets []int64
			err := forEachWindow(t.Context(), sliceStream{samples: signal, chunk: chunk, rate: g.rate}, g, func(offset int64, w []float32) error {
				offsets = append(offsets, offset)
				require.Len(t, w, g.window)
				assert.Equal(t, signal[offset:offset+int64(g.window)], w, "window at %d", offset)
				return nil
			})
			require.NoError(t, err)
			assert.Equal(t, []int64{0, 4, 8, 12}, offsets, "the tail shorter than a window is dropped")
			assert.Len(t, offsets, g.count(int64(len(signal))))
		})
	}

	t.Run("windows may be kept", func(t *testing.T) {
		var kept [][]float32
		require.NoError(t, forEachWindow(t.Context(), sliceStream{samples: signal, chunk: 3, rate: g.rate}, g, func(_ int64, w []float32) error {
			kept = append(kept, w)
			return nil
		}))
		assert.Equal(t, signal[0:10], kept[0])
		assert.Equal(t, signal[12:22], kept[3])
	})

	t.Run("an error from the callback stops the stream", func(t *testing.T) {
		stop := errors.NewStd("stop")
		calls := 0
		err := forEachWindow(t.Context(), sliceStream{samples: signal, chunk: 5, rate: g.rate}, g, func(int64, []float32) error {
			calls++
			return stop
		})
		require.ErrorIs(t, err, stop)
		assert.Equal(t, 1, calls)
	})

	t.Run("a clip shorter than a window has none", func(t *testing.T) {
		called := false
		require.NoError(t, forEachWindow(t.Context(), sliceStream{samples: signal[:9], chunk: 3, rate: g.rate}, g, func(int64, []float32) error {
			called = true
			return nil
		}))
		assert.False(t, called)
		assert.Zero(t, g.count(9))
	})

	t.Run("offsets in milliseconds", func(t *testing.T) {
		assert.Equal(t, 281, windowGrid{window: 144000, hop: 72000, rate: 256000}.offsetMs(72000))
		assert.Equal(t, 562, windowGrid{window: 144000, hop: 72000, rate: 256000}.offsetMs(144000))
	})
}

const (
	// liveSourceRate is a capture rate bat recorders use.
	liveSourceRate = 192000
	// liveFrameBytes is the size of one capture frame the live path resamples at a
	// time (20 ms of 16-bit mono).
	liveFrameBytes = liveSourceRate / 50 * 2
	// minWindowCorrelation is how closely a window from the clip path must follow
	// the same window from the live resampling path. It guards against a change of
	// resampler preset, chunking or windowing on either side.
	minWindowCorrelation = 0.99
	liveTestWindows      = 4
)

// batLikeSignal returns 16-bit mono PCM at the source rate: tones in the bat band
// with a slow amplitude sweep and a little noise, so every window differs.
func batLikeSignal(samples int) []byte {
	rng := rand.New(rand.NewPCG(1, 2))
	out := make([]byte, 2*samples)
	for i := range samples {
		ts := float64(i) / liveSourceRate
		v := 0.3*math.Sin(2*math.Pi*38000*ts)*(0.6+0.4*math.Sin(2*math.Pi*7*ts)) +
			0.2*math.Sin(2*math.Pi*21000*ts) + 0.05*(rng.Float64()-0.5)
		binary.LittleEndian.PutUint16(out[2*i:], uint16(int16(v*math.MaxInt16/2))) //nolint:gosec // G115: PCM bit reinterpretation
	}
	return out
}

func correlation(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	return dot / math.Sqrt(na*nb)
}

// TestWindowReader_AgreesWithTheLivePath compares the windows the learner cuts from
// a saved clip with the windows the live analysis cuts from the same audio, which
// is resampled one capture frame at a time and requantised to 16 bits.
func TestWindowReader_AgreesWithTheLivePath(t *testing.T) {
	e := newTestEnv(t)
	g := e.grid
	targetSamples := g.window + (liveTestWindows-1)*g.hop
	sourceSamples := targetSamples*liveSourceRate/g.rate + liveSourceRate // a second of margin
	pcm := batLikeSignal(sourceSamples)

	// Live path: frame sized chunks through the resampler, then int16 / 32768.
	rs, err := resample.NewResampler(liveSourceRate, g.rate)
	require.NoError(t, err)
	t.Cleanup(func() { _ = rs.Close() })
	var live []float32
	for off := 0; off < len(pcm); off += liveFrameBytes {
		out, err := rs.ResampleInto(pcm[off:min(off+liveFrameBytes, len(pcm))])
		require.NoError(t, err)
		for i := 0; i+1 < len(out); i += 2 {
			live = append(live, float32(int16(binary.LittleEndian.Uint16(out[i:])))/pcmScale) //nolint:gosec // G115: PCM bit reinterpretation
		}
	}

	// Clip path: the same audio saved as a WAV and read by the learner.
	path := filepath.Join(t.TempDir(), "clip.wav")
	require.NoError(t, wav.EncodePCM(t.Context(), &wav.Options{PCMData: pcm, OutputPath: path, SampleRate: liveSourceRate, BitDepth: pcmBitDepth}))
	src, err := transcode.DecodeMono(t.Context(), path, g.rate)
	require.NoError(t, err)

	seen := 0
	err = forEachWindow(t.Context(), src, g, func(offset int64, w []float32) error {
		if seen >= liveTestWindows {
			return nil
		}
		seen++
		liveWindow := live[offset : offset+int64(g.window)]
		assert.GreaterOrEqual(t, correlation(w, liveWindow), minWindowCorrelation, "window at sample %d", offset)
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, liveTestWindows, seen)
}
