package ffmpeg_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
)

// TestIsValidBatExpansionFactor verifies that only the offered time-expansion
// factors are accepted.
func TestIsValidBatExpansionFactor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		factor int
		want   bool
	}{
		{name: "5x valid", factor: 5, want: true},
		{name: "10x valid", factor: 10, want: true},
		{name: "16x valid", factor: 16, want: true},
		{name: "20x valid", factor: 20, want: true},
		{name: "zero invalid", factor: 0, want: false},
		{name: "negative invalid", factor: -5, want: false},
		{name: "unsupported factor invalid", factor: 8, want: false},
		{name: "large factor invalid", factor: 100, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ffmpeg.IsValidBatExpansionFactor(tt.factor))
		})
	}
}

// TestAudibleBatsOutputSampleRate documents the fixed 48 kHz playback rate of the
// derived audible-bats audio.
func TestAudibleBatsOutputSampleRate(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 48000, ffmpeg.AudibleBatsOutputSampleRate)
}

// TestTimeExpandBatAudioValidation verifies the input validation performed before
// any FFmpeg process is launched, so these cases need no real FFmpeg binary.
// ValidateFFmpegPath only checks the path's shape (non-empty, absolute, not
// contaminated) and never touches the filesystem, so an absolute placeholder
// path under t.TempDir() satisfies it on any OS without requiring FFmpeg to
// actually be installed.
func TestTimeExpandBatAudioValidation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	placeholderFFmpegPath := filepath.Join(t.TempDir(), "ffmpeg")

	t.Run("empty ffmpeg path rejected", func(t *testing.T) {
		t.Parallel()
		err := ffmpeg.TimeExpandBatAudio(ctx, "/tmp/in.wav", "", 10, 256000, "/tmp/out.wav")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "FFmpeg")
	})

	t.Run("invalid expansion factor rejected", func(t *testing.T) {
		t.Parallel()
		err := ffmpeg.TimeExpandBatAudio(ctx, "/tmp/in.wav", placeholderFFmpegPath, 7, 256000, "/tmp/out.wav")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "time-expansion factor")
	})

	t.Run("non-positive source sample rate rejected", func(t *testing.T) {
		t.Parallel()
		err := ffmpeg.TimeExpandBatAudio(ctx, "/tmp/in.wav", placeholderFFmpegPath, 10, 0, "/tmp/out.wav")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "source sample rate")
	})
}
