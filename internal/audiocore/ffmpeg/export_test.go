package ffmpeg_test

import (
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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

func TestExportAudio_NormalizationBoostsGatedQuietAudio(t *testing.T) {
	t.Parallel()

	ffmpegPath, err := findFFmpegBinary()
	if err != nil {
		t.Skip("FFmpeg not available:", err)
	}

	const sampleRate = 48000
	const amplitude = 3.0
	const freqHz = 3000.0
	numSamples := sampleRate * 2
	pcm := make([]byte, numSamples*2)
	for i := range numSamples {
		sample := amplitude * math.Sin(2.0*math.Pi*freqHz*float64(i)/float64(sampleRate))
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(int16(sample))) //nolint:gosec // G115: amplitude*sin always in int16 range
	}

	outDir := t.TempDir()
	outPath := filepath.Join(outDir, "quiet_norm.flac")

	err = ffmpeg.ExportAudio(t.Context(), &ffmpeg.ExportOptions{
		PCMData:    pcm,
		OutputPath: outPath,
		Format:     ffmpeg.FormatFLAC,
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

	// Minimum RMS level (dBFS) a gated quiet clip must reach to be considered audible.
	const minAudibleRMSdBFS = -35.0
	decoded := decodePCM16(t, ffmpegPath, outPath)
	rmsDB := rmsDBFS(decoded)
	assert.Greater(t, rmsDB, minAudibleRMSdBFS, "quiet clips below loudnorm's gate should still be made audible")

	if sampleRateOut, ok := probeSampleRate(t, outPath); ok {
		assert.Equal(t, sampleRate, sampleRateOut)
	}
}

func decodePCM16(t *testing.T, ffmpegPath, inputPath string) []byte {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), ffmpegPath,
		"-hide_banner",
		"-loglevel", "error",
		"-i", inputPath,
		"-ac", "1",
		"-f", "s16le",
		"pipe:1",
	)
	output, err := cmd.Output()
	require.NoError(t, err)
	require.NotEmpty(t, output)
	return output
}

func rmsDBFS(pcm []byte) float64 {
	if len(pcm) < 2 {
		return math.Inf(-1)
	}
	sampleBytes := len(pcm) - len(pcm)%2
	var sumSquares float64
	var count int
	for i := 0; i < sampleBytes; i += 2 {
		sample := float64(int16(binary.LittleEndian.Uint16(pcm[i:i+2]))) / 32768.0 //nolint:gosec // intentional PCM reinterpretation
		sumSquares += sample * sample
		count++
	}
	if count == 0 {
		return math.Inf(-1)
	}
	rms := math.Sqrt(sumSquares / float64(count))
	if rms <= 0 {
		return math.Inf(-1)
	}
	return 20 * math.Log10(rms)
}

func probeSampleRate(t *testing.T, inputPath string) (int, bool) {
	t.Helper()

	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Log("ffprobe not available, skipping sample-rate assertion")
		return 0, false
	}
	output, err := exec.CommandContext(t.Context(), ffprobePath,
		"-v", "error",
		"-select_streams", "a:0",
		"-show_entries", "stream=sample_rate",
		"-of", "default=nw=1:nk=1",
		inputPath,
	).Output()
	require.NoError(t, err)

	rate, err := strconv.Atoi(strings.TrimSpace(string(output)))
	require.NoError(t, err)
	return rate, true
}
