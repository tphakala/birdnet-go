package processors

import (
	"context"
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore"
)

func TestGainProcessorCreation(t *testing.T) {
	t.Parallel()
	// Valid gain
	proc, err := NewGainProcessor("test-gain", 1.5)
	require.NoError(t, err)
	require.NotNil(t, proc)

	gainProc, ok := proc.(*GainProcessor)
	require.True(t, ok, "Expected processor to be a *GainProcessor")
	assert.Equal(t, "test-gain", gainProc.ID())
	assert.InDelta(t, 1.5, gainProc.GetGain(), 0.01)

	// Invalid gain - too low
	proc, err = NewGainProcessor("test", -1.0)
	require.Error(t, err)
	assert.Nil(t, proc)

	// Invalid gain - too high
	proc, err = NewGainProcessor("test", 11.0)
	require.Error(t, err)
	assert.Nil(t, proc)
}

func TestGainProcessorProcess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("NilInput", func(t *testing.T) {
		t.Parallel()
		proc, err := NewGainProcessor("test-gain", 2.0)
		require.NoError(t, err)
		
		output, err := proc.Process(ctx, nil)
		require.Error(t, err)
		assert.Nil(t, output)
	})

	t.Run("Unity Gain", func(t *testing.T) {
		t.Parallel()
		proc, err := NewGainProcessor("test-gain", 1.0)
		require.NoError(t, err)

		input := &audiocore.AudioData{
			Buffer: []byte{0, 0, 0, 0},
			Format: audiocore.AudioFormat{
				SampleRate: 48000,
				Channels:   1,
				BitDepth:   16,
				Encoding:   "pcm_s16le",
			},
			Timestamp: time.Now(),
			SourceID:  "test",
		}

		output, err := proc.Process(ctx, input)
		require.NoError(t, err)
		assert.Equal(t, input, output)
	})

	t.Run("PCM S16LE Processing", func(t *testing.T) {
		t.Parallel()
		proc, err := NewGainProcessor("test-gain", 2.0)
		require.NoError(t, err)

		// Create test buffer with known values
		buffer := make([]byte, 8)
		var neg1000 int16 = -1000
		var neg16000 int16 = -16000
		binary.LittleEndian.PutUint16(buffer[0:2], uint16(int16(1000)))   //nolint:gosec // G115: intentional int16→uint16 for PCM test data
		binary.LittleEndian.PutUint16(buffer[2:4], uint16(neg1000))     //nolint:gosec // G115: intentional int16→uint16 for PCM test data
		binary.LittleEndian.PutUint16(buffer[4:6], uint16(int16(16000))) //nolint:gosec // G115: intentional int16→uint16 for PCM test data
		binary.LittleEndian.PutUint16(buffer[6:8], uint16(neg16000))    //nolint:gosec // G115: intentional int16→uint16 for PCM test data

		input := &audiocore.AudioData{
			Buffer: buffer,
			Format: audiocore.AudioFormat{
				SampleRate: 48000,
				Channels:   1,
				BitDepth:   16,
				Encoding:   "pcm_s16le",
			},
			Timestamp: time.Now(),
			SourceID:  "test",
		}

		output, err := proc.Process(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, output)

		// Check output values
		assert.Equal(t, int16(2000), int16(binary.LittleEndian.Uint16(output.Buffer[0:2])))   //nolint:gosec // G115: intentional uint16→int16 for PCM test verification
		assert.Equal(t, int16(-2000), int16(binary.LittleEndian.Uint16(output.Buffer[2:4]))) //nolint:gosec // G115: intentional uint16→int16 for PCM test verification
		assert.Equal(t, int16(32000), int16(binary.LittleEndian.Uint16(output.Buffer[4:6]))) //nolint:gosec // G115: intentional uint16→int16 for PCM test verification
		assert.Equal(t, int16(-32000), int16(binary.LittleEndian.Uint16(output.Buffer[6:8]))) //nolint:gosec // G115: intentional uint16→int16 for PCM test verification
	})

	t.Run("PCM S16LE Clipping", func(t *testing.T) {
		t.Parallel()
		proc, err := NewGainProcessor("test-gain", 3.0)
		require.NoError(t, err)

		// Create test buffer with values that will clip
		buffer := make([]byte, 4)
		var neg20000 int16 = -20000
		binary.LittleEndian.PutUint16(buffer[0:2], uint16(int16(20000))) //nolint:gosec // G115: intentional int16→uint16 for PCM test data
		binary.LittleEndian.PutUint16(buffer[2:4], uint16(neg20000))  //nolint:gosec // G115: intentional int16→uint16 for PCM test data

		input := &audiocore.AudioData{
			Buffer: buffer,
			Format: audiocore.AudioFormat{
				SampleRate: 48000,
				Channels:   1,
				BitDepth:   16,
				Encoding:   "pcm_s16le",
			},
			Timestamp: time.Now(),
			SourceID:  "test",
		}

		output, err := proc.Process(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, output)

		// Check clipping
		assert.Equal(t, int16(math.MaxInt16), int16(binary.LittleEndian.Uint16(output.Buffer[0:2]))) //nolint:gosec // G115: intentional uint16→int16 for PCM test verification
		assert.Equal(t, int16(math.MinInt16), int16(binary.LittleEndian.Uint16(output.Buffer[2:4]))) //nolint:gosec // G115: intentional uint16→int16 for PCM test verification
	})

	t.Run("PCM F32LE Processing", func(t *testing.T) {
		t.Parallel()
		proc, err := NewGainProcessor("test-gain", 0.5)
		require.NoError(t, err)

		// Create test buffer with known float values
		buffer := make([]byte, 16)
		binary.LittleEndian.PutUint32(buffer[0:4], math.Float32bits(0.5))
		binary.LittleEndian.PutUint32(buffer[4:8], math.Float32bits(-0.5))
		binary.LittleEndian.PutUint32(buffer[8:12], math.Float32bits(0.8))
		binary.LittleEndian.PutUint32(buffer[12:16], math.Float32bits(-0.8))

		input := &audiocore.AudioData{
			Buffer: buffer,
			Format: audiocore.AudioFormat{
				SampleRate: 48000,
				Channels:   1,
				BitDepth:   32,
				Encoding:   "pcm_f32le",
			},
			Timestamp: time.Now(),
			SourceID:  "test",
		}

		output, err := proc.Process(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, output)

		// Check output values
		assert.InDelta(t, 0.25, math.Float32frombits(binary.LittleEndian.Uint32(output.Buffer[0:4])), 0.001)
		assert.InDelta(t, -0.25, math.Float32frombits(binary.LittleEndian.Uint32(output.Buffer[4:8])), 0.001)
		assert.InDelta(t, 0.4, math.Float32frombits(binary.LittleEndian.Uint32(output.Buffer[8:12])), 0.001)
		assert.InDelta(t, -0.4, math.Float32frombits(binary.LittleEndian.Uint32(output.Buffer[12:16])), 0.001)
	})

	t.Run("PCM F32LE Clipping", func(t *testing.T) {
		t.Parallel()
		proc, err := NewGainProcessor("test-gain", 2.0)
		require.NoError(t, err)

		// Create test buffer with values that will clip
		buffer := make([]byte, 8)
		binary.LittleEndian.PutUint32(buffer[0:4], math.Float32bits(0.8))
		binary.LittleEndian.PutUint32(buffer[4:8], math.Float32bits(-0.8))

		input := &audiocore.AudioData{
			Buffer: buffer,
			Format: audiocore.AudioFormat{
				SampleRate: 48000,
				Channels:   1,
				BitDepth:   32,
				Encoding:   "pcm_f32le",
			},
			Timestamp: time.Now(),
			SourceID:  "test",
		}

		output, err := proc.Process(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, output)

		// Check clipping to [-1.0, 1.0]
		assert.InDelta(t, float32(1.0), math.Float32frombits(binary.LittleEndian.Uint32(output.Buffer[0:4])), 0.01)
		assert.InDelta(t, float32(-1.0), math.Float32frombits(binary.LittleEndian.Uint32(output.Buffer[4:8])), 0.01)
	})

	t.Run("Unsupported Encoding", func(t *testing.T) {
		t.Parallel()
		proc, err := NewGainProcessor("test-gain", 1.5)
		require.NoError(t, err)
		
		input := &audiocore.AudioData{
			Buffer: []byte{0, 0, 0, 0},
			Format: audiocore.AudioFormat{
				SampleRate: 48000,
				Channels:   1,
				BitDepth:   24,
				Encoding:   "pcm_s24le",
			},
			Timestamp: time.Now(),
			SourceID:  "test",
		}

		output, err := proc.Process(ctx, input)
		require.Error(t, err)
		assert.Nil(t, output)
	})

	t.Run("Context Cancellation", func(t *testing.T) {
		t.Parallel()
		proc, err := NewGainProcessor("test-gain", 1.0)
		require.NoError(t, err)
		
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		input := &audiocore.AudioData{
			Buffer: []byte{0, 0, 0, 0},
			Format: audiocore.AudioFormat{
				SampleRate: 48000,
				Channels:   1,
				BitDepth:   16,
				Encoding:   "pcm_s16le",
			},
			Timestamp: time.Now(),
			SourceID:  "test",
		}

		output, err := proc.Process(ctx, input)
		require.Error(t, err)
		assert.Equal(t, context.Canceled, err)
		assert.Nil(t, output)
	})
}

