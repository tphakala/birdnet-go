// process_alloc_test.go asserts the 16-bit conversion hot path does not
// allocate a new slice per call after the Float32Pool consolidation.
package analysis

import (
	"io"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/logger"
	dsppcm "github.com/tphakala/go-audio-dsp/pcm"
)

// newAllocTestLogger mirrors the pattern used in internal/audiocore/buffer tests.
func newAllocTestLogger() logger.Logger {
	return logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC)
}

// TestDecodePCM16WithPool_ZeroAllocs verifies that the 16-bit decode hot path
// reuses pooled float32 slices at both the standard Float32BufferSize and at a
// non-standard size. Pre-consolidation, the non-standard size fell through to
// a fresh make() on every call, producing unbounded allocation churn.
//
// Note: sync.Pool.Put boxes the slice into an interface{}, which historically
// cost 1 alloc per Put. The buffer package accepts this trade-off
// (see buffer/pool.go SA6002 nolint). We therefore assert allocs <= 1
// rather than == 0, which still catches the old fallback-to-make path
// (that produced a new slice allocation per call).
func TestDecodePCM16WithPool_ZeroAllocs(t *testing.T) {
	// Do NOT mark t.Parallel(): the shared Manager is stateful and other
	// tests in this package may mutate global conf.Setting() concurrently.

	mgr := buffer.NewManager(newAllocTestLogger())
	require.NotNil(t, mgr, "buffer.NewManager should return a non-nil manager")

	standardBytes := Float32BufferSize * 2
	nonStandardBytes := standardBytes + 128 // any length the old fallback would have hit make()

	cases := []struct {
		name     string
		pcmBytes int
	}{
		{"standard_size", standardBytes},
		{"non_standard_size", nonStandardBytes},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pcm := make([]byte, tc.pcmBytes)
			for i := range pcm {
				pcm[i] = byte(i & 0xFF)
			}

			// Warm the pool so the first Get doesn't count as an allocation
			// via pool.New (AllocsPerRun also runs a warmup, but an explicit
			// warm pass makes the intent unambiguous).
			warm, err := decodePCM16WithPool(mgr, pcm)
			require.NoError(t, err)
			require.Len(t, warm, tc.pcmBytes/2)

			// Sanity: the first sample should be the int16 little-endian decode of
			// pcm[0..2] divided by 32768. Guards against silent regressions where a
			// loop change leaves pooled slots uninitialised (sync.Pool does not
			// zero).
			expectedFirst := float32(int16(pcm[0])|int16(pcm[1])<<8) / float32(32768.0)
			assert.InDelta(t, expectedFirst, warm[0], 1e-6,
				"first converted sample should match scalar decode of pcm[0..2]")

			if p := mgr.Float32PoolFor(len(warm)); p != nil {
				p.Put(warm)
			}

			var sink []float32

			allocs := testing.AllocsPerRun(100, func() {
				out, decodeErr := decodePCM16WithPool(mgr, pcm)
				if decodeErr != nil {
					panic(decodeErr)
				}
				sink = out
				if p := mgr.Float32PoolFor(len(out)); p != nil {
					p.Put(out)
				}
			})

			_ = sink

			assert.LessOrEqualf(t, allocs, float64(1),
				"decodePCM16WithPool allocated %.1f slices per call; expected <= 1 "+
					"(only sync.Pool boxing overhead should remain)", allocs)
		})
	}
}

// TestDecodePCM16WithPool_OverwritesPooledBuffer verifies that every sample of
// a recycled pooled buffer is overwritten: sync.Pool does not zero, so a
// decode that left a slot untouched would feed stale data to the model.
func TestDecodePCM16WithPool_OverwritesPooledBuffer(t *testing.T) {
	mgr := buffer.NewManager(newAllocTestLogger())
	require.NotNil(t, mgr)

	const samples = 4096
	pcm := make([]byte, samples*2)
	for i := range pcm {
		pcm[i] = byte(i*7 + 3)
	}

	pool := mgr.Float32PoolFor(samples)
	require.NotNil(t, pool)
	poisoned := make([]float32, samples)
	for i := range poisoned {
		poisoned[i] = float32(math.NaN())
	}
	pool.Put(poisoned)

	out, err := decodePCM16WithPool(mgr, pcm)
	require.NoError(t, err)
	require.Len(t, out, samples)
	for i, got := range out {
		want := float32(int16(pcm[2*i])|int16(pcm[2*i+1])<<8) / 32768
		require.Equal(t, math.Float32bits(want), math.Float32bits(got), "sample %d: bit-identical to int16/32768", i)
	}
}

// TestDecodePCM16WithPool_OddLengthFails verifies that a half sample is
// reported and that no pooled buffer is taken for it.
func TestDecodePCM16WithPool_OddLengthFails(t *testing.T) {
	mgr := buffer.NewManager(newAllocTestLogger())
	require.NotNil(t, mgr)

	const samples = 1024
	pool := mgr.Float32PoolFor(samples)
	require.NotNil(t, pool)
	before := pool.GetStats()

	out, err := decodePCM16WithPool(mgr, make([]byte, samples*2+1))
	require.Error(t, err)
	require.ErrorIs(t, err, dsppcm.ErrOddByteLength)
	assert.Nil(t, out)

	after := pool.GetStats()
	assert.Equal(t, before, after, "an odd-length decode must not touch the pool")
}

// TestDecodePCM16WithPool_EmptyInput verifies that an empty buffer decodes to an
// empty slice without a pool, which has no size-0 entry.
func TestDecodePCM16WithPool_EmptyInput(t *testing.T) {
	mgr := buffer.NewManager(newAllocTestLogger())
	require.NotNil(t, mgr)

	out, err := decodePCM16WithPool(mgr, nil)

	require.NoError(t, err)
	assert.Empty(t, out)
}
