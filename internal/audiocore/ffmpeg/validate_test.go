package ffmpeg_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// makeTestWAVWithSize creates a minimal, well-formed WAV file of the requested byte size.
func makeTestWAVWithSize(t *testing.T, path string, size int64) {
	t.Helper()

	wavHeader := []byte{
		'R', 'I', 'F', 'F',
		0, 0, 0, 0, // ChunkSize, updated below
		'W', 'A', 'V', 'E',
		'f', 'm', 't', ' ',
		16, 0, 0, 0, // Subchunk1Size
		1, 0, // AudioFormat PCM
		2, 0, // NumChannels
		0x44, 0xAC, 0, 0, // SampleRate 44100
		0x10, 0xB1, 0x02, 0, // ByteRate
		4, 0, // BlockAlign
		16, 0, // BitsPerSample
		'd', 'a', 't', 'a',
		0, 0, 0, 0, // Subchunk2Size, updated below
	}

	headerLen := int64(len(wavHeader))
	dataSize := max(size-headerLen, 0)

	chunkSize := uint32(36 + dataSize) //nolint:gosec // G115: test sizes are small
	wavHeader[4] = byte(chunkSize)
	wavHeader[5] = byte(chunkSize >> 8)
	wavHeader[6] = byte(chunkSize >> 16)
	wavHeader[7] = byte(chunkSize >> 24)

	sub2 := uint32(dataSize) //nolint:gosec // G115: test sizes are small
	wavHeader[40] = byte(sub2)
	wavHeader[41] = byte(sub2 >> 8)
	wavHeader[42] = byte(sub2 >> 16)
	wavHeader[43] = byte(sub2 >> 24)

	f, err := os.Create(path) //nolint:gosec // G304: test fixture
	require.NoError(t, err)

	_, err = f.Write(wavHeader)
	require.NoError(t, err)

	if dataSize > 0 {
		_, err = f.Write(make([]byte, dataSize))
		require.NoError(t, err)
	}

	require.NoError(t, f.Close())
}

// TestQuickValidateFile verifies the quick (no-ffprobe) validation path.
func TestQuickValidateFile(t *testing.T) {
	t.Parallel()

	t.Run("valid WAV file", func(t *testing.T) {
		t.Parallel()
		p := filepath.Join(t.TempDir(), "test.wav")
		makeTestWAVWithSize(t, p, 10*1024)

		ok, err := ffmpeg.QuickValidateFile(p)
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("non-existent file", func(t *testing.T) {
		t.Parallel()
		ok, err := ffmpeg.QuickValidateFile(filepath.Join(t.TempDir(), "nonexistent.wav"))
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("file too small", func(t *testing.T) {
		t.Parallel()
		p := filepath.Join(t.TempDir(), "tiny.wav")
		require.NoError(t, os.WriteFile(p, []byte("small"), 0o600))

		ok, err := ffmpeg.QuickValidateFile(p)
		require.NoError(t, err)
		assert.False(t, ok)
	})
}

// TestValidateFile_SmallFile verifies that ValidateFile correctly marks small files
// as invalid. After all attempts are exhausted, an error is returned.
func TestValidateFile_SmallFile(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "tiny.wav")
	require.NoError(t, os.WriteFile(p, []byte("small"), 0o600))

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)

	// Call with 1 attempt to avoid retry delay in tests.
	result, err := ffmpeg.ValidateFile(ctx, p, ffmpeg.WithMaxAttempts(1))
	// After exhausting all attempts, an error is returned.
	require.Error(t, err, "exhausted attempts should produce an error")
	require.NotNil(t, result)
	assert.False(t, result.IsValid)
}

// TestValidateFile_NonExistentFile verifies that ValidateFile returns an error for
// files that do not exist (not a retryable condition).
func TestValidateFile_NonExistentFile(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)

	_, err := ffmpeg.ValidateFile(ctx, filepath.Join(t.TempDir(), "missing.wav"),
		ffmpeg.WithMaxAttempts(1))
	assert.Error(t, err, "non-existent file should produce an error")
}

// TestValidateFile_ValidWAV verifies that a well-formed WAV file is recognised
// as valid. The test requires ffprobe and is skipped if it is not available.
func TestValidateFile_ValidWAV(t *testing.T) {
	t.Parallel()

	ffmpegPath, err := findFFmpegBinary()
	if err != nil {
		t.Skip("FFmpeg not available, skipping full validation test")
	}
	_ = ffmpegPath // ffprobe must be co-located with ffmpeg

	p := filepath.Join(t.TempDir(), "valid.wav")
	makeTestWAVSilence(t, p, 1) // 1 second of real, properly encoded silence

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)

	result, err := ffmpeg.ValidateFile(ctx, p)
	if err != nil {
		// Only skip when ffprobe is genuinely missing; other errors are real failures.
		if result != nil && result.Error != nil && errors.Is(result.Error, ffmpeg.ErrFFprobeNotAvailable) {
			t.Skip("ffprobe not available, skipping full validation test")
		}
		t.Fatalf("ValidateFile returned unexpected error: %v", err)
	}
	require.NotNil(t, result)
	// The file must be marked valid if ffprobe succeeded.
	assert.True(t, result.IsValid, "1-second WAV silence file should be valid")
	assert.True(t, result.IsComplete, "file should be marked complete")
}