func TestGainProcessorSetGain(t *testing.T) {
	t.Parallel()
	proc, err := NewGainProcessor("test-gain", 1.0)
	require.NoError(t, err)

	gainProc, ok := proc.(*GainProcessor)
	require.True(t, ok, "Expected processor to be a *GainProcessor")

	// Valid gain
	err = gainProc.SetGain(1.5)
	require.NoError(t, err)
	assert.InDelta(t, 1.5, gainProc.GetGain(), 0.01)

	// Invalid gain - too low
	err = gainProc.SetGain(-0.1)
	require.Error(t, err)
	assert.InDelta(t, 1.5, gainProc.GetGain(), 0.01) // Should remain unchanged

	// Invalid gain - too high
	err = gainProc.SetGain(10.1)
	require.Error(t, err)
	assert.InDelta(t, 1.5, gainProc.GetGain(), 0.01) // Should remain unchanged
}

func TestGainProcessorFormats(t *testing.T) {
	t.Parallel()
	proc, err := NewGainProcessor("test-gain", 1.0)
	require.NoError(t, err)

	// Should accept any format
	assert.Nil(t, proc.GetRequiredFormat())

	// Should output same format as input
	inputFormat := audiocore.AudioFormat{
		SampleRate: 44100,
		Channels:   2,
		BitDepth:   24,
		Encoding:   "pcm_s24le",
	}
	outputFormat := proc.GetOutputFormat(inputFormat)
	assert.Equal(t, inputFormat, outputFormat)
}
