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

// newTestBufferManager creates a buffer.Manager and allocates analysis and
// capture buffers for the given sourceID. It uses reasonable defaults for
// audio parameters (48 kHz, 16-bit, mono).
func newTestBufferManager(t *testing.T, sourceID string) *buffer.Manager {
	t.Helper()
	mgr := buffer.NewManager(logger.Global().Module("test"))

	// Analysis buffer: capacity 48000 bytes, overlap 0, read size 48000.
	require.NoError(t, mgr.AllocateAnalysis(sourceID, 48000, 0, 48000))

	// Capture buffer: 10 seconds, 48 kHz, 2 bytes per sample.
	require.NoError(t, mgr.AllocateCapture(sourceID, 10, 48000, 2))

	return mgr
}

func TestBufferConsumer_NewValidation(t *testing.T) {
	t.Parallel()

	mgr := buffer.NewManager(logger.Global().Module("test"))

	t.Run("nil buffer manager", func(t *testing.T) {
		t.Parallel()
		_, err := NewBufferConsumer("test", nil, 48000, 16, 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "buffer manager must not be nil")
	})

	t.Run("invalid sample rate", func(t *testing.T) {
		t.Parallel()
		_, err := NewBufferConsumer("test", mgr, 0, 16, 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid sample rate")
	})

	t.Run("invalid bit depth", func(t *testing.T) {
		t.Parallel()
		_, err := NewBufferConsumer("test", mgr, 48000, 0, 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid bit depth")
	})

	t.Run("invalid channels", func(t *testing.T) {
		t.Parallel()
		_, err := NewBufferConsumer("test", mgr, 48000, 16, 0)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid channel count")
	})

	t.Run("valid parameters", func(t *testing.T) {
		t.Parallel()
		consumer, err := NewBufferConsumer("test", mgr, 48000, 16, 1)
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

	consumer, err := NewBufferConsumer("buf-consumer", mgr, 48000, 16, 1)
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
	ab, abErr := mgr.AnalysisBuffer(sourceID)
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

	consumer, err := NewBufferConsumer("buf-consumer", mgr, 48000, 16, 1)
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

	consumer, err := NewBufferConsumer("buf-consumer", mgr, 48000, 16, 1)
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
