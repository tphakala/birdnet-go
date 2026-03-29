package analysis

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// testDefaultModelID is the default model identifier used in buffer consumer
// tests where a concrete model name is not relevant.
const testDefaultModelID = "BirdNET_GLOBAL_6K_V2.4"

// testDefaultSampleRate is the source sample rate used in most tests.
const testDefaultSampleRate = 48000

// testDefaultTargets returns a single ModelTarget at 48 kHz for backwards-
// compatible test helpers.
func testDefaultTargets() []ModelTarget {
	return []ModelTarget{{ModelID: testDefaultModelID, SampleRate: testDefaultSampleRate}}
}

// newTestBufferManager creates a buffer.Manager and allocates analysis and
// capture buffers for the given sourceID. It uses reasonable defaults for
// audio parameters (48 kHz, 16-bit, mono).
func newTestBufferManager(t *testing.T, sourceID string) *buffer.Manager {
	t.Helper()
	mgr := buffer.NewManager(logger.Global().Module("test"))

	// Analysis buffer: capacity 48000 bytes, overlap 0, read size 48000.
	require.NoError(t, mgr.AllocateAnalysis(sourceID, testDefaultModelID, 48000, 0, 48000))

	// Capture buffer: 10 seconds, 48 kHz, 2 bytes per sample.
	require.NoError(t, mgr.AllocateCapture(sourceID, 10, 48000, 2))

	return mgr
}

func TestBufferConsumer_NewValidation(t *testing.T) {
	t.Parallel()

	mgr := buffer.NewManager(logger.Global().Module("test"))

	t.Run("nil buffer manager", func(t *testing.T) {
		t.Parallel()
		_, err := NewBufferConsumer("test", nil, 48000, 16, 1, testDefaultTargets())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "buffer manager must not be nil")
	})

	t.Run("invalid sample rate", func(t *testing.T) {
		t.Parallel()
		_, err := NewBufferConsumer("test", mgr, 0, 16, 1, testDefaultTargets())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid sample rate")
	})

	t.Run("invalid bit depth", func(t *testing.T) {
		t.Parallel()
		_, err := NewBufferConsumer("test", mgr, 48000, 0, 1, testDefaultTargets())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid bit depth")
	})

	t.Run("invalid channels", func(t *testing.T) {
		t.Parallel()
		_, err := NewBufferConsumer("test", mgr, 48000, 16, 0, testDefaultTargets())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid channel count")
	})

	t.Run("valid parameters", func(t *testing.T) {
		t.Parallel()
		consumer, err := NewBufferConsumer("test", mgr, 48000, 16, 1, testDefaultTargets())
		require.NoError(t, err)
		assert.Equal(t, "test", consumer.ID())
		assert.Equal(t, 48000, consumer.SampleRate())
		assert.Equal(t, 16, consumer.BitDepth())
		assert.Equal(t, 1, consumer.Channels())
	})
}

func TestBufferConsumer_WritesToBothBuffers(t *testing.T) {
	t.Parallel()

	sourceID := "test-source"
	mgr := newTestBufferManager(t, sourceID)

	consumer, err := NewBufferConsumer("buf-consumer", mgr, 48000, 16, 1, testDefaultTargets())
	require.NoError(t, err)

	// Create a small PCM frame (100 samples of silence).
	pcmData := make([]byte, 200) // 100 samples * 2 bytes

	frame := audiocore.AudioFrame{
		SourceID:   sourceID,
		SourceName: "Test Source",
		Data:       pcmData,
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
		Timestamp:  time.Now(),
	}

	err = consumer.Write(frame)
	require.NoError(t, err)

	// Verify analysis buffer received data by reading it back.
	ab, abErr := mgr.AnalysisBuffer(sourceID, testDefaultModelID)
	require.NoError(t, abErr)
	require.NotNil(t, ab)

	// Write enough data to fill the analysis buffer's readSize so we can read back.
	// The buffer was allocated with readSize=48000, so write enough to satisfy that.
	bigFrame := audiocore.AudioFrame{
		SourceID:   sourceID,
		SourceName: "Test Source",
		Data:       make([]byte, 48000),
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
		Timestamp:  time.Now(),
	}
	require.NoError(t, consumer.Write(bigFrame))

	data, readErr := ab.Read()
	require.NoError(t, readErr)
	assert.NotNil(t, data, "analysis buffer should return data after sufficient writes")

	// Verify capture buffer received data by checking start time is set.
	cb, cbErr := mgr.CaptureBuffer(sourceID)
	require.NoError(t, cbErr)
	require.NotNil(t, cb)
	assert.False(t, cb.StartTime().IsZero(), "capture buffer should have a start time after write")
}

