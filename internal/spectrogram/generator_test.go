package spectrogram

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/securefs"
)

// TestNewGenerator tests generator creation
func TestNewGenerator(t *testing.T) {
	tempDir := t.TempDir()
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Path = tempDir

	sfs, err := securefs.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create SecureFS: %v", err)
	}

	logger := slog.Default()

	gen := NewGenerator(settings, sfs, logger)
	if gen == nil {
		t.Fatal("NewGenerator() returned nil")
	}
	if gen.settings != settings {
		t.Error("NewGenerator() did not set settings correctly")
	}
	if gen.sfs != sfs {
		t.Error("NewGenerator() did not set sfs correctly")
	}
	// Logger is intentionally wrapped with component context, so we can't check for equality
	// Just verify it's not nil
	if gen.logger == nil {
		t.Error("NewGenerator() did not set logger (logger is nil)")
	}
}

// TestGenerator_EnsureOutputDirectory tests directory creation
func TestGenerator_EnsureOutputDirectory(t *testing.T) {
	tempDir := t.TempDir()
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Path = tempDir

	sfs, err := securefs.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create SecureFS: %v", err)
	}

	gen := NewGenerator(settings, sfs, slog.Default())

	// Test creating a nested directory
	outputPath := filepath.Join(tempDir, "subdir", "test.png")
	err = gen.ensureOutputDirectory(outputPath)
	if err != nil {
		t.Errorf("ensureOutputDirectory() error = %v", err)
	}

	// Verify directory was created
	outputDir := filepath.Dir(outputPath)
	if _, err := os.Stat(outputDir); os.IsNotExist(err) {
		t.Errorf("ensureOutputDirectory() did not create directory: %v", outputDir)
	}
}

// TestGenerator_EnsureOutputDirectory_PathTraversal tests path validation
func TestGenerator_EnsureOutputDirectory_PathTraversal(t *testing.T) {
	tempDir := t.TempDir()
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Path = tempDir

	sfs, err := securefs.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create SecureFS: %v", err)
	}

	gen := NewGenerator(settings, sfs, slog.Default())

	// Test path traversal attempt
	outputPath := filepath.Join(tempDir, "..", "escape", "test.png")
	err = gen.ensureOutputDirectory(outputPath)
	if err == nil {
		t.Error("ensureOutputDirectory() should reject path traversal")
	}
}

// TestGenerator_GetSoxSpectrogramArgs tests Sox argument building
func TestGenerator_GetSoxSpectrogramArgs(t *testing.T) {
	tests := []struct {
		name          string
		width         int
		raw           bool
		ffmpegMajor   int
		hasFfmpegVer  bool
		wantDuration  bool // Whether we expect -d parameter
	}{
		{
			name:         "FFmpeg 7.x, no duration needed",
			width:        800,
			raw:          false,
			ffmpegMajor:  7,
			hasFfmpegVer: true,
			wantDuration: false,
		},
		{
			name:         "FFmpeg 5.x, duration needed",
			width:        800,
			raw:          false,
			ffmpegMajor:  5,
			hasFfmpegVer: true,
			wantDuration: true,
		},
		{
			name:         "FFmpeg unknown, duration needed (safety)",
			width:        800,
			raw:          false,
			ffmpegMajor:  0,
			hasFfmpegVer: false,
			wantDuration: true,
		},
		{
			name:         "Raw mode enabled",
			width:        400,
			raw:          true,
			ffmpegMajor:  7,
			hasFfmpegVer: true,
			wantDuration: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			settings := &conf.Settings{}
			settings.Realtime.Audio.Export.Path = tempDir
			settings.Realtime.Audio.Export.Length = 15 // Fallback duration
			settings.Realtime.Audio.FfmpegMajor = tt.ffmpegMajor
			if tt.hasFfmpegVer {
				settings.Realtime.Audio.FfmpegVersion = "test"
			}

			sfs, err := securefs.New(tempDir)
			if err != nil {
				t.Fatalf("Failed to create SecureFS: %v", err)
			}

			gen := NewGenerator(settings, sfs, slog.Default())

			audioPath := filepath.Join(tempDir, "test.wav")
			outputPath := filepath.Join(tempDir, "test.png")
			args := gen.getSoxSpectrogramArgs(context.Background(), audioPath, outputPath, tt.width, tt.raw)

			// Check for expected parameters
			hasDuration := false
			hasRaw := false
			hasWidth := false
			hasHeight := false

			for i, arg := range args {
				if arg == "-d" && i+1 < len(args) {
					hasDuration = true
				}
				if arg == "-r" {
					hasRaw = true
				}
				if arg == "-x" && i+1 < len(args) {
					hasWidth = true
				}
				if arg == "-y" && i+1 < len(args) {
					hasHeight = true
				}
			}

			if hasDuration != tt.wantDuration {
				t.Errorf("getSoxSpectrogramArgs() duration parameter: got %v, want %v", hasDuration, tt.wantDuration)
			}
			if hasRaw != tt.raw {
				t.Errorf("getSoxSpectrogramArgs() raw parameter: got %v, want %v", hasRaw, tt.raw)
			}
			if !hasWidth {
				t.Error("getSoxSpectrogramArgs() missing -x (width) parameter")
			}
			if !hasHeight {
				t.Error("getSoxSpectrogramArgs() missing -y (height) parameter")
			}
		})
	}
}

