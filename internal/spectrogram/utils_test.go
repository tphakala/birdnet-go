package spectrogram

import (
	"testing"
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
			if (err != nil) != tt.wantErr {
				t.Errorf("SizeToPixels() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotWidth != tt.wantWidth {
				t.Errorf("SizeToPixels() = %v, want %v", gotWidth, tt.wantWidth)
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
			if (err != nil) != tt.wantErr {
				t.Errorf("PixelsToSize() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotSize != tt.wantSize {
				t.Errorf("PixelsToSize() = %v, want %v", gotSize, tt.wantSize)
			}
		})
	}
}

func TestGetValidSizes(t *testing.T) {
	sizes := GetValidSizes()

	// Check that we have exactly 4 sizes
	if len(sizes) != 4 {
		t.Errorf("GetValidSizes() returned %d sizes, want 4", len(sizes))
	}

	// Check that sizes are sorted
	expected := []string{"lg", "md", "sm", "xl"}
	for i, size := range sizes {
		if size != expected[i] {
			t.Errorf("GetValidSizes()[%d] = %v, want %v", i, size, expected[i])
		}
	}

	// Check that all sizes are valid
	for _, size := range sizes {
		if _, err := SizeToPixels(size); err != nil {
			t.Errorf("GetValidSizes() returned invalid size %v", size)
		}
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
			if (err != nil) != tt.wantErr {
				t.Errorf("BuildSpectrogramPath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("BuildSpectrogramPath() = %v, want %v", got, tt.want)
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
			if (err != nil) != tt.wantErr {
				t.Errorf("BuildSpectrogramPathWithParams() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("BuildSpectrogramPathWithParams() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSizeToPixelsRoundTrip(t *testing.T) {
	// Test that SizeToPixels and PixelsToSize are inverse operations
	sizes := GetValidSizes()
	for _, size := range sizes {
		width, err := SizeToPixels(size)
		if err != nil {
			t.Errorf("SizeToPixels(%v) failed: %v", size, err)
			continue
		}

		gotSize, err := PixelsToSize(width)
		if err != nil {
			t.Errorf("PixelsToSize(%v) failed: %v", width, err)
			continue
		}

		if gotSize != size {
			t.Errorf("Round trip failed: %v -> %v -> %v", size, width, gotSize)
		}
	}
}