func TestBufferConsumer_MissingBufferDoesNotError(t *testing.T) {
	t.Parallel()

	// Manager with no buffers allocated.
	mgr := buffer.NewManager(logger.Global().Module("test"))

	consumer, err := NewBufferConsumer("buf-consumer", mgr, 48000, 16, 1, testDefaultTargets())
	require.NoError(t, err)

	frame := audiocore.AudioFrame{
		SourceID:   "no-such-source",
		SourceName: "Missing",
		Data:       make([]byte, 200),
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
		Timestamp:  time.Now(),
	}

	// Write should succeed (log warnings but no error).
	err = consumer.Write(frame)
	assert.NoError(t, err)
}

func TestBufferConsumer_CloseRejectsSubsequentWrites(t *testing.T) {
	t.Parallel()

	sourceID := "test-source"
	mgr := newTestBufferManager(t, sourceID)

	consumer, err := NewBufferConsumer("buf-consumer", mgr, 48000, 16, 1, testDefaultTargets())
	require.NoError(t, err)

	require.NoError(t, consumer.Close())

	frame := audiocore.AudioFrame{
		SourceID:   sourceID,
		SourceName: "Test",
		Data:       make([]byte, 200),
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
		Timestamp:  time.Now(),
	}

	err = consumer.Write(frame)
	assert.ErrorIs(t, err, audiocore.ErrConsumerClosed)
}

func TestCalculateAudioLevel_EmptyData(t *testing.T) {
	t.Parallel()

	result := calculateAudioLevel(nil, "src", "name")
	assert.Equal(t, 0, result.Level)
	assert.False(t, result.Clipping)
	assert.Equal(t, "src", result.Source)
	assert.Equal(t, "name", result.Name)
}

func TestCalculateAudioLevel_Silence(t *testing.T) {
	t.Parallel()

	// 100 samples of silence (all zeros).
	data := make([]byte, 200)
	result := calculateAudioLevel(data, "src", "name")
	assert.Equal(t, 0, result.Level)
	assert.False(t, result.Clipping)
}

func TestCalculateAudioLevel_Clipping(t *testing.T) {
	t.Parallel()

	// Produce a frame where every sample is at the positive clip threshold.
	numSamples := 100
	data := make([]byte, numSamples*2)
	for i := range numSamples {
		binary.LittleEndian.PutUint16(data[i*2:], uint16(pcm16ClipPositive)) //nolint:gosec // test-only
	}

	result := calculateAudioLevel(data, "src", "name")
	assert.True(t, result.Clipping)
	assert.GreaterOrEqual(t, result.Level, 95)
}

func TestCalculateAudioLevel_MidLevel(t *testing.T) {
	t.Parallel()

	// Produce samples with a known amplitude (~half of max).
	const amplitude int16 = 16384 // ~50% of 32768
	numSamples := 100
	data := make([]byte, numSamples*2)
	for i := range numSamples {
		binary.LittleEndian.PutUint16(data[i*2:], uint16(amplitude)) //nolint:gosec // test-only
	}

	result := calculateAudioLevel(data, "src", "name")
	assert.False(t, result.Clipping)
	// RMS of constant 16384 = 16384
	// dB = 20*log10(16384/32768) = 20*log10(0.5) ≈ -6.02
	// scaled = (-6.02 + 60) * (100/50) ≈ 107.96 → clamped to 100
	// The level should be high but clamped.
	assert.Positive(t, result.Level)
}

func TestCalculateAudioLevel_OddByteCount(t *testing.T) {
	t.Parallel()

	// 3 bytes — should be truncated to 2 (1 sample).
	data := []byte{0x00, 0x40, 0xFF} // sample = 0x4000 = 16384
	result := calculateAudioLevel(data, "src", "name")
	assert.Positive(t, result.Level)
	assert.False(t, result.Clipping)
}

