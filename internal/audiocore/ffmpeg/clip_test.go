package ffmpeg_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
)

// TestExtractClip verifies the basic clip extraction for multiple output formats.
func TestExtractClip(t *testing.T) {
	t.Parallel()

	ffmpegPath, err := findFFmpegBinary()
	if err != nil {
		t.Skip("FFmpeg not available:", err)
	}

	testDir := t.TempDir()
	testFile := filepath.Join(testDir, "test.wav")
	makeTestWAVSilence(t, testFile, 3) // 3 seconds of silence at 48 kHz

	t.Run("extract WAV segment", func(t *testing.T) {
		t.Parallel()
		buf, err := ffmpeg.ExtractClip(t.Context(), &ffmpeg.ClipOptions{
			InputPath:  testFile,
			Start:      0.5,
			End:        2.0,
			Format:     ffmpeg.FormatWAV,
			FFmpegPath: ffmpegPath,
		})
		require.NoError(t, err)
		assert.Positive(t, buf.Len())
	})

	t.Run("extract MP3 segment", func(t *testing.T) {
		t.Parallel()
		buf, err := ffmpeg.ExtractClip(t.Context(), &ffmpeg.ClipOptions{
			InputPath:  testFile,
			Start:      0.0,
			End:        1.5,
			Format:     ffmpeg.FormatMP3,
			FFmpegPath: ffmpegPath,
		})
		require.NoError(t, err)
		assert.Positive(t, buf.Len())
	})

	t.Run("extract FLAC segment", func(t *testing.T) {
		t.Parallel()
		buf, err := ffmpeg.ExtractClip(t.Context(), &ffmpeg.ClipOptions{
			InputPath:  testFile,
			Start:      1.0,
			End:        2.5,
			Format:     ffmpeg.FormatFLAC,
			FFmpegPath: ffmpegPath,
		})
		require.NoError(t, err)
		assert.Positive(t, buf.Len())
	})
}

// TestExtractClip_InvalidInputs verifies that malformed options return errors.
func TestExtractClip_InvalidInputs(t *testing.T) {
	t.Parallel()

	ffmpegPath, err := findFFmpegBinary()
	if err != nil {
		t.Skip("FFmpeg not available:", err)
	}

	testDir := t.TempDir()
	testFile := filepath.Join(testDir, "test.wav")
	makeTestWAVSilence(t, testFile, 3)

	tests := []struct {
		name string
		opts *ffmpeg.ClipOptions
	}{
		{
			name: "end <= start",
			opts: &ffmpeg.ClipOptions{InputPath: testFile, Start: 2.0, End: 1.0, Format: ffmpeg.FormatWAV, FFmpegPath: ffmpegPath},
		},
		{
			name: "negative start",
			opts: &ffmpeg.ClipOptions{InputPath: testFile, Start: -1.0, End: 1.0, Format: ffmpeg.FormatWAV, FFmpegPath: ffmpegPath},
		},
		{
			name: "unsupported format",
			opts: &ffmpeg.ClipOptions{InputPath: testFile, Start: 0.0, End: 1.0, Format: "ogg_vorbis_unsupported", FFmpegPath: ffmpegPath},
		},
		{
			name: "nonexistent input file",
			opts: &ffmpeg.ClipOptions{InputPath: "/nonexistent/file.wav", Start: 0.0, End: 1.0, Format: ffmpeg.FormatWAV, FFmpegPath: ffmpegPath},
		},
		{
			name: "empty FFmpeg path",
			opts: &ffmpeg.ClipOptions{InputPath: testFile, Start: 0.0, End: 1.0, Format: ffmpeg.FormatWAV, FFmpegPath: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ffmpeg.ExtractClip(t.Context(), tt.opts)
			assert.Error(t, err)
		})
	}
}

// TestExtractClip_ContextCancellation verifies that a cancelled context is honoured.
func TestExtractClip_ContextCancellation(t *testing.T) {
	t.Parallel()

	ffmpegPath, err := findFFmpegBinary()
	if err != nil {
		t.Skip("FFmpeg not available:", err)
	}

	testDir := t.TempDir()
	testFile := filepath.Join(testDir, "test.wav")
	makeTestWAVSilence(t, testFile, 3)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel immediately

	_, err = ffmpeg.ExtractClip(ctx, &ffmpeg.ClipOptions{
		InputPath:  testFile,
		Start:      0.0,
		End:        1.0,
		Format:     ffmpeg.FormatWAV,
		FFmpegPath: ffmpegPath,
	})
	assert.Error(t, err)
}

