package ffmpeg_test

import (
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