func TestCalculateAudioLevel_NegativeClipping(t *testing.T) {
	t.Parallel()

	// Produce a frame where every sample is at the negative clip threshold (-32768).
	// -32768 as int16 has bit pattern 0x8000.
	numSamples := 100
	data := make([]byte, numSamples*2)
	for i := range numSamples {
		binary.LittleEndian.PutUint16(data[i*2:], 0x8000) // -32768 in two's complement
	}

	result := calculateAudioLevel(data, "src", "name")
	assert.True(t, result.Clipping)
	assert.GreaterOrEqual(t, result.Level, 95)
}

func TestCalculateAudioLevel_MatchesLegacy(t *testing.T) {
	t.Parallel()

	// Verify that calculateAudioLevel produces the same results as the
	// legacy implementation's formula for a known input.
	const sampleVal int16 = 10000
	numSamples := 480
	data := make([]byte, numSamples*2)
	for i := range numSamples {
		binary.LittleEndian.PutUint16(data[i*2:], uint16(sampleVal)) //nolint:gosec // test-only
	}

	// Compute expected value manually.
	absVal := math.Abs(float64(sampleVal))
	sum := absVal * absVal * float64(numSamples)
	rms := math.Sqrt(sum / float64(numSamples))
	db := 20 * math.Log10(rms/pcm16Max)
	expected := (db + 60) * (100.0 / 50.0)
	if expected < 0 {
		expected = 0
	} else if expected > 100 {
		expected = 100
	}

	result := calculateAudioLevel(data, "s", "n")
	assert.Equal(t, int(expected), result.Level)
}

func TestBufferConsumer_FanOut_MultiModel(t *testing.T) {
	t.Parallel()

	const (
		sourceID     = "multi-model-src"
		birdnetModel = "birdnet-v2.4"
		perchModel   = "perch-v2"
		sourceRate   = 48000
		perchRate    = 32000
		// Use generous buffer capacities and small readSizes so a single
		// frame write is sufficient to trigger a successful Read.
		bufCapacity = 96000 // large enough for multiple frames
		readSize48  = 4800  // small readSize for birdnet (100 ms at 48 kHz)
		readSize32  = 3200  // small readSize for perch (100 ms at 32 kHz)
	)

	mgr := buffer.NewManager(logger.Global().Module("test"))

	// Allocate analysis buffers for both models.
	require.NoError(t, mgr.AllocateAnalysis(sourceID, birdnetModel, bufCapacity, 0, readSize48))
	require.NoError(t, mgr.AllocateAnalysis(sourceID, perchModel, bufCapacity, 0, readSize32))
	require.NoError(t, mgr.AllocateCapture(sourceID, 10, sourceRate, 2))

	targets := []ModelTarget{
		{ModelID: birdnetModel, SampleRate: sourceRate},
		{ModelID: perchModel, SampleRate: perchRate},
	}

	consumer, err := NewBufferConsumer("multi-model", mgr, sourceRate, 16, 1, targets)
	require.NoError(t, err)
	t.Cleanup(func() { _ = consumer.Close() })

	// Write a frame large enough to satisfy both readSizes after resampling.
	// 9600 bytes at 48 kHz = 4800 samples. Resampled to 32 kHz ≈ 3200 samples
	// = 6400 bytes, well above readSize32.
	frameSize := 9600 // bytes
	frame := audiocore.AudioFrame{
		SourceID:   sourceID,
		SourceName: "Multi-Model Source",
		Data:       make([]byte, frameSize),
		SampleRate: sourceRate,
		BitDepth:   16,
		Channels:   1,
		Timestamp:  time.Now(),
	}
	require.NoError(t, consumer.Write(frame))

	// Verify birdnet buffer received data (no resampling).
	ab48, err := mgr.AnalysisBuffer(sourceID, birdnetModel)
	require.NoError(t, err)
	data48, readErr := ab48.Read()
	require.NoError(t, readErr)
	require.NotNil(t, data48, "48 kHz buffer should have data after write")

	// Verify perch buffer received resampled data.
	ab32, err := mgr.AnalysisBuffer(sourceID, perchModel)
	require.NoError(t, err)
	data32, readErr := ab32.Read()
	require.NoError(t, readErr)
	require.NotNil(t, data32, "32 kHz buffer should have resampled data after write")

	// The 32 kHz read returns readSize32 bytes (3200), while the 48 kHz read
	// returns readSize48 bytes (4800). The lower-rate read should be shorter.
	assert.Less(t, len(data32), len(data48),
		"32 kHz readSize should be less than 48 kHz readSize")
}

