package spectrogram

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

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
		name         string
		width        int
		raw          bool
		ffmpegMajor  int
		hasFfmpegVer bool
		wantDuration bool // Whether we expect -d parameter (always true after #1484 fix)
	}{
		{
			name:         "FFmpeg 7.x, duration always needed",
			width:        800,
			raw:          false,
			ffmpegMajor:  7,
			hasFfmpegVer: true,
			wantDuration: true, // Changed: duration now always required (fixes #1484)
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
			name:         "Raw mode with duration",
			width:        400,
			raw:          true,
			ffmpegMajor:  7,
			hasFfmpegVer: true,
			wantDuration: true, // Changed: duration now always required (fixes #1484)
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
	hasSpectrogram := slices.Contains(args, "spectrogram")
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

// TestAudioDurationCache_Cleanup tests that the cache evicts old entries when exceeding max size
// This addresses issue #1503 where memory accumulates over hours of operation
func TestAudioDurationCache_Cleanup(t *testing.T) {
	// Clear the cache before test
	ClearAudioDurationCache()
	defer ClearAudioDurationCache() // Clean up after test

	// Create test files to get unique cache keys
	tempDir := t.TempDir()

	// Add entries up to the max cache size + some extra
	maxEntries := GetMaxCacheEntries()
	extraEntries := 10

	for i := range maxEntries + extraEntries {
		// Create a real file so cache entry is valid (use index for unique names)
		testFile := filepath.Join(tempDir, "test_"+strconv.Itoa(i)+".wav")
		if err := os.WriteFile(testFile, []byte("test"), 0o644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}

		// Manually add cache entry (simulating what getCachedAudioDuration does)
		AddToCacheForTest(testFile, float64(i), 4)
	}

	// Verify cache size is bounded
	cacheSize := GetAudioDurationCacheSize()
	if cacheSize > maxEntries {
		t.Errorf("Cache size %d exceeds max %d - cache cleanup not working", cacheSize, maxEntries)
	}
}

// TestAudioDurationCache_EvictsOldestEntries tests that oldest entries are evicted first
func TestAudioDurationCache_EvictsOldestEntries(t *testing.T) {
	// Clear the cache before test
	ClearAudioDurationCache()
	defer ClearAudioDurationCache()

	tempDir := t.TempDir()
	maxEntries := GetMaxCacheEntries()

	// Create "old" entry first with timestamp 1 hour ago
	oldFile := filepath.Join(tempDir, "old.wav")
	if err := os.WriteFile(oldFile, []byte("old"), 0o644); err != nil {
		t.Fatalf("Failed to create old test file: %v", err)
	}
	AddToCacheForTestWithTimestamp(oldFile, 1.0, 3, time.Now().Add(-1*time.Hour))

	// Fill cache with newer entries (unique filenames using index)
	for i := range maxEntries {
		testFile := filepath.Join(tempDir, "new_"+strconv.Itoa(i)+".wav")
		if err := os.WriteFile(testFile, []byte("new"), 0o644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
		AddToCacheForTest(testFile, float64(i+10), 3)
	}

	// Old entry should have been evicted (it was the oldest)
	if HasCacheEntry(oldFile) {
		t.Error("Old cache entry should have been evicted but was still present")
	}
}

// TestFFmpegFallback_GetsFreshContext tests that FFmpeg fallback gets adequate time
// even when Sox fails after consuming time from the shared context.
// This addresses issue #1503 where FFmpeg fails with "context canceled".
func TestFFmpegFallback_GetsFreshContext(t *testing.T) {
	// This test verifies that the FFmpeg fallback timeout is independent.
	// We verify this by checking that GetFFmpegFallbackTimeout returns a value
	// greater than zero, ensuring FFmpeg always has dedicated time.
	timeout := GetFFmpegFallbackTimeout()
	if timeout <= 0 {
		t.Errorf("FFmpeg fallback timeout should be positive, got %v", timeout)
	}

	// Verify it's at least 30 seconds (reasonable minimum for FFmpeg processing)
	minTimeout := 30 * time.Second
	if timeout < minTimeout {
		t.Errorf("FFmpeg fallback timeout %v is less than minimum %v", timeout, minTimeout)
	}
}

// TestFFmpegFallback_NotAffectedByParentContext tests that FFmpeg fallback
// succeeds even when the parent context has minimal time remaining.
// This documents the fix for issue #1503.
func TestFFmpegFallback_NotAffectedByParentContext(t *testing.T) {
	// Create a context with very short deadline (simulating exhausted Sox timeout)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Sleep to consume most of the context time
	time.Sleep(50 * time.Millisecond)

	// Verify parent context has minimal time left
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("Context should have deadline")
	}
	remaining := time.Until(deadline)
	if remaining > 100*time.Millisecond {
		t.Fatalf("Context should have minimal time remaining, got %v", remaining)
	}

	// CreateFreshFFmpegContext should return a context with full timeout
	// regardless of parent context state
	ffmpegCtx, ffmpegCancel := CreateFreshFFmpegContext(ctx)
	defer ffmpegCancel()

	// Verify FFmpeg context has adequate time
	ffmpegDeadline, ok := ffmpegCtx.Deadline()
	if !ok {
		t.Fatal("FFmpeg context should have deadline")
	}
	ffmpegRemaining := time.Until(ffmpegDeadline)

	// FFmpeg should have at least 30 seconds
	minRequired := 30 * time.Second
	if ffmpegRemaining < minRequired {
		t.Errorf("FFmpeg context has %v remaining, need at least %v", ffmpegRemaining, minRequired)
	}
}
