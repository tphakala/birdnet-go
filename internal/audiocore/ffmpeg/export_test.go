package ffmpeg_test

import (
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/errors"
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

// TestExportAudio_ErrorsAreEnhanced verifies that every error ExportAudio
// returns carries internal/errors telemetry metadata (component + category) so
// FFmpeg export failures reach Sentry tagged, matching the native WAV and FLAC
// export paths. None of these cases require a real FFmpeg binary: the validation
// cases return before exec, and the process-start case uses a bogus path.
func TestExportAudio_ErrorsAreEnhanced(t *testing.T) {
	t.Parallel()

	outDir := t.TempDir()
	pcm := makePCMSilence(t, 1)
	// Absolute on every platform (t.TempDir is absolute) and uncontaminated, so
	// ValidateFFmpegPath passes; never executed in the validation cases.
	validPath := filepath.Join(outDir, "ffmpeg-never-executed")

	// A plain file used as an output parent directory so os.MkdirAll fails.
	parentIsFile := filepath.Join(outDir, "not-a-dir")
	require.NoError(t, os.WriteFile(parentIsFile, []byte("x"), 0o600))

	tests := []struct {
		name          string
		opts          *ffmpeg.ExportOptions
		wantComponent string
		wantCategory  errors.ErrorCategory
		wantOperation string
	}{
		{
			name:          "nil options",
			opts:          nil,
			wantComponent: "audiocore/ffmpeg",
			wantCategory:  errors.CategoryValidation,
			wantOperation: "export_validate",
		},
		{
			name: "empty output path",
			opts: &ffmpeg.ExportOptions{
				PCMData: pcm, OutputPath: "", Format: ffmpeg.FormatMP3,
				SampleRate: 48000, Channels: 1, BitDepth: 16, FFmpegPath: validPath,
			},
			wantComponent: "audiocore/ffmpeg",
			wantCategory:  errors.CategoryValidation,
			wantOperation: "export_validate",
		},
		{
			name: "empty PCM data",
			opts: &ffmpeg.ExportOptions{
				PCMData: nil, OutputPath: filepath.Join(outDir, "out.mp3"), Format: ffmpeg.FormatMP3,
				SampleRate: 48000, Channels: 1, BitDepth: 16, FFmpegPath: validPath,
			},
			wantComponent: "audiocore/ffmpeg",
			wantCategory:  errors.CategoryValidation,
			wantOperation: "export_validate",
		},
		{
			// ValidateFFmpegPath is a shared audiocore helper that already returns a
			// fully enhanced error, so this case (and the relative-path one below)
			// passed before the fix too; they are regression-locks on ExportAudio
			// returning that error directly rather than re-wrapping (double-report).
			name: "empty FFmpeg path",
			opts: &ffmpeg.ExportOptions{
				PCMData: pcm, OutputPath: filepath.Join(outDir, "out.mp3"), Format: ffmpeg.FormatMP3,
				SampleRate: 48000, Channels: 1, BitDepth: 16, FFmpegPath: "",
			},
			wantComponent: "audiocore",
			wantCategory:  errors.CategoryValidation,
			wantOperation: "validate_ffmpeg_path",
		},
		{
			name: "relative FFmpeg path",
			opts: &ffmpeg.ExportOptions{
				PCMData: pcm, OutputPath: filepath.Join(outDir, "out.mp3"), Format: ffmpeg.FormatMP3,
				SampleRate: 48000, Channels: 1, BitDepth: 16, FFmpegPath: "ffmpeg",
			},
			wantComponent: "audiocore",
			wantCategory:  errors.CategoryValidation,
			wantOperation: "validate_ffmpeg_path",
		},
		{
			name: "output directory cannot be created",
			opts: &ffmpeg.ExportOptions{
				PCMData: pcm, OutputPath: filepath.Join(parentIsFile, "out.mp3"), Format: ffmpeg.FormatMP3,
				SampleRate: 48000, Channels: 1, BitDepth: 16, FFmpegPath: validPath,
			},
			wantComponent: "audiocore/ffmpeg",
			wantCategory:  errors.CategoryFileIO,
			wantOperation: "export_create_directory",
		},
		{
			name: "nonexistent FFmpeg binary fails process start",
			opts: &ffmpeg.ExportOptions{
				PCMData: pcm, OutputPath: filepath.Join(outDir, "out.mp3"), Format: ffmpeg.FormatMP3,
				SampleRate: 48000, Channels: 1, BitDepth: 16,
				FFmpegPath: filepath.Join(outDir, "ffmpeg-does-not-exist"),
			},
			wantComponent: "audiocore/ffmpeg",
			wantCategory:  errors.CategoryAudio,
			wantOperation: "export_ffmpeg_start",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ffmpeg.ExportAudio(t.Context(), tt.opts)
			require.Error(t, err)

			var enhanced *errors.EnhancedError
			require.ErrorAs(t, err, &enhanced, "error must carry internal/errors telemetry metadata")
			assert.Equal(t, tt.wantComponent, enhanced.GetComponent(), "component tag")
			assert.Equal(t, tt.wantCategory, enhanced.Category, "category tag")
			// Pin each error to its origin so a future refactor that returns the
			// right component/category from the wrong site is caught.
			assert.Equal(t, tt.wantOperation, enhanced.GetContext()["operation"], "operation context")
		})
	}
}