// TestValidateFile_CancelledContext verifies that a cancelled context results in
// an error from ValidateFile.
func TestValidateFile_CancelledContext(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "test.wav")
	makeTestWAVWithSize(t, p, 10*1024)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel immediately

	_, err := ffmpeg.ValidateFile(ctx, p)
	assert.Error(t, err)
}

// TestValidateFile_Options verifies that functional options are honoured.
func TestValidateFile_Options(t *testing.T) {
	t.Parallel()

	// A small file that never becomes valid.
	p := filepath.Join(t.TempDir(), "tiny.wav")
	require.NoError(t, os.WriteFile(p, []byte("small"), 0o600))

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)

	start := time.Now()
	_, _ = ffmpeg.ValidateFile(ctx, p,
		ffmpeg.WithMaxAttempts(2),
		ffmpeg.WithRetryDelay(50*time.Millisecond), // much shorter than default
	)
	elapsed := time.Since(start)

	// With 2 attempts and 50 ms initial delay it should finish well under 1 second.
	assert.Less(t, elapsed, 2*time.Second, "validation with short retry should finish quickly")
}

func TestSetAndGetFFprobePath(t *testing.T) {
	original := ffmpeg.GetFFprobePath()
	t.Cleanup(func() { ffmpeg.SetFFprobePath(original) })

	ffmpeg.SetFFprobePath("")
	assert.Empty(t, ffmpeg.GetFFprobePath())

	ffmpeg.SetFFprobePath("/custom/path/to/ffprobe")
	assert.Equal(t, "/custom/path/to/ffprobe", ffmpeg.GetFFprobePath())
}

// makeTestWAVSilence writes a valid PCM WAV file containing silence.
func makeTestWAVSilence(t *testing.T, path string, durationSec int) {
	t.Helper()

	const sampleRate = 48000
	const numChannels = 1
	const bitsPerSample = 16

	numSamples := sampleRate * durationSec * numChannels
	dataSize := numSamples * (bitsPerSample / 8)

	buf := make([]byte, 44+dataSize)
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+dataSize))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(buf[22:24], uint16(numChannels))
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(sampleRate*numChannels*bitsPerSample/8))
	binary.LittleEndian.PutUint16(buf[32:34], uint16(numChannels*bitsPerSample/8))
	binary.LittleEndian.PutUint16(buf[34:36], uint16(bitsPerSample))
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataSize))
	// silence data is already zero-filled.

	require.NoError(t, os.WriteFile(path, buf, 0o600))
}

// findFFmpegBinary locates the FFmpeg binary for testing.
func findFFmpegBinary() (string, error) {
	paths := []string{"/usr/bin/ffmpeg", "/usr/local/bin/ffmpeg"}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	// Try PATH last so absolute paths take priority (consistent with existing tests).
	if path, err := exec.LookPath(conf.GetFfmpegBinaryName()); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("ffmpeg not found")
}

