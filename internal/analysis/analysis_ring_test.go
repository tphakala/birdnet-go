package analysis

import (
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/inference/bsgbat"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	ringSimDuration       = 120 * time.Second
	ringSimPollJitter     = 20 * time.Millisecond
	ringSimWorstInference = 400 * time.Millisecond // Pi 4, one thread, worst measured BSG-BAT window
	ringSimInferenceShare = 0.7                    // share of the step inference may use, before the poll is subtracted
	ringSimSeed           = 42
	ringSimBytesPerSample = 2 // mono 16-bit
)

// ringSimChunkBytes are realistic write sizes: a small capture callback, one
// ffmpeg read, and a 192 kHz read upsampled to 384 kHz.
var ringSimChunkBytes = []int{4 << 10, 32 << 10, 64 << 10}

// ringSimResult is what the simulated monitor observed.
type ringSimResult struct {
	lostBytes    int64
	writtenBytes int64
	windowsRead  int
	startupShort int  // short windows before the first contiguous one
	lateShort    bool // a short window after the first contiguous one
}

// bsgBatSpecAtStep returns the registry BSG-BAT spec with another step.
func bsgBatSpecAtStep(stepFrames int) classifier.ModelSpec {
	spec := classifier.ModelRegistry[classifier.RegistryIDBSGBat].Spec
	spec.StepSamples = stepFrames * bsgbat.HopSize
	return spec
}

// simulateRing drives an analysis buffer of the given capacity on a virtual
// clock the way the pipeline does: audio arrives at the real rate in chunks, and
// a monitor reads one window per time.Ticker tick, blocked for the inference time
// of each scored window. A time.Ticker holds one pending tick: the first tick
// that fires while the monitor is busy is delivered when it returns, later ones
// are dropped.
func simulateRing(t *testing.T, spec classifier.ModelSpec, capacity, chunkBytes int) ringSimResult {
	t.Helper()

	_, overlapBytes, readSize := spec.BufferDimensions(0)
	windowBytes := overlapBytes + readSize
	ab, err := buffer.NewAnalysisBuffer(capacity, overlapBytes, readSize, "sim",
		logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC), nil)
	require.NoError(t, err)

	bytesPerSecond := spec.EffectiveSampleRate() * ringSimBytesPerSample
	// Inference keeps up with the step: its worst draw leaves room for the poll
	// quantization within one step.
	maxInference := min(ringSimWorstInference,
		time.Duration(ringSimInferenceShare*float64(spec.BufferInterval(0)))-bufferMonitorPollInterval)
	require.Positive(t, maxInference)

	rng := rand.New(rand.NewPCG(ringSimSeed, ringSimSeed))
	chunk := make([]byte, chunkBytes)

	nextChunk := 1
	applyWrites := func(until time.Duration) {
		for time.Duration(nextChunk)*time.Duration(chunkBytes)*time.Second/time.Duration(bytesPerSecond) <= until {
			require.NoError(t, ab.Write(chunk))
			nextChunk++
		}
	}

	var res ringSimResult
	seenContiguous := false
	// read performs one monitor read and reports whether a window was scored.
	read := func() (gotWindow, scored bool) {
		window, info, release, readErr := ab.ReadWindow()
		defer release()
		require.NoError(t, readErr)
		if len(window) == 0 {
			return false, false
		}
		res.windowsRead++
		if info.ContiguousBytes >= windowBytes {
			seenContiguous = true
			return true, true
		}
		if seenContiguous {
			res.lateShort = true
		} else {
			res.startupShort++
		}
		return true, false
	}

	tickN := 0
	nextFire := func() time.Duration {
		tickN++
		jitter := time.Duration(rng.Int64N(int64(2*ringSimPollJitter))) - ringSimPollJitter
		return time.Duration(tickN)*bufferMonitorPollInterval + jitter
	}

	fire := nextFire()
	start := fire
	for start <= ringSimDuration {
		applyWrites(start)
		busy := time.Duration(0)
		if _, scored := read(); scored {
			busy = time.Duration(rng.Int64N(int64(maxInference) + 1))
		}
		// The first tick after this one started is held, later ones during the
		// busy period are dropped; the next read starts when both the monitor and
		// that tick are ready.
		for fire <= start {
			fire = nextFire()
		}
		start = max(fire, start+busy)
	}
	applyWrites(ringSimDuration)
	for {
		if gotWindow, _ := read(); !gotWindow {
			break
		}
	}

	stats := ab.Stats()
	res.lostBytes = stats.LostBytes
	res.writtenBytes = stats.WrittenBytes
	return res
}

// TestAnalysisRing_ContiguousWindowLossless proves the headroom ring keeps a
// BSG-BAT stream whole at the smallest, default and largest steps for realistic
// write sizes, poll jitter and inference times that keep up with the step.
func TestAnalysisRing_ContiguousWindowLossless(t *testing.T) {
	t.Parallel()

	for _, stepFrames := range []int{100, 250, 512} {
		spec := bsgBatSpecAtStep(stepFrames)
		capacity, _, readSize := analysisBufferGeometry(spec, 0)
		wantStartup := int(math.Ceil(float64(spec.ClipSampleCount())/float64(spec.StepSamples))) - 1

		for _, chunkBytes := range ringSimChunkBytes {
			t.Run(fmt.Sprintf("step_%d_chunk_%d", stepFrames, chunkBytes), func(t *testing.T) {
				t.Parallel()
				res := simulateRing(t, spec, capacity, chunkBytes)
				assert.Zero(t, res.lostBytes, "no byte may be overwritten unread")
				assert.False(t, res.lateShort, "no window after the first contiguous one may be short")
				assert.Equal(t, wantStartup, res.startupShort, "only the startup windows are short")
				assert.Equal(t, int(res.writtenBytes/int64(readSize)), res.windowsRead,
					"every window the stream holds is read")
			})
		}
	}
}

// TestAnalysisRing_OneWindowRingLosesAudioAtLargeStep shows the headroom is what
// makes the lossless test pass: a ring of one window overwrites unread audio at
// a 512 frame step under the same load.
func TestAnalysisRing_OneWindowRingLosesAudioAtLargeStep(t *testing.T) {
	t.Parallel()

	spec := bsgBatSpecAtStep(512)
	res := simulateRing(t, spec, spec.ClipSizeBytes(), 32<<10)
	assert.Positive(t, res.lostBytes)
	assert.True(t, res.lateShort)
}

// TestAnalysisBufferGeometry_ForwardsSpec checks that for every registry entry
// the helper takes its ring capacity from AnalysisRingCapacity and its overlap
// and read size from BufferDimensions, the values the allocation site uses.
func TestAnalysisBufferGeometry_ForwardsSpec(t *testing.T) {
	t.Parallel()

	overlaps := []time.Duration{0, 500 * time.Millisecond, 1500 * time.Millisecond, 2400 * time.Millisecond}
	for id := range classifier.ModelRegistry {
		spec := classifier.ModelRegistry[id].Spec
		for _, overlap := range overlaps {
			_, wantOverlap, wantRead := spec.BufferDimensions(overlap)
			capacity, gotOverlap, gotRead := analysisBufferGeometry(spec, overlap)
			assert.Equal(t, spec.AnalysisRingCapacity(), capacity, "%s ring capacity at %v", id, overlap)
			assert.Equal(t, wantOverlap, gotOverlap, "%s overlap bytes at %v", id, overlap)
			assert.Equal(t, wantRead, gotRead, "%s read size at %v", id, overlap)
		}
	}
}