func TestBufferConsumer_FanOut_SingleModel_BackwardsCompat(t *testing.T) {
	t.Parallel()

	sourceID := "single-model-src"
	mgr := newTestBufferManager(t, sourceID)

	// Single target at source rate — no resampler should be created.
	targets := []ModelTarget{{ModelID: testDefaultModelID, SampleRate: testDefaultSampleRate}}
	consumer, err := NewBufferConsumer("single", mgr, testDefaultSampleRate, 16, 1, targets)
	require.NoError(t, err)
	t.Cleanup(func() { _ = consumer.Close() })

	// No resamplers should have been created.
	assert.Empty(t, consumer.resamplers, "no resamplers needed when target rate equals source rate")

	// Write should succeed.
	frame := audiocore.AudioFrame{
		SourceID:   sourceID,
		SourceName: "Single Model",
		Data:       make([]byte, 200),
		SampleRate: testDefaultSampleRate,
		BitDepth:   16,
		Channels:   1,
		Timestamp:  time.Now(),
	}
	assert.NoError(t, consumer.Write(frame))
}

func TestBufferConsumer_SingleModel_FullPipeline(t *testing.T) {
	t.Parallel()
	mgr := buffer.NewManager(logger.Global().Module("test"))

	const (
		sampleRate     = 48000
		clipLength     = 3 // seconds
		bytesPerSample = 2
		capacity       = sampleRate * clipLength * bytesPerSample // 288000
	)

	userOverlap := 1 * time.Second
	baseClip := 3 * time.Second
	modelClip := 3 * time.Second
	scaled := effectiveOverlap(userOverlap, baseClip, modelClip)
	oBytes := overlapBytes(scaled, sampleRate, bytesPerSample)

	readSize := capacity - oBytes

	require.NoError(t, mgr.AllocateAnalysis("mic1", "birdnet-v2.4", capacity, oBytes, readSize))
	require.NoError(t, mgr.AllocateCapture("mic1", 120, sampleRate, bytesPerSample))

	targets := []ModelTarget{{ModelID: "birdnet-v2.4", SampleRate: sampleRate}}
	consumer, err := NewBufferConsumer("mic1", mgr, sampleRate, 16, 1, targets)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, consumer.Close()) })

	// Write enough data to fill the analysis buffer.
	frameSize := 4096
	framesNeeded := capacity / frameSize
	for range framesNeeded + 1 {
		frame := audiocore.AudioFrame{
			SourceID:   "mic1",
			Data:       make([]byte, frameSize),
			SampleRate: sampleRate,
			BitDepth:   16,
			Channels:   1,
		}
		require.NoError(t, consumer.Write(frame))
	}

	// Read from the analysis buffer — should return data.
	ab, err := mgr.AnalysisBuffer("mic1", "birdnet-v2.4")
	require.NoError(t, err)
	data, err := ab.Read()
	require.NoError(t, err)
	assert.NotNil(t, data, "should have enough data for a full read")
}

func TestBufferConsumer_Close_ClosesResamplers(t *testing.T) {
	t.Parallel()

	const (
		sourceID   = "close-resampler-src"
		sourceRate = 48000
		targetRate = 32000
		modelID    = "resampler-model"
	)

	mgr := buffer.NewManager(logger.Global().Module("test"))
	require.NoError(t, mgr.AllocateAnalysis(sourceID, modelID, 32000, 0, 32000))
	require.NoError(t, mgr.AllocateCapture(sourceID, 10, sourceRate, 2))

	targets := []ModelTarget{{ModelID: modelID, SampleRate: targetRate}}
	consumer, err := NewBufferConsumer("close-test", mgr, sourceRate, 16, 1, targets)
	require.NoError(t, err)

	// A resampler should have been created for the non-native rate.
	require.Len(t, consumer.resamplers, 1)
	assert.Contains(t, consumer.resamplers, targetRate)

	// Close should succeed and not panic.
	require.NoError(t, consumer.Close())

	// Subsequent writes should be rejected.
	frame := audiocore.AudioFrame{
		SourceID:   sourceID,
		SourceName: "Closed Source",
		Data:       make([]byte, 200),
		SampleRate: sourceRate,
		BitDepth:   16,
		Channels:   1,
		Timestamp:  time.Now(),
	}
	assert.ErrorIs(t, consumer.Write(frame), audiocore.ErrConsumerClosed)
}