// makeTestWAVCustom creates a WAV file with specific audioFormat, channels, sampleRate, bitsPerSample, and dataSize.
// If includeDataChunk is false, the data chunk is omitted.
// If the file would otherwise be smaller than 2048 bytes, a JUNK chunk is appended so that
// the file exceeds MinValidAudioSize (1024 bytes) and tests the WAV parser rather than the minimum size check.
func makeTestWAVCustom(t *testing.T, path string, audioFormat uint16, channels uint16, sampleRate uint32, bitsPerSample uint16, dataSize uint32, includeDataChunk bool) {
	t.Helper()

	var buf bytes.Buffer
	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(0)) // total size, updated below
	buf.WriteString("WAVE")

	// fmt chunk
	buf.WriteString("fmt ")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(16)) // fmt chunk size
	_ = binary.Write(&buf, binary.LittleEndian, audioFormat)
	_ = binary.Write(&buf, binary.LittleEndian, channels)
	_ = binary.Write(&buf, binary.LittleEndian, sampleRate)
	bytesPerSample := uint32(max(bitsPerSample/8, 1))
	byteRate := sampleRate * uint32(channels) * bytesPerSample
	_ = binary.Write(&buf, binary.LittleEndian, byteRate)
	blockAlign := channels * uint16(bytesPerSample)
	_ = binary.Write(&buf, binary.LittleEndian, blockAlign)
	_ = binary.Write(&buf, binary.LittleEndian, bitsPerSample)

	if includeDataChunk {
		buf.WriteString("data")
		_ = binary.Write(&buf, binary.LittleEndian, dataSize)
		if dataSize > 0 {
			buf.Write(make([]byte, dataSize))
		}
	}

	// Ensure file exceeds MinValidAudioSize (1024 bytes) so validateFile reaches format inspection.
	if buf.Len() < 2048 {
		junkPayloadSize := uint32(2048 - buf.Len())
		buf.WriteString("JUNK")
		_ = binary.Write(&buf, binary.LittleEndian, junkPayloadSize)
		buf.Write(make([]byte, junkPayloadSize))
	}

	data := buf.Bytes()
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))

	require.NoError(t, os.WriteFile(path, data, 0o600))
}

// makeTestWAVTruncated creates a WAV file where the data chunk header declares declaredDataSize,
// but only actualDataSize bytes are written to disk.
func makeTestWAVTruncated(t *testing.T, path string, declaredDataSize, actualDataSize uint32) {
	t.Helper()

	var buf bytes.Buffer
	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(0))
	buf.WriteString("WAVE")

	// fmt chunk (16 bytes)
	buf.WriteString("fmt ")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1)) // PCM
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1)) // mono
	_ = binary.Write(&buf, binary.LittleEndian, uint32(48000))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(96000))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(2))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(16))

	// data chunk with declared size larger than written bytes
	buf.WriteString("data")
	_ = binary.Write(&buf, binary.LittleEndian, declaredDataSize)
	if actualDataSize > 0 {
		buf.Write(make([]byte, actualDataSize))
	}

	data := buf.Bytes()
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))

	require.NoError(t, os.WriteFile(path, data, 0o600))
}

// makeTestWAVIncompleteFmt creates a WAV file with custom fmt chunk size and bitsPerSample.
func makeTestWAVIncompleteFmt(t *testing.T, path string, fmtSize uint32, bitsPerSample uint16) {
	t.Helper()

	var buf bytes.Buffer
	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(0))
	buf.WriteString("WAVE")

	// fmt chunk with custom fmtSize (e.g. 14 bytes)
	buf.WriteString("fmt ")
	_ = binary.Write(&buf, binary.LittleEndian, fmtSize)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1)) // PCM
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1)) // mono
	_ = binary.Write(&buf, binary.LittleEndian, uint32(48000))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(96000))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(2))
	if fmtSize >= 16 {
		_ = binary.Write(&buf, binary.LittleEndian, bitsPerSample)
	}

	// data chunk (2048 bytes so total file > 1024 MinValidAudioSize)
	buf.WriteString("data")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(2048))
	buf.Write(make([]byte, 2048))

	data := buf.Bytes()
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))

	require.NoError(t, os.WriteFile(path, data, 0o600))
}

