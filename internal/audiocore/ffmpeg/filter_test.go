package ffmpeg_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
)

// TestBuildProcessingFilterChain verifies that the FFmpeg -af filter chain is built
// correctly for the supported combinations of denoise, normalize, and gain.
func TestBuildProcessingFilterChain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		filters     ffmpeg.AudioFilters
		wantEmpty   bool
		wantContain []string
	}{
		{
			name:      "no filters returns empty string",
			filters:   ffmpeg.AudioFilters{},
			wantEmpty: true,
		},
		{
			name:        "gain only",
			filters:     ffmpeg.AudioFilters{GainDB: 6.0},
			wantContain: []string{"volume=+6.0dB"},
		},
		{
			name:        "negative gain",
			filters:     ffmpeg.AudioFilters{GainDB: -3.0},
			wantContain: []string{"volume=-3.0dB"},
		},
		{
			name:        "denoise light",
			filters:     ffmpeg.AudioFilters{Denoise: "light"},
			wantContain: []string{"afftdn=nr=6:nf=-30"},
		},
		{
			name:        "denoise medium",
			filters:     ffmpeg.AudioFilters{Denoise: "medium"},
			wantContain: []string{"afftdn=nr=12:nf=-40"},
		},
		{
			name:        "denoise heavy",
			filters:     ffmpeg.AudioFilters{Denoise: "heavy"},
			wantContain: []string{"afftdn=nr=20:nf=-50"},
		},
		{
			name:        "normalize analysis pass (no LoudnessStats)",
			filters:     ffmpeg.AudioFilters{Normalize: true},
			wantContain: []string{"loudnorm=", "print_format=json"},
		},
		{
			name: "normalize with measured stats (pass 2)",
			filters: ffmpeg.AudioFilters{
				Normalize: true,
				LoudnessStats: &ffmpeg.LoudnessStats{
					InputI:       "-23.0",
					InputTP:      "-2.0",
					InputLRA:     "7.0",
					InputThresh:  "-33.0",
					TargetOffset: "0.0",
				},
			},
			wantContain: []string{"loudnorm=", "linear=true"},
		},
		{
			name: "denoise + gain combined",
			filters: ffmpeg.AudioFilters{
				Denoise: "light",
				GainDB:  3.0,
			},
			wantContain: []string{"afftdn=", "volume=+3.0dB"},
		},
		{
			name:      "unknown denoise preset is ignored",
			filters:   ffmpeg.AudioFilters{Denoise: "ultra"},
			wantEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ffmpeg.BuildProcessingFilterChain(tt.filters)

			if tt.wantEmpty {
				assert.Empty(t, got, "expected empty filter chain")
				return
			}

			for _, want := range tt.wantContain {
				assert.Contains(t, got, want,
					"expected filter chain to contain %q", want)
			}
		})
	}
}

// TestAudioFilters_HasFilters verifies the HasFilters predicate.
func TestAudioFilters_HasFilters(t *testing.T) {
	t.Parallel()

	assert.False(t, ffmpeg.AudioFilters{}.HasFilters(), "zero-value AudioFilters should have no filters")
	assert.True(t, ffmpeg.AudioFilters{GainDB: 1.0}.HasFilters())
	assert.True(t, ffmpeg.AudioFilters{Denoise: "light"}.HasFilters())
	assert.True(t, ffmpeg.AudioFilters{Normalize: true}.HasFilters())
}

// TestIsValidDenoisePreset verifies the preset name validator.
func TestIsValidDenoisePreset(t *testing.T) {
	t.Parallel()

	validPresets := []string{"", "light", "medium", "heavy"}
	for _, p := range validPresets {
		assert.True(t, ffmpeg.IsValidDenoisePreset(p), "expected %q to be valid", p)
	}

	invalidPresets := []string{"ultra", "extreme", "none", "off"}
	for _, p := range invalidPresets {
		assert.False(t, ffmpeg.IsValidDenoisePreset(p), "expected %q to be invalid", p)
	}
}

// TestIsValidGainDB verifies the gain range validator.
func TestIsValidGainDB(t *testing.T) {
	t.Parallel()

	assert.True(t, ffmpeg.IsValidGainDB(0.0), "zero gain should be valid")
	assert.True(t, ffmpeg.IsValidGainDB(6.0), "positive gain should be valid")
	assert.True(t, ffmpeg.IsValidGainDB(-6.0), "negative gain should be valid")
	assert.True(t, ffmpeg.IsValidGainDB(60.0), "max gain should be valid")
	assert.True(t, ffmpeg.IsValidGainDB(-60.0), "min gain should be valid")

	assert.False(t, ffmpeg.IsValidGainDB(61.0), "gain above max should be invalid")
	assert.False(t, ffmpeg.IsValidGainDB(-61.0), "gain below min should be invalid")
}

// TestAnalyzeFileLoudness verifies that loudness analysis runs against a real audio
// file and returns parseable stats. Skipped when FFmpeg is not available.
func TestAnalyzeFileLoudness(t *testing.T) {
	t.Parallel()

	ffmpegPath, err := findFFmpegBinary()
	if err != nil {
		t.Skip("FFmpeg not available:", err)
	}

	testDir := t.TempDir()
	toneFile := testDir + "/tone.wav"
	makeTestWAVTone(t, toneFile, 2, 440.0)

	stats, err := ffmpeg.AnalyzeFileLoudness(t.Context(), toneFile, ffmpegPath, ffmpeg.AudioFilters{}, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, stats.InputI, "InputI should be populated")
}

// TestProcessAudioFile verifies that ProcessAudioFile applies filters and returns
// WAV output. Skipped when FFmpeg is not available.
func TestProcessAudioFile(t *testing.T) {
	t.Parallel()

	ffmpegPath, err := findFFmpegBinary()
	if err != nil {
		t.Skip("FFmpeg not available:", err)
	}

	testDir := t.TempDir()
	silenceFile := testDir + "/silence.wav"
	makeTestWAVSilence(t, silenceFile, 2)

	t.Run("gain only", func(t *testing.T) {
		t.Parallel()
		buf, err := ffmpeg.ProcessAudioFile(t.Context(), silenceFile, ffmpegPath, ffmpeg.AudioFilters{GainDB: 6.0})
		require.NoError(t, err)
		assert.Positive(t, buf.Len())
	})

	t.Run("denoise only", func(t *testing.T) {
		t.Parallel()
		buf, err := ffmpeg.ProcessAudioFile(t.Context(), silenceFile, ffmpegPath, ffmpeg.AudioFilters{Denoise: "light"})
		require.NoError(t, err)
		assert.Positive(t, buf.Len())
	})

	t.Run("no filters returns WAV", func(t *testing.T) {
		t.Parallel()
		buf, err := ffmpeg.ProcessAudioFile(t.Context(), silenceFile, ffmpegPath, ffmpeg.AudioFilters{})
		require.NoError(t, err)
		assert.Positive(t, buf.Len())
	})
}
