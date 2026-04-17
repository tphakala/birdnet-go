// process_alloc_test.go asserts the 16-bit conversion hot path does not
// allocate a new slice per call after the Float32Pool consolidation.
package analysis

import (
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// newAllocTestLogger mirrors the pattern used in internal/audiocore/buffer tests.
func newAllocTestLogger() logger.Logger {
	return logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC)
}

// TestConvert16BitToFloat32WithPool_ZeroAllocs verifies that the 16-bit
// conversion hot path reuses pooled float32 slices at both the standard
// Float32BufferSize and at a non-standard size. Pre-consolidation, the
// non-standard size fell through to a fresh make() on every call, producing
// unbounded allocation churn.
//
// The test targets convert16BitToFloat32WithPool directly rather than the
// convertToFloat32WithPool wrapper. The wrapper returns [][]float32{inner},
// which heap-allocates the outer slice header every call independent of
// pool state, so measuring the wrapper would mask the underlying hot-path
// behaviour this test exists to protect.
//
// Note: sync.Pool.Put boxes the slice into an interface{}, which historically
// cost 1 alloc per Put. The buffer package accepts this trade-off
// (see buffer/pool.go SA6002 nolint). We therefore assert allocs <= 1
// rather than == 0, which still catches the old fallback-to-make path
// (that produced a new slice allocation per call).
func TestConvert16BitToFloat32WithPool_ZeroAllocs(t *testing.T) {
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
			warm := convert16BitToFloat32WithPool(mgr, pcm)
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
				out := convert16BitToFloat32WithPool(mgr, pcm)
				sink = out
				if p := mgr.Float32PoolFor(len(out)); p != nil {
					p.Put(out)
				}
			})

			_ = sink

			assert.LessOrEqualf(t, allocs, float64(1),
				"convert16BitToFloat32WithPool allocated %.1f slices per call; expected <= 1 "+
					"(only sync.Pool boxing overhead should remain)", allocs)
		})
	}
}