// makeTestWAVInconsistentFormat creates a WAV file with specific byteRate and blockAlign.
func makeTestWAVInconsistentFormat(t *testing.T, path string, byteRate uint32, blockAlign uint16) {
	t.Helper()

	var buf bytes.Buffer
	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(0))
	buf.WriteString("WAVE")

	// fmt chunk (16 bytes)
	buf.WriteString("fmt ")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1)) // PCM
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1)) // mono
	_ = binary.Write(&buf, binary.LittleEndian, uint32(48000))
	_ = binary.Write(&buf, binary.LittleEndian, byteRate)
	_ = binary.Write(&buf, binary.LittleEndian, blockAlign)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(16))

	// data chunk (2048 bytes so total file > 1024 MinValidAudioSize)
	buf.WriteString("data")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(2048))
	buf.Write(make([]byte, 2048))

	data := buf.Bytes()
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))

	require.NoError(t, os.WriteFile(path, data, 0o600))
}

// makeTestWAVExtendedFmt creates a WAV file with extra bytes in the fmt chunk.
func makeTestWAVExtendedFmt(t *testing.T, path string, extraBytes int) {
	t.Helper()

	var buf bytes.Buffer
	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(0))
	buf.WriteString("WAVE")

	// fmt chunk with extended bytes
	fmtSize := uint32(16 + extraBytes)
	buf.WriteString("fmt ")
	_ = binary.Write(&buf, binary.LittleEndian, fmtSize)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1)) // PCM
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1)) // mono
	_ = binary.Write(&buf, binary.LittleEndian, uint32(48000))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(96000))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(2))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(16))
	if extraBytes > 0 {
		buf.Write(make([]byte, extraBytes))
	}
	if fmtSize%2 != 0 {
		buf.WriteByte(0) // RIFF chunk word-boundary padding
	}

	// data chunk (2048 bytes)
	buf.WriteString("data")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(2048))
	buf.Write(make([]byte, 2048))

	data := buf.Bytes()
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))

	require.NoError(t, os.WriteFile(path, data, 0o600))
}