// TestExportAudio_RuntimeFailuresAreEnhanced covers the FFmpeg-execution and
// finalization error paths using fake POSIX-shell "FFmpeg" binaries, so the
// cmd.Wait() non-zero-exit and os.Rename finalize branches are exercised
// deterministically without a real FFmpeg. POSIX-only; skipped on Windows.
func TestExportAudio_RuntimeFailuresAreEnhanced(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fake shell-script FFmpeg binaries are POSIX-only")
	}

	outDir := t.TempDir()
	pcm := makePCMSilence(t, 1)

	writeFakeBin := func(t *testing.T, name, script string) string {
		t.Helper()
		p := filepath.Join(outDir, name)
		require.NoError(t, os.WriteFile(p, []byte(script), 0o755)) //nolint:gosec // test-only fake binary must be executable
		return p
	}

	// Drains stdin then exits non-zero -> cmd.Wait() reports a non-zero exit.
	waitFailBin := writeFakeBin(t, "wait-fail.sh", "#!/bin/sh\ncat > /dev/null\nexit 1\n")
	// Exits zero but never writes the temp output file -> os.Rename finalize fails.
	renameFailBin := writeFakeBin(t, "rename-fail.sh", "#!/bin/sh\ncat > /dev/null\nexit 0\n")

	tests := []struct {
		name          string
		ffmpegPath    string
		wantCategory  errors.ErrorCategory
		wantOperation string
	}{
		{
			name:          "FFmpeg exits non-zero",
			ffmpegPath:    waitFailBin,
			wantCategory:  errors.CategoryAudio,
			wantOperation: "export_ffmpeg_wait",
		},
		{
			name:          "missing temp output fails finalize rename",
			ffmpegPath:    renameFailBin,
			wantCategory:  errors.CategoryFileIO,
			wantOperation: "export_finalize_rename",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ffmpeg.ExportAudio(t.Context(), &ffmpeg.ExportOptions{
				PCMData:    pcm,
				OutputPath: filepath.Join(outDir, strings.ReplaceAll(tt.name, " ", "_")+".mp3"),
				Format:     ffmpeg.FormatMP3,
				SampleRate: 48000, Channels: 1, BitDepth: 16,
				FFmpegPath: tt.ffmpegPath,
			})
			require.Error(t, err)

			var enhanced *errors.EnhancedError
			require.ErrorAs(t, err, &enhanced, "runtime failure must carry telemetry metadata")
			assert.Equal(t, "audiocore/ffmpeg", enhanced.GetComponent(), "component tag")
			assert.Equal(t, tt.wantCategory, enhanced.Category, "category tag")
			assert.Equal(t, tt.wantOperation, enhanced.GetContext()["operation"], "operation context")
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
