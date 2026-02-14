package spectrogram

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSizeToPixels(t *testing.T) {
	tests := []struct {
		name      string
		size      string
		wantWidth int
		wantErr   bool
	}{
		{
			name:      "valid small size",
			size:      "sm",
			wantWidth: 400,
			wantErr:   false,
		},
		{
			name:      "valid medium size",
			size:      "md",
			wantWidth: 800,
			wantErr:   false,
		},
		{
			name:      "valid large size",
			size:      "lg",
			wantWidth: 1000,
			wantErr:   false,
		},
		{
			name:      "valid extra large size",
			size:      "xl",
			wantWidth: 1200,
			wantErr:   false,
		},
		{
			name:      "invalid size",
			size:      "invalid",
			wantWidth: 0,
			wantErr:   true,
		},
		{
			name:      "empty size",
			size:      "",
			wantWidth: 0,
			wantErr:   true,
		},
		{
			name:      "uppercase size",
			size:      "SM",
			wantWidth: 0,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotWidth, err := SizeToPixels(tt.size)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantWidth, gotWidth)
			}
		})
	}
}

func TestPixelsToSize(t *testing.T) {
	tests := []struct {
		name     string
		width    int
		wantSize string
		wantErr  bool
	}{
		{
			name:     "400 pixels to sm",
			width:    400,
			wantSize: "sm",
			wantErr:  false,
		},
		{
			name:     "800 pixels to md",
			width:    800,
			wantSize: "md",
			wantErr:  false,
		},
		{
			name:     "1000 pixels to lg",
			width:    1000,
			wantSize: "lg",
			wantErr:  false,
		},
		{
			name:     "1200 pixels to xl",
			width:    1200,
			wantSize: "xl",
			wantErr:  false,
		},
		{
			name:     "invalid width",
			width:    999,
			wantSize: "",
			wantErr:  true,
		},
		{
			name:     "zero width",
			width:    0,
			wantSize: "",
			wantErr:  true,
		},
		{
			name:     "negative width",
			width:    -100,
			wantSize: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSize, err := PixelsToSize(tt.width)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantSize, gotSize)
			}
		})
	}
}

func TestGetValidSizes(t *testing.T) {
	sizes := GetValidSizes()

	// Check that we have exactly 4 sizes
	assert.Len(t, sizes, 4)

	// Check that sizes are sorted
	expected := []string{"lg", "md", "sm", "xl"}
	assert.Equal(t, expected, sizes)

	// Check that all sizes are valid
	for _, size := range sizes {
		_, err := SizeToPixels(size)
		assert.NoError(t, err, "GetValidSizes() returned invalid size %v", size)
	}
}

func TestBuildSpectrogramPath(t *testing.T) {
	tests := []struct {
		name     string
		clipPath string
		want     string
		wantErr  bool
	}{
		{
			name:     "wav file",
			clipPath: "clips/2024-01-15/Bird_species/Bird_species.2024-01-15T10:00:00.wav",
			want:     "clips/2024-01-15/Bird_species/Bird_species.2024-01-15T10:00:00.png",
			wantErr:  false,
		},
		{
			name:     "flac file",
			clipPath: "clips/test.flac",
			want:     "clips/test.png",
			wantErr:  false,
		},
		{
			name:     "mp3 file",
			clipPath: "/absolute/path/to/audio.mp3",
			want:     "/absolute/path/to/audio.png",
			wantErr:  false,
		},
		{
			name:     "no extension",
			clipPath: "clips/noextension",
			want:     "",
			wantErr:  true,
		},
		{
			name:     "multiple dots",
			clipPath: "clips/file.with.dots.wav",
			want:     "clips/file.with.dots.png",
			wantErr:  false,
		},
		{
			name:     "hidden file",
			clipPath: "clips/.hidden.wav",
			want:     "clips/.hidden.png",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildSpectrogramPath(tt.clipPath)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestBuildSpectrogramPathWithParams(t *testing.T) {
	tests := []struct {
		name      string
		audioPath string
		width     int
		raw       bool
		want      string
		wantErr   bool
	}{
		{
			name:      "sm size, not raw",
			audioPath: "clips/audio.wav",
			width:     400,
			raw:       false,
			want:      "clips/audio.sm.png",
			wantErr:   false,
		},
		{
			name:      "sm size, raw",
			audioPath: "clips/audio.wav",
			width:     400,
			raw:       true,
			want:      "clips/audio.sm.raw.png",
			wantErr:   false,
		},
		{
			name:      "md size, not raw",
			audioPath: "clips/audio.wav",
			width:     800,
			raw:       false,
			want:      "clips/audio.md.png",
			wantErr:   false,
		},
		{
			name:      "md size, raw",
			audioPath: "clips/audio.wav",
			width:     800,
			raw:       true,
			want:      "clips/audio.md.raw.png",
			wantErr:   false,
		},
		{
			name:      "lg size, not raw",
			audioPath: "clips/audio.wav",
			width:     1000,
			raw:       false,
			want:      "clips/audio.lg.png",
			wantErr:   false,
		},
		{
			name:      "xl size, raw",
			audioPath: "clips/audio.wav",
			width:     1200,
			raw:       true,
			want:      "clips/audio.xl.raw.png",
			wantErr:   false,
		},
		{
			name:      "invalid width",
			audioPath: "clips/audio.wav",
			width:     999,
			raw:       false,
			want:      "",
			wantErr:   true,
		},
		{
			name:      "flac file with params",
			audioPath: "/path/to/audio.flac",
			width:     800,
			raw:       true,
			want:      "/path/to/audio.md.raw.png",
			wantErr:   false,
		},
		{
			name:      "file with multiple dots",
			audioPath: "clips/file.with.dots.wav",
			width:     400,
			raw:       false,
			want:      "clips/file.with.dots.sm.png",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildSpectrogramPathWithParams(tt.audioPath, tt.width, tt.raw)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestIsOperationalError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "context canceled",
			err:  context.Canceled,
			want: true,
		},
		{
			name: "context deadline exceeded",
			err:  context.DeadlineExceeded,
			want: true,
		},
		{
			name: "signal killed",
			err:  fmt.Errorf("exit status 137: signal: killed"),
			want: true,
		},
		{
			name: "wrapped context canceled",
			err:  fmt.Errorf("sox failed: %w", context.Canceled),
			want: true,
		},
		{
			name: "wrapped signal killed",
			err:  fmt.Errorf("process failed: signal: killed"),
			want: true,
		},
		{
			name: "generic error",
			err:  fmt.Errorf("disk full"),
			want: false,
		},
		{
			name: "sox binary not found",
			err:  fmt.Errorf("exec: sox: not found"),
			want: false,
		},
		{
			name: "permission denied",
			err:  fmt.Errorf("permission denied"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := IsOperationalError(tt.err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSizeToPixelsRoundTrip(t *testing.T) {
	// Test that SizeToPixels and PixelsToSize are inverse operations
	sizes := GetValidSizes()
	for _, size := range sizes {
		width, err := SizeToPixels(size)
		require.NoError(t, err, "SizeToPixels(%v) failed", size)

		gotSize, err := PixelsToSize(width)
		require.NoError(t, err, "PixelsToSize(%v) failed", width)

		assert.Equal(t, size, gotSize, "Round trip failed: %v -> %v -> %v", size, width, gotSize)
	}
}