func TestValidateFile_WAVFastPath_BypassesFFprobe(t *testing.T) {
	// Intentionally set a bogus ffprobe path to ensure the WAV fast-path
	// parses metadata directly without ever invoking ffprobe.
	original := ffmpeg.GetFFprobePath()
	t.Cleanup(func() { ffmpeg.SetFFprobePath(original) })
	ffmpeg.SetFFprobePath("/nonexistent/ffprobe/binary")

	t.Run("valid PCM format 1 succeeds on fast path", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "pcm.wav")
		makeTestWAVWithSize(t, p, 96044) // 44 byte header + 96000 bytes = 1s of 48kHz mono 16-bit

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		t.Cleanup(cancel)

		result, err := ffmpeg.ValidateFile(ctx, p)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.IsValid, "WAV file should be valid via Go fast path")
		assert.True(t, result.IsComplete, "WAV file should be marked complete")
		assert.Equal(t, "pcm_s16le", result.Format)
		assert.Equal(t, 44100, result.SampleRate)
		assert.Equal(t, 2, result.Channels)
		expectedDuration := float64(96000) / float64(44100*2*2)
		assert.InDelta(t, expectedDuration, result.Duration, 0.01)
	})

	t.Run("valid float format 3 succeeds on fast path", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "float.wav")
		// 48000 Hz, 2 channels, 32-bit float, 1 second = 48000 * 2 * 4 = 384000 bytes
		makeTestWAVCustom(t, p, 3, 2, 48000, 32, 384000, true)

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		t.Cleanup(cancel)

		result, err := ffmpeg.ValidateFile(ctx, p)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.IsValid, "Float WAV file should be valid via Go fast path")
		assert.True(t, result.IsComplete, "Float WAV file should be marked complete")
		assert.Equal(t, "pcm_f32le", result.Format)
		assert.Equal(t, 48000, result.SampleRate)
		assert.Equal(t, 2, result.Channels)
		assert.InDelta(t, 1.0, result.Duration, 0.01)
	})

	t.Run("valid PCM format 1 with zero-length data chunk succeeds on fast path", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "empty_data.wav")
		makeTestWAVCustom(t, p, 1, 1, 48000, 16, 0, true)

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		t.Cleanup(cancel)

		result, err := ffmpeg.ValidateFile(ctx, p)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.IsValid, "Zero-length data chunk should be valid")
		assert.Equal(t, 0.0, result.Duration)
	})

	t.Run("WAV with no data chunk falls back to ffprobe", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "no_data.wav")
		makeTestWAVCustom(t, p, 1, 1, 48000, 16, 0, false)

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		t.Cleanup(cancel)

		var gateCalled bool
		gate := func(ctx context.Context) (func(), error) {
			gateCalled = true
			return func() {}, nil
		}

		// Without a data chunk, fast path must return false and fall back to ffprobe.
		// Since ffprobe is set to a nonexistent path, validation must fail.
		result, err := ffmpeg.ValidateFile(ctx, p, ffmpeg.WithMaxAttempts(1), ffmpeg.WithProbeGate(gate))
		assert.Error(t, err)
		assert.True(t, gateCalled, "Probe gate should be acquired when no data chunk falls back to ffprobe")
		if result != nil {
			assert.False(t, result.IsValid)
		}
	})

	t.Run("WAV with unsupported audioFormat falls back to ffprobe", func(t *testing.T) {
		for _, formatCode := range []uint16{6, 7} { // 6 = A-law, 7 = mu-law
			p := filepath.Join(t.TempDir(), fmt.Sprintf("format_%d.wav", formatCode))
			makeTestWAVCustom(t, p, formatCode, 1, 8000, 8, 8000, true)

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			t.Cleanup(cancel)

			var gateCalled bool
			gate := func(ctx context.Context) (func(), error) {
				gateCalled = true
				return func() {}, nil
			}

			// With unsupported format code, fast path must return false and fall back to ffprobe.
			// Since ffprobe is set to a nonexistent path, validation must fail.
			result, err := ffmpeg.ValidateFile(ctx, p, ffmpeg.WithMaxAttempts(1), ffmpeg.WithProbeGate(gate))
			assert.Error(t, err, "Format code %d should not pass fast path", formatCode)
			assert.True(t, gateCalled, "Probe gate should be acquired when unsupported format code falls back to ffprobe")
			if result != nil {
				assert.False(t, result.IsValid)
			}
		}
	})

	t.Run("WAV with truncated data chunk falls back to ffprobe", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "truncated.wav")
		// Declares 100000 bytes for data chunk, but only writes 2048 bytes (total file ~2092 > MinValidAudioSize)
		makeTestWAVTruncated(t, p, 100000, 2048)

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		t.Cleanup(cancel)

		var gateCalled bool
		gate := func(ctx context.Context) (func(), error) {
			gateCalled = true
			return func() {}, nil
		}

		result, err := ffmpeg.ValidateFile(ctx, p, ffmpeg.WithMaxAttempts(1), ffmpeg.WithProbeGate(gate))
		assert.Error(t, err)
		assert.True(t, gateCalled, "Probe gate should be acquired when truncated data chunk falls back to ffprobe")
		if result != nil {
			assert.False(t, result.IsValid)
		}
	})

	t.Run("WAV with incomplete fmt chunk (< 16 bytes) falls back to ffprobe", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "short_fmt.wav")
		// 14 byte fmt chunk
		makeTestWAVIncompleteFmt(t, p, 14, 16)

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		t.Cleanup(cancel)

		var gateCalled bool
		gate := func(ctx context.Context) (func(), error) {
			gateCalled = true
			return func() {}, nil
		}

		result, err := ffmpeg.ValidateFile(ctx, p, ffmpeg.WithMaxAttempts(1), ffmpeg.WithProbeGate(gate))
		assert.Error(t, err)
		assert.True(t, gateCalled, "Probe gate should be acquired when incomplete fmt chunk falls back to ffprobe")
		if result != nil {
			assert.False(t, result.IsValid)
		}
	})

	t.Run("WAV with zero bitsPerSample falls back to ffprobe", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "zero_bits.wav")
		makeTestWAVIncompleteFmt(t, p, 16, 0)

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		t.Cleanup(cancel)

		var gateCalled bool
		gate := func(ctx context.Context) (func(), error) {
			gateCalled = true
			return func() {}, nil
		}

		result, err := ffmpeg.ValidateFile(ctx, p, ffmpeg.WithMaxAttempts(1), ffmpeg.WithProbeGate(gate))
		assert.Error(t, err)
		assert.True(t, gateCalled, "Probe gate should be acquired when zero bitsPerSample falls back to ffprobe")
		if result != nil {
			assert.False(t, result.IsValid)
		}
	})

	t.Run("WAV with inconsistent byteRate or blockAlign falls back to ffprobe", func(t *testing.T) {
		tests := []struct {
			name       string
			byteRate   uint32
			blockAlign uint16
		}{
			{name: "inconsistent byteRate", byteRate: 8000, blockAlign: 2},
			{name: "inconsistent blockAlign", byteRate: 96000, blockAlign: 4},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				p := filepath.Join(t.TempDir(), "inconsistent.wav")
				makeTestWAVInconsistentFormat(t, p, tt.byteRate, tt.blockAlign)

				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				t.Cleanup(cancel)

				var gateCalled bool
				gate := func(ctx context.Context) (func(), error) {
					gateCalled = true
					return func() {}, nil
				}

				result, err := ffmpeg.ValidateFile(ctx, p, ffmpeg.WithMaxAttempts(1), ffmpeg.WithProbeGate(gate))
				assert.Error(t, err)
				assert.True(t, gateCalled, "Probe gate should be acquired when format consistency check fails")
				if result != nil {
					assert.False(t, result.IsValid)
				}
			})
		}
	})

	t.Run("WAV with extended fmt chunk (>16 bytes) succeeds on fast path", func(t *testing.T) {
		for _, extraBytes := range []int{1, 24} {
			p := filepath.Join(t.TempDir(), fmt.Sprintf("extended_%d.wav", extraBytes))
			makeTestWAVExtendedFmt(t, p, extraBytes)

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			t.Cleanup(cancel)

			var gateCalled bool
			gate := func(ctx context.Context) (func(), error) {
				gateCalled = true
				return func() {}, nil
			}

			result, err := ffmpeg.ValidateFile(ctx, p, ffmpeg.WithProbeGate(gate))
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.True(t, result.IsValid)
			assert.False(t, gateCalled, "Probe gate should not be called when fast path succeeds for extended fmt chunk")
			assert.Equal(t, "pcm_s16le", result.Format)
			assert.Equal(t, 48000, result.SampleRate)
			assert.Equal(t, 1, result.Channels)
		}
	})
}