// TestExtractClip_Filters verifies that audio filters are applied during extraction.
func TestExtractClip_Filters(t *testing.T) {
	t.Parallel()

	ffmpegPath, err := findFFmpegBinary()
	if err != nil {
		t.Skip("FFmpeg not available:", err)
	}

	testDir := t.TempDir()
	silenceFile := filepath.Join(testDir, "silence.wav")
	makeTestWAVSilence(t, silenceFile, 3)

	toneFile := filepath.Join(testDir, "tone.wav")
	makeTestWAVTone(t, toneFile, 3, 440.0)

	t.Run("gain filter", func(t *testing.T) {
		t.Parallel()
		filters := ffmpeg.AudioFilters{GainDB: 6.0}
		buf, err := ffmpeg.ExtractClip(t.Context(), &ffmpeg.ClipOptions{
			InputPath:  silenceFile,
			Start:      0.5,
			End:        2.0,
			Format:     ffmpeg.FormatWAV,
			Filters:    &filters,
			FFmpegPath: ffmpegPath,
		})
		require.NoError(t, err)
		assert.Positive(t, buf.Len())
	})

	t.Run("denoise filter", func(t *testing.T) {
		t.Parallel()
		filters := ffmpeg.AudioFilters{Denoise: "medium"}
		buf, err := ffmpeg.ExtractClip(t.Context(), &ffmpeg.ClipOptions{
			InputPath:  silenceFile,
			Start:      0.5,
			End:        2.0,
			Format:     ffmpeg.FormatMP3,
			Filters:    &filters,
			FFmpegPath: ffmpegPath,
		})
		require.NoError(t, err)
		assert.Positive(t, buf.Len())
	})

	t.Run("normalize filter", func(t *testing.T) {
		t.Parallel()
		filters := ffmpeg.AudioFilters{Normalize: true}
		buf, err := ffmpeg.ExtractClip(t.Context(), &ffmpeg.ClipOptions{
			InputPath:  toneFile,
			Start:      0.5,
			End:        2.0,
			Format:     ffmpeg.FormatFLAC,
			Filters:    &filters,
			FFmpegPath: ffmpegPath,
		})
		require.NoError(t, err)
		assert.Positive(t, buf.Len())
	})

	t.Run("nil filters is same as no filters", func(t *testing.T) {
		t.Parallel()
		buf, err := ffmpeg.ExtractClip(t.Context(), &ffmpeg.ClipOptions{
			InputPath:  silenceFile,
			Start:      0.5,
			End:        2.0,
			Format:     ffmpeg.FormatWAV,
			Filters:    nil,
			FFmpegPath: ffmpegPath,
		})
		require.NoError(t, err)
		assert.Positive(t, buf.Len())
	})
}

// TestIsSupportedClipFormat verifies the format support predicate.
func TestIsSupportedClipFormat(t *testing.T) {
	t.Parallel()

	supported := []string{"wav", "mp3", "flac", "opus", "aac", "alac"}
	for _, f := range supported {
		assert.True(t, ffmpeg.IsSupportedClipFormat(f), "expected %q to be supported", f)
	}

	unsupported := []string{"", "ogg_vorbis", "wma", "unknown"}
	for _, f := range unsupported {
		assert.False(t, ffmpeg.IsSupportedClipFormat(f), "expected %q to be unsupported", f)
	}
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

// makeTestWAVTone writes a valid PCM WAV file containing a sine-wave tone.
// loudnorm analysis requires non-silent audio (silence yields -inf integrated loudness).
func makeTestWAVTone(t *testing.T, path string, durationSec int, freqHz float64) {
	t.Helper()

	const sampleRate = 48000
	const numChannels = 1
	const bitsPerSample = 16
	const amplitude = 16000.0

	numSamples := sampleRate * durationSec * numChannels
	dataSize := numSamples * (bitsPerSample / 8)

	buf := make([]byte, 44+dataSize)
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+dataSize))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1)
	binary.LittleEndian.PutUint16(buf[22:24], uint16(numChannels))
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(sampleRate*numChannels*bitsPerSample/8))
	binary.LittleEndian.PutUint16(buf[32:34], uint16(numChannels*bitsPerSample/8))
	binary.LittleEndian.PutUint16(buf[34:36], uint16(bitsPerSample))
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataSize))

	for i := range numSamples {
		sample := amplitude * math.Sin(2.0*math.Pi*freqHz*float64(i)/float64(sampleRate))
		binary.LittleEndian.PutUint16(buf[44+i*2:46+i*2], uint16(int16(sample))) //nolint:gosec // G115: amplitude*sin is always in int16 range
	}

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
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		candidate := filepath.Join(dir, "ffmpeg")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("ffmpeg not found")
}