// TestGenerator_GetSoxArgs tests full Sox argument building for file input
func TestGenerator_GetSoxArgs(t *testing.T) {
	tempDir := t.TempDir()
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Path = tempDir
	settings.Realtime.Audio.Export.Length = 15
	settings.Realtime.Audio.FfmpegMajor = 7 // Use FFmpeg 7.x to avoid -d parameter

	sfs, err := securefs.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create SecureFS: %v", err)
	}

	gen := NewGenerator(settings, sfs, slog.Default())

	audioPath := filepath.Join(tempDir, "test.wav")
	outputPath := filepath.Join(tempDir, "test.png")

	args := gen.getSoxArgs(context.Background(), audioPath, outputPath, 800, false, SoxInputFile)

	// First argument should be the audio file path
	if len(args) == 0 || args[0] != audioPath {
		t.Errorf("getSoxArgs() first argument should be audio path, got %v", args)
	}

	// Should contain spectrogram parameters
	hasSpectrogram := false
	for _, arg := range args {
		if arg == "spectrogram" {
			hasSpectrogram = true
			break
		}
	}
	if !hasSpectrogram {
		t.Error("getSoxArgs() missing 'spectrogram' parameter")
	}
}

// TestGenerator_GenerateFromPCM_MissingBinary tests error handling for missing Sox
func TestGenerator_GenerateFromPCM_MissingBinary(t *testing.T) {
	tempDir := t.TempDir()
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Path = tempDir
	// Don't set SoxPath - simulate missing binary

	sfs, err := securefs.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create SecureFS: %v", err)
	}

	gen := NewGenerator(settings, sfs, slog.Default())

	outputPath := filepath.Join(tempDir, "test.png")
	pcmData := []byte{0, 1, 2, 3}

	err = gen.GenerateFromPCM(context.Background(), pcmData, outputPath, 400, false)
	if err == nil {
		t.Error("GenerateFromPCM() should error when Sox binary not configured")
	}
}

// TestGenerator_GenerateFromFile_MissingBinaries tests error handling
func TestGenerator_GenerateFromFile_MissingBinaries(t *testing.T) {
	tempDir := t.TempDir()
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Path = tempDir
	// Don't set SoxPath or FfmpegPath

	sfs, err := securefs.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create SecureFS: %v", err)
	}

	gen := NewGenerator(settings, sfs, slog.Default())

	audioPath := filepath.Join(tempDir, "test.wav")
	outputPath := filepath.Join(tempDir, "test.png")

	// Create a dummy audio file
	if err := os.WriteFile(audioPath, []byte("fake audio"), 0o644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	err = gen.GenerateFromFile(context.Background(), audioPath, outputPath, 400, false)
	if err == nil {
		t.Error("GenerateFromFile() should error when binaries not configured")
	}
}
