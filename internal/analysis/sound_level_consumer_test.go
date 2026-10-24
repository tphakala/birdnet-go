package analysis

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/soundlevel"
)

const (
	consumerTestRate  = 48000
	expectedBandCount = 30
	oddFrameBytes     = 201
)

func TestSoundLevelConsumer_NilProcessor(t *testing.T) {
	t.Parallel()

	_, _, err := NewSoundLevelConsumer("slc-1", nil, 48000, 16, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sound level processor must not be nil")
}

func TestSoundLevelConsumer_Accessors(t *testing.T) {
	t.Parallel()

	proc, err := soundlevel.NewProcessor("src-1", "Source One", 48000, 5)
	require.NoError(t, err)

	consumer, ch, err := NewSoundLevelConsumer("slc-2", proc, 48000, 16, 1)
	require.NoError(t, err)
	require.NotNil(t, ch)

	assert.Equal(t, "slc-2", consumer.ID())
	assert.Equal(t, 48000, consumer.SampleRate())
	assert.Equal(t, 16, consumer.BitDepth())
	assert.Equal(t, 1, consumer.Channels())
}

func TestSoundLevelConsumer_WriteEmptyFrame(t *testing.T) {
	t.Parallel()

	proc, err := soundlevel.NewProcessor("src-2", "Source Two", 48000, 5)
	require.NoError(t, err)

	consumer, ch, err := NewSoundLevelConsumer("slc-3", proc, 48000, 16, 1)
	require.NoError(t, err)

	frame := audiocore.AudioFrame{
		SourceID:   "src-2",
		SourceName: "Source Two",
		Data:       nil,
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
		Timestamp:  time.Now(),
	}

	// Empty data should be a no-op (no error, nothing on channel).
	err = consumer.Write(frame)
	require.NoError(t, err)

	select {
	case <-ch:
		t.Fatal("expected no data on channel for empty frame")
	default:
		// Good, no data.
	}
}

func TestSoundLevelConsumer_WriteAccumulatesWithoutError(t *testing.T) {
	t.Parallel()

	// Use a short interval so we can potentially complete it.
	proc, err := soundlevel.NewProcessor("src-3", "Source Three", 48000, 1)
	require.NoError(t, err)

	consumer, _, err := NewSoundLevelConsumer("slc-4", proc, 48000, 16, 1)
	require.NoError(t, err)

	// Send a small frame (less than 1 second). The processor should accumulate
	// but not produce a result yet.
	numSamples := 1000
	data := makeSineWavePCM16(numSamples, 440, 48000)

	frame := audiocore.AudioFrame{
		SourceID:   "src-3",
		SourceName: "Source Three",
		Data:       data,
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
		Timestamp:  time.Now(),
	}

	err = consumer.Write(frame)
	assert.NoError(t, err)
}

func TestSoundLevelConsumer_WriteProducesDataAfterInterval(t *testing.T) {
	t.Parallel()

	// 1-second interval with 48 kHz = need 48000 samples to complete.
	const sampleRate = 48000
	const interval = 1
	proc, err := soundlevel.NewProcessor("src-4", "Source Four", sampleRate, interval)
	require.NoError(t, err)

	consumer, ch, err := NewSoundLevelConsumer("slc-5", proc, sampleRate, 16, 1)
	require.NoError(t, err)

	// Generate exactly 1 second of 440 Hz sine wave.
	data := makeSineWavePCM16(sampleRate, 440, sampleRate)

	frame := audiocore.AudioFrame{
		SourceID:   "src-4",
		SourceName: "Source Four",
		Data:       data,
		SampleRate: sampleRate,
		BitDepth:   16,
		Channels:   1,
		Timestamp:  time.Now(),
	}

	err = consumer.Write(frame)
	require.NoError(t, err)

	select {
	case result := <-ch:
		assert.Equal(t, "src-4", result.Source)
		assert.Equal(t, "Source Four", result.Name)
		assert.Equal(t, interval, result.Duration)
		assert.Len(t, result.OctaveBands, expectedBandCount)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for sound level data on channel")
	}
}

func TestSoundLevelConsumer_WriteSpanningIntervalsPublishesEach(t *testing.T) {
	t.Parallel()

	// A 2.5 s write is longer than one decode chunk and spans two 1 s intervals.
	const interval = 1
	proc, err := soundlevel.NewProcessor("src-6", "Source Six", consumerTestRate, interval)
	require.NoError(t, err)

	consumer, ch, err := NewSoundLevelConsumer("slc-7", proc, consumerTestRate, 16, 1)
	require.NoError(t, err)
	require.Greater(t, 5*consumerTestRate/2, soundLevelDecodeChunk)

	require.NoError(t, consumer.Write(audiocore.AudioFrame{
		SourceID: "src-6",
		Data:     makeSineWavePCM16(5*consumerTestRate/2, 440, consumerTestRate),
	}))

	assert.Len(t, ch, 2)
}

func TestSoundLevelConsumer_OddLengthFrameIsDropped(t *testing.T) {
	t.Parallel()

	const interval = 1
	proc, err := soundlevel.NewProcessor("src-7", "Source Seven", consumerTestRate, interval)
	require.NoError(t, err)

	consumer, ch, err := NewSoundLevelConsumer("slc-8", proc, consumerTestRate, 16, 1)
	require.NoError(t, err)

	require.NoError(t, consumer.Write(audiocore.AudioFrame{
		SourceID: "src-7",
		Data:     make([]byte, oddFrameBytes),
	}))
	assert.Empty(t, ch)

	// The dropped frame left no partial sample behind: one aligned second
	// completes exactly one interval.
	require.NoError(t, consumer.Write(audiocore.AudioFrame{
		SourceID: "src-7",
		Data:     makeSineWavePCM16(consumerTestRate, 440, consumerTestRate),
	}))
	assert.Len(t, ch, 1)
}

func TestSoundLevelConsumer_CloseRejectsWrites(t *testing.T) {
	t.Parallel()

	proc, err := soundlevel.NewProcessor("src-5", "Source Five", 48000, 5)
	require.NoError(t, err)

	consumer, _, err := NewSoundLevelConsumer("slc-6", proc, 48000, 16, 1)
	require.NoError(t, err)

	require.NoError(t, consumer.Close())

	frame := audiocore.AudioFrame{
		SourceID:   "src-5",
		SourceName: "Source Five",
		Data:       make([]byte, 200),
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
		Timestamp:  time.Now(),
	}

	err = consumer.Write(frame)
	assert.ErrorIs(t, err, audiocore.ErrConsumerClosed)
}

// makeSineWavePCM16 generates PCM16 little-endian bytes for a sine wave.
func makeSineWavePCM16(numSamples int, freqHz float64, sampleRate int) []byte {
	data := make([]byte, numSamples*2)
	for i := range numSamples {
		// Half amplitude to avoid clipping.
		val := 0.5 * math.Sin(2*math.Pi*freqHz*float64(i)/float64(sampleRate))
		sample := int16(val * 32767) //nolint:gosec // test-only
		binary.LittleEndian.PutUint16(data[i*2:], uint16(sample))
	}
	return data
}
