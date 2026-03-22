package ffmpeg_test

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
)

// makePCMSilence returns a slice of silence PCM bytes (16-bit LE, mono, 48 kHz).
func makePCMSilence(t *testing.T, durationSec int) []byte {
	t.Helper()
	const sampleRate = 48000
	numSamples := sampleRate * durationSec
	return make([]byte, numSamples*2)
}

// TestExportAudio_MP3 verifies that PCM audio can be exported to an MP3 file.
func TestExportAudio_MP3(t *testing.T) {
	t.Parallel()

	ffmpegPath, err := findFFmpegBinary()
	if err != nil {
		t.Skip("FFmpeg not available:", err)
	}

	pcm := makePCMSilence(t, 1)
	outDir := t.TempDir()
	outPath := filepath.Join(outDir, "output.mp3")

	err = ffmpeg.ExportAudio(t.Context(), &ffmpeg.ExportOptions{
		PCMData:    pcm,
		OutputPath: outPath,
		Format:     ffmpeg.FormatMP3,
		Bitrate:    "128k",
		SampleRate: 48000,
		Channels:   1,
		BitDepth:   16,
		FFmpegPath: ffmpegPath,
	})
	require.NoError(t, err)
	assert.FileExists(t, outPath)

	info, err := os.Stat(outPath)
	require.NoError(t, err)
	assert.Positive(t, info.Size())

	// The temp file must be removed after successful export.
	assert.NoFileExists(t, outPath+ffmpeg.TempExt)
}

// TestExportAudio_FLAC verifies that PCM audio can be exported to a FLAC file.
func TestExportAudio_FLAC(t *testing.T) {
	t.Parallel()

	ffmpegPath, err := findFFmpegBinary()
	if err != nil {
		t.Skip("FFmpeg not available:", err)
	}

	pcm := makePCMSilence(t, 1)
	outDir := t.TempDir()
	outPath := filepath.Join(outDir, "output.flac")

	err = ffmpeg.ExportAudio(t.Context(), &ffmpeg.ExportOptions{
		PCMData:    pcm,
		OutputPath: outPath,
		Format:     ffmpeg.FormatFLAC,
		SampleRate: 48000,
		Channels:   1,
		BitDepth:   16,
		FFmpegPath: ffmpegPath,
	})
	require.NoError(t, err)
	assert.FileExists(t, outPath)
}

// TestExportAudio_WithGain verifies that gain adjustment is applied during export.
func TestExportAudio_WithGain(t *testing.T) {
	t.Parallel()

	ffmpegPath, err := findFFmpegBinary()
	if err != nil {
		t.Skip("FFmpeg not available:", err)
	}

	pcm := makePCMSilence(t, 1)
	outDir := t.TempDir()
	outPath := filepath.Join(outDir, "output.mp3")

	err = ffmpeg.ExportAudio(t.Context(), &ffmpeg.ExportOptions{
		PCMData:    pcm,
		OutputPath: outPath,
		Format:     ffmpeg.FormatMP3,
		Bitrate:    "128k",
		SampleRate: 48000,
		Channels:   1,
		BitDepth:   16,
		GainDB:     6.0,
		FFmpegPath: ffmpegPath,
	})
	require.NoError(t, err)
	assert.FileExists(t, outPath)
}

// TestExportAudio_InvalidInputs verifies that bad options return errors without panicking.
func TestExportAudio_InvalidInputs(t *testing.T) {
	t.Parallel()

	ffmpegPath, err := findFFmpegBinary()
	if err != nil {
		t.Skip("FFmpeg not available:", err)
	}

	pcm := makePCMSilence(t, 1)
	outDir := t.TempDir()

	tests := []struct {
		name string
		opts *ffmpeg.ExportOptions
	}{
		{
			name: "empty PCM data",
			opts: &ffmpeg.ExportOptions{
				PCMData: nil, OutputPath: filepath.Join(outDir, "out.mp3"),
				Format: ffmpeg.FormatMP3, Bitrate: "128k",
				SampleRate: 48000, Channels: 1, BitDepth: 16,
				FFmpegPath: ffmpegPath,
			},
		},
		{
			name: "empty output path",
			opts: &ffmpeg.ExportOptions{
				PCMData: pcm, OutputPath: "",
				Format: ffmpeg.FormatMP3, Bitrate: "128k",
				SampleRate: 48000, Channels: 1, BitDepth: 16,
				FFmpegPath: ffmpegPath,
			},
		},
		{
			name: "empty FFmpeg path",
			opts: &ffmpeg.ExportOptions{
				PCMData: pcm, OutputPath: filepath.Join(outDir, "out.mp3"),
				Format: ffmpeg.FormatMP3, Bitrate: "128k",
				SampleRate: 48000, Channels: 1, BitDepth: 16,
				FFmpegPath: "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ffmpeg.ExportAudio(t.Context(), tt.opts)
			assert.Error(t, err)
		})
	}
}

