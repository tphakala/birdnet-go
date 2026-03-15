package myaudio

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestExtractAudioClip(t *testing.T) {
	t.Parallel()

	// Skip if ffmpeg not available
	ffmpegPath, err := findFFmpeg()
	if err != nil {
		t.Skip("FFmpeg not available:", err)
	}

	// Create a test WAV file (1 second of silence, 48kHz mono 16-bit)
	testDir := t.TempDir()
	testFile := filepath.Join(testDir, "test.wav")
	createTestWAVFile48k(t, testFile, 3) // 3 seconds of silence at 48kHz

	settings := &conf.AudioSettings{
		FfmpegPath: ffmpegPath,
		Export: conf.ExportSettings{
			Bitrate: "192k",
		},
	}

	t.Run("extract WAV segment", func(t *testing.T) {
		t.Parallel()
		buf, err := ExtractAudioClip(t.Context(), testFile, 0.5, 2.0, "wav", settings, nil)
		require.NoError(t, err)
		assert.Positive(t, buf.Len(), "output buffer should not be empty")
	})

	t.Run("extract MP3 segment", func(t *testing.T) {
		t.Parallel()
		buf, err := ExtractAudioClip(t.Context(), testFile, 0.0, 1.5, FormatMP3, settings, nil)
		require.NoError(t, err)
		assert.Positive(t, buf.Len(), "output buffer should not be empty")
	})

	t.Run("extract FLAC segment", func(t *testing.T) {
		t.Parallel()
		buf, err := ExtractAudioClip(t.Context(), testFile, 1.0, 2.5, FormatFLAC, settings, nil)
		require.NoError(t, err)
		assert.Positive(t, buf.Len(), "output buffer should not be empty")
	})

	t.Run("invalid start > end", func(t *testing.T) {
		t.Parallel()
		_, err := ExtractAudioClip(t.Context(), testFile, 2.0, 1.0, "wav", settings, nil)
		assert.Error(t, err)
	})

	t.Run("negative start", func(t *testing.T) {
		t.Parallel()
		_, err := ExtractAudioClip(t.Context(), testFile, -1.0, 1.0, "wav", settings, nil)
		assert.Error(t, err)
	})

	t.Run("nonexistent input file", func(t *testing.T) {
		t.Parallel()
		_, err := ExtractAudioClip(t.Context(), "/nonexistent/file.wav", 0.0, 1.0, "wav", settings, nil)
		assert.Error(t, err)
	})

	t.Run("unsupported format", func(t *testing.T) {
		t.Parallel()
		_, err := ExtractAudioClip(t.Context(), testFile, 0.0, 1.0, "ogg_vorbis_invalid", settings, nil)
		assert.Error(t, err)
	})

	t.Run("context cancellation", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel() // Cancel immediately
		_, err := ExtractAudioClip(ctx, testFile, 0.0, 1.0, "wav", settings, nil)
		assert.Error(t, err)
	})
}

// createTestWAVFile48k creates a valid WAV file with silence at 48kHz for testing.
func createTestWAVFile48k(t *testing.T, path string, durationSec int) {
	t.Helper()

	sampleRate := 48000
	numChannels := 1
	bitsPerSample := 16
	numSamples := sampleRate * durationSec * numChannels
	dataSize := numSamples * (bitsPerSample / 8)

	// WAV header (44 bytes)
	header := make([]byte, 44)
	// RIFF chunk
	copy(header[0:4], "RIFF")
	writeLE32(header, 4, uint32(36+dataSize))
	copy(header[8:12], "WAVE")
	// fmt subchunk
	copy(header[12:16], "fmt ")
	writeLE32(header, 16, 16) // subchunk size
	writeLE16(header, 20, 1)  // PCM format
	writeLE16(header, 22, uint16(numChannels))
	writeLE32(header, 24, uint32(sampleRate))
	writeLE32(header, 28, uint32(sampleRate*numChannels*bitsPerSample/8)) // byte rate
	writeLE16(header, 32, uint16(numChannels*bitsPerSample/8))            // block align
	writeLE16(header, 34, uint16(bitsPerSample))
	// data subchunk
	copy(header[36:40], "data")
	writeLE32(header, 40, uint32(dataSize))

	// Write header + silence data
	data := make([]byte, 44+dataSize)
	copy(data, header)
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

func writeLE16(buf []byte, offset int, val uint16) {
	buf[offset] = byte(val)
	buf[offset+1] = byte(val >> 8)
}

func writeLE32(buf []byte, offset int, val uint32) {
	buf[offset] = byte(val)
	buf[offset+1] = byte(val >> 8)
	buf[offset+2] = byte(val >> 16)
	buf[offset+3] = byte(val >> 24)
}

func TestExtractAudioClipWithFilters(t *testing.T) {
	t.Parallel()

	ffmpegPath, err := findFFmpeg()
	if err != nil {
		t.Skip("FFmpeg not available:", err)
	}

	testDir := t.TempDir()

	// Silence file for non-normalize tests
	testFile := filepath.Join(testDir, "test.wav")
	createTestWAVFile48k(t, testFile, 3)

	// Tone file for normalize tests (loudnorm requires non-silent audio)
	toneFile := filepath.Join(testDir, "tone.wav")
	createTestWAVFileWithTone(t, toneFile, 3, 440.0)

	settings := &conf.AudioSettings{FfmpegPath: ffmpegPath}

	t.Run("clip with gain filter", func(t *testing.T) {
		t.Parallel()
		filters := AudioFilters{GainDB: 6.0}
		buf, err := ExtractAudioClip(t.Context(), testFile, 0.5, 2.0, "wav", settings, &filters)
		require.NoError(t, err)
		assert.Positive(t, buf.Len())
	})

	t.Run("clip with denoise filter", func(t *testing.T) {
		t.Parallel()
		filters := AudioFilters{Denoise: "medium"}
		buf, err := ExtractAudioClip(t.Context(), testFile, 0.5, 2.0, FormatMP3, settings, &filters)
		require.NoError(t, err)
		assert.Positive(t, buf.Len())
	})

	t.Run("clip with normalize", func(t *testing.T) {
		t.Parallel()
		filters := AudioFilters{Normalize: true}
		buf, err := ExtractAudioClip(t.Context(), toneFile, 0.5, 2.0, FormatFLAC, settings, &filters)
		require.NoError(t, err)
		assert.Positive(t, buf.Len())
	})

	t.Run("clip with nil filters is same as no filters", func(t *testing.T) {
		t.Parallel()
		buf, err := ExtractAudioClip(t.Context(), testFile, 0.5, 2.0, "wav", settings, nil)
		require.NoError(t, err)
		assert.Positive(t, buf.Len())
	})
}

// findFFmpeg locates the FFmpeg binary for testing.
func findFFmpeg() (string, error) {
	// Try PATH first
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p, nil
	}

	// Fall back to common paths
	paths := []string{"/usr/bin/ffmpeg", "/usr/local/bin/ffmpeg"}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("ffmpeg not found")
}