func TestValidateFile_WithProbeGate(t *testing.T) {
	original := ffmpeg.GetFFprobePath()
	t.Cleanup(func() { ffmpeg.SetFFprobePath(original) })
	ffmpeg.SetFFprobePath("/nonexistent/ffprobe/binary")

	t.Run("probe gate is not called for valid WAV fast path", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "fast.wav")
		makeTestWAVWithSize(t, p, 96044)

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		t.Cleanup(cancel)

		var gateCalled bool
		result, err := ffmpeg.ValidateFile(ctx, p, ffmpeg.WithProbeGate(func(ctx context.Context) (func(), error) {
			gateCalled = true
			return func() {}, nil
		}))
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.IsValid)
		assert.False(t, gateCalled, "Probe gate should not be called when fast path succeeds")
	})

	t.Run("probe gate is called and released when falling back to ffprobe", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "fallback.wav")
		// Unsupported format code 6 falls back to ffprobe
		makeTestWAVCustom(t, p, 6, 1, 8000, 8, 8000, true)

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		t.Cleanup(cancel)

		var gateAcquired, gateReleased bool
		_, _ = ffmpeg.ValidateFile(ctx, p,
			ffmpeg.WithMaxAttempts(1),
			ffmpeg.WithProbeGate(func(ctx context.Context) (func(), error) {
				gateAcquired = true
				return func() {
					gateReleased = true
				}, nil
			}),
		)
		assert.True(t, gateAcquired, "Probe gate should be acquired when falling back to probe")
		assert.True(t, gateReleased, "Probe gate should be released after probe execution")
	})
}
