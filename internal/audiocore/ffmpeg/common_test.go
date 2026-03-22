package ffmpeg_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
)

// TestValidateFFmpegPath_Valid verifies that a real, absolute ffmpeg path passes validation.
func TestValidateFFmpegPath_Valid(t *testing.T) {
	t.Parallel()

	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not found in PATH, skipping valid-path test")
	}

	// LookPath may return a relative path on some systems; resolve it.
	if !filepath.IsAbs(ffmpegPath) {
		abs, absErr := filepath.Abs(ffmpegPath)
		require.NoError(t, absErr)
		ffmpegPath = abs
	}

	err = ffmpeg.ValidateFFmpegPath(ffmpegPath)
	assert.NoError(t, err, "absolute path to real ffmpeg binary should pass validation")
}

// TestValidateFFmpegPath_Invalid verifies that malformed or suspicious paths are rejected.
func TestValidateFFmpegPath_Invalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
	}{
		{
			name: "empty path",
			path: "",
		},
		{
			name: "relative path",
			path: "usr/bin/ffmpeg",
		},
		{
			name: "path with api prefix contamination",
			path: "/api/v1/ffmpeg",
		},
		{
			name: "path with ingress prefix contamination",
			path: "/ingress/ffmpeg",
		},
		{
			name: "non-existent absolute path is accepted (existence not checked)",
			path: "", // placeholder — existence is not checked by ValidateFFmpegPath
		},
	}

	// All cases except the placeholder should return an error.
	errorCases := tests[:4]
	for _, tt := range errorCases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ffmpeg.ValidateFFmpegPath(tt.path)
			assert.Error(t, err, "path %q should fail validation", tt.path)
		})
	}

	// ValidateFFmpegPath does NOT check whether the file exists; a non-existent
	// absolute path with no proxy contamination should pass.
	t.Run("non-existent absolute path passes (existence not checked)", func(t *testing.T) {
		t.Parallel()

		err := ffmpeg.ValidateFFmpegPath("/nonexistent/path/to/ffmpeg")
		assert.NoError(t, err, "non-existent absolute path should pass path-format validation")
	})
}

// TestGetFFmpegFormat verifies format string output for various sample rate/channel/bit-depth combinations.
func TestGetFFmpegFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		sampleRate     int
		numChannels    int
		bitDepth       int
		wantSampleRate string
		wantChannels   string
		wantFormat     string
	}{
		{
			name:           "48kHz mono 16-bit",
			sampleRate:     48000,
			numChannels:    1,
			bitDepth:       16,
			wantSampleRate: "48000",
			wantChannels:   "1",
			wantFormat:     "s16le",
		},
		{
			name:           "44.1kHz stereo 24-bit",
			sampleRate:     44100,
			numChannels:    2,
			bitDepth:       24,
			wantSampleRate: "44100",
			wantChannels:   "2",
			wantFormat:     "s24le",
		},
		{
			name:           "96kHz mono 32-bit",
			sampleRate:     96000,
			numChannels:    1,
			bitDepth:       32,
			wantSampleRate: "96000",
			wantChannels:   "1",
			wantFormat:     "s32le",
		},
		{
			name:           "unsupported bit depth defaults to s16le",
			sampleRate:     22050,
			numChannels:    1,
			bitDepth:       8,
			wantSampleRate: "22050",
			wantChannels:   "1",
			wantFormat:     "s16le",
		},
		{
			name:           "zero bit depth defaults to s16le",
			sampleRate:     48000,
			numChannels:    2,
			bitDepth:       0,
			wantSampleRate: "48000",
			wantChannels:   "2",
			wantFormat:     "s16le",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotSampleRate, gotChannels, gotFormat := ffmpeg.GetFFmpegFormat(tt.sampleRate, tt.numChannels, tt.bitDepth)
			assert.Equal(t, tt.wantSampleRate, gotSampleRate, "sample rate string mismatch")
			assert.Equal(t, tt.wantChannels, gotChannels, "channels string mismatch")
			assert.Equal(t, tt.wantFormat, gotFormat, "format string mismatch")
		})
	}
}

// TestGetAudioDuration verifies that GetAudioDuration returns the correct duration for a WAV file.
// The test is skipped if sox is not available on the system.
func TestGetAudioDuration(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("sox"); err != nil {
		t.Skip("sox not found in PATH, skipping GetAudioDuration test")
	}

	// Generate a minimal WAV file using sox.
	dir := t.TempDir()
	wavPath := filepath.Join(dir, "test.wav")

	// Use sox to generate 1 second of silence at 48kHz mono 16-bit.
	cmd := exec.Command("sox", "-n", "-r", "48000", "-c", "1", "-b", "16", wavPath, "trim", "0.0", "1.0") //nolint:gosec // G204: fixed test arguments
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "sox failed to create test WAV: %s", string(out))
	require.FileExists(t, wavPath)

	duration, err := ffmpeg.GetAudioDuration(t.Context(), wavPath)
	require.NoError(t, err)
	assert.InDelta(t, 1.0, duration, 0.05, "duration should be approximately 1 second")
}

// TestGetAudioDuration_EmptyPath verifies that an empty path returns an error without calling sox.
func TestGetAudioDuration_EmptyPath(t *testing.T) {
	t.Parallel()

	_, err := ffmpeg.GetAudioDuration(t.Context(), "")
	assert.Error(t, err, "empty path should return an error")
}

// TestGetAudioDuration_NonExistentFile verifies that a non-existent file path returns an error.
func TestGetAudioDuration_NonExistentFile(t *testing.T) {
	t.Parallel()

	if _, lookErr := exec.LookPath("sox"); lookErr != nil {
		t.Skip("sox not found in PATH, skipping non-existent file test")
	}

	_, err := ffmpeg.GetAudioDuration(t.Context(), "/nonexistent/path/audio.wav")
	assert.Error(t, err, "non-existent file should return an error")
}

// TestGetAudioDuration_CanceledContext verifies that a canceled context causes GetAudioDuration to return an error.
func TestGetAudioDuration_CanceledContext(t *testing.T) {
	t.Parallel()

	if _, lookErr := exec.LookPath("sox"); lookErr != nil {
		t.Skip("sox not found in PATH, skipping canceled context test")
	}

	// Write a dummy file so sox can at least attempt to open it.
	dir := t.TempDir()
	dummyPath := filepath.Join(dir, "dummy.wav")
	require.NoError(t, os.WriteFile(dummyPath, []byte("not a real wav"), 0o600))

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel immediately

	_, err := ffmpeg.GetAudioDuration(ctx, dummyPath)
	assert.Error(t, err, "canceled context should return an error")
}
