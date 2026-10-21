package ffmpeg_test

import (
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
			path: "", // placeholder; existence is not checked by ValidateFFmpegPath
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

		// Build a platform-absolute path that does not exist. A literal Unix path
		// like /nonexistent/... is not absolute on Windows (filepath.IsAbs is
		// false there), which would make this fail the absolute-path check it is
		// meant to pass.
		nonExistent := filepath.Join(t.TempDir(), "nonexistent", "ffmpeg")
		err := ffmpeg.ValidateFFmpegPath(nonExistent)
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

// TestValidatePath_RedactsContaminatedTokenInError verifies that the validator
// never put a credential-bearing ingress path into the error (which reaches
// telemetry). The Home Assistant ingress prefix carries a token in the path.
func TestValidatePath_RedactsContaminatedTokenInError(t *testing.T) {
	t.Parallel()

	const token = "super-secret-ingress-token"
	contaminated := "/api/hassio_ingress/" + token + "/usr/bin/ffmpeg"

	ffErr := ffmpeg.ValidateFFmpegPath(contaminated)
	require.Error(t, ffErr)
	assert.NotContains(t, ffErr.Error(), token, "ffmpeg validation error must not leak the ingress token")
	assert.NotContains(t, ffErr.Error(), "hassio_ingress", "ffmpeg validation error must not leak the contaminated path")
}