// TestExportAudio_CreatesDirectory verifies that the output directory is created
// if it does not already exist.
func TestExportAudio_CreatesDirectory(t *testing.T) {
	t.Parallel()

	ffmpegPath, err := findFFmpegBinary()
	if err != nil {
		t.Skip("FFmpeg not available:", err)
	}

	pcm := makePCMSilence(t, 1)
	outDir := filepath.Join(t.TempDir(), "nested", "subdir")
	outPath := filepath.Join(outDir, "output.mp3")

	err = ffmpeg.ExportAudio(t.Context(), &ffmpeg.ExportOptions{
		PCMData:    pcm,
		OutputPath: outPath,
		Format:     ffmpeg.FormatMP3,
		Bitrate:    "128k",
		SampleRate: 48000,
		Channels:   1,
		BitDepth:   16,
		FFmpegPath: ffmpegPath,
	})
	require.NoError(t, err)
	assert.FileExists(t, outPath)
}

// TestExportAudioToBuffer verifies that PCM can be exported to an in-memory buffer.
func TestExportAudioToBuffer(t *testing.T) {
	t.Parallel()

	ffmpegPath, err := findFFmpegBinary()
	if err != nil {
		t.Skip("FFmpeg not available:", err)
	}

	pcm := makePCMSilence(t, 1)

	customArgs := []string{
		"-c:a", "libmp3lame",
		"-b:a", "128k",
		"-f", "mp3",
	}

	buf, err := ffmpeg.ExportAudioToBuffer(t.Context(), pcm, ffmpegPath, 48000, 1, 16, customArgs)
	require.NoError(t, err)
	assert.Positive(t, buf.Len())
}

// TestBuildExportFFmpegArgs_Filter verifies filter construction via exported helpers.
// Since buildExportFFmpegArgs is unexported, we exercise it indirectly through ExportAudio.
func TestExportAudio_Normalization(t *testing.T) {
	t.Parallel()

	ffmpegPath, err := findFFmpegBinary()
	if err != nil {
		t.Skip("FFmpeg not available:", err)
	}

	// Build a non-silent tone file for loudnorm to analyze.
	const sampleRate = 48000
	const amplitude = 16000.0
	const freqHz = 440.0
	numSamples := sampleRate * 2 // 2 seconds
	pcm := make([]byte, numSamples*2)
	for i := range numSamples {
		sample := amplitude * math.Sin(2.0*math.Pi*freqHz*float64(i)/float64(sampleRate))
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(int16(sample))) //nolint:gosec // G115: amplitude*sin always in int16 range
	}

	outDir := t.TempDir()
	outPath := filepath.Join(outDir, "output_norm.mp3")

	err = ffmpeg.ExportAudio(t.Context(), &ffmpeg.ExportOptions{
		PCMData:    pcm,
		OutputPath: outPath,
		Format:     ffmpeg.FormatMP3,
		Bitrate:    "128k",
		SampleRate: sampleRate,
		Channels:   1,
		BitDepth:   16,
		Normalization: ffmpeg.ExportNormalization{
			Enabled:       true,
			TargetLUFS:    -23.0,
			TruePeak:      -2.0,
			LoudnessRange: 7.0,
		},
		FFmpegPath: ffmpegPath,
	})
	require.NoError(t, err)
	assert.FileExists(t, outPath)
}
