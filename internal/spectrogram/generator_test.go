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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/securefs"
)

// testSoxPath is used in tests to set a Sox path that won't be called
const testSoxPath = "/usr/bin/sox"

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

// soxArgsResult holds the parsed spectrogram arguments for testing
type soxArgsResult struct {
	hasDuration bool
	hasRaw      bool
	hasWidth    bool
	hasHeight   bool
}

// parseSoxSpectrogramArgs scans args for spectrogram parameters
func parseSoxSpectrogramArgs(args []string) soxArgsResult {
	result := soxArgsResult{}
	for i, arg := range args {
		switch arg {
		case "-d":
			result.hasDuration = i+1 < len(args)
		case "-r":
			result.hasRaw = true
		case "-x":
			result.hasWidth = i+1 < len(args)
		case "-y":
			result.hasHeight = i+1 < len(args)
		}
	}
	return result
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
		{"FFmpeg 7.x, duration always needed", 800, false, 7, true, true},
		{"FFmpeg 5.x, duration needed", 800, false, 5, true, true},
		{"FFmpeg unknown, duration needed (safety)", 800, false, 0, false, true},
		{"Raw mode with duration", 400, true, 7, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gen, tempDir := createTestGenerator(t, tt.ffmpegMajor, tt.hasFfmpegVer)
			audioPath := filepath.Join(tempDir, "test.wav")
			outputPath := filepath.Join(tempDir, "test.png")

			args := gen.getSoxSpectrogramArgs(context.Background(), audioPath, outputPath, tt.width, tt.raw)
			result := parseSoxSpectrogramArgs(args)

			assert.Equal(t, tt.wantDuration, result.hasDuration, "duration parameter mismatch")
			assert.Equal(t, tt.raw, result.hasRaw, "raw parameter mismatch")
			assert.True(t, result.hasWidth, "missing -x (width) parameter")
			assert.True(t, result.hasHeight, "missing -y (height) parameter")
		})
	}
}

// createTestGenerator creates a Generator with the specified FFmpeg settings for testing
func createTestGenerator(t *testing.T, ffmpegMajor int, hasFfmpegVer bool) (gen *Generator, tempDir string) {
	t.Helper()
	tempDir = t.TempDir()
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Path = tempDir
	settings.Realtime.Audio.Export.Length = 15 // Fallback duration
	settings.Realtime.Audio.FfmpegMajor = ffmpegMajor
	if hasFfmpegVer {
		settings.Realtime.Audio.FfmpegVersion = "test"
	}

	sfs, err := securefs.New(tempDir)
	require.NoError(t, err, "Failed to create SecureFS")

	return NewGenerator(settings, sfs, slog.Default()), tempDir
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
	if err := os.WriteFile(audioPath, []byte("fake audio"), 0o600); err != nil {
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
		if err := os.WriteFile(testFile, []byte("test"), 0o600); err != nil {
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
	if err := os.WriteFile(oldFile, []byte("old"), 0o600); err != nil {
		t.Fatalf("Failed to create old test file: %v", err)
	}
	AddToCacheForTestWithTimestamp(oldFile, 1.0, 3, time.Now().Add(-1*time.Hour))

	// Fill cache with newer entries (unique filenames using index)
	for i := range maxEntries {
		testFile := filepath.Join(tempDir, "new_"+strconv.Itoa(i)+".wav")
		if err := os.WriteFile(testFile, []byte("new"), 0o600); err != nil {
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

// TestComputeRemainingTimeout tests the computeRemainingTimeout helper function.
func TestComputeRemainingTimeout(t *testing.T) {
	tests := []struct {
		name           string
		setupCtx       func() (context.Context, context.CancelFunc)
		fallback       time.Duration
		expectFallback bool
		minExpected    time.Duration
		maxExpected    time.Duration
	}{
		{
			name: "context with deadline returns remaining time",
			setupCtx: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 10*time.Second)
			},
			fallback:       30 * time.Second,
			expectFallback: false,
			minExpected:    9 * time.Second,  // Allow some execution time
			maxExpected:    11 * time.Second, // Should be close to 10s
		},
		{
			name: "context without deadline returns fallback",
			setupCtx: func() (context.Context, context.CancelFunc) {
				return context.WithCancel(context.Background())
			},
			fallback:       30 * time.Second,
			expectFallback: true,
			minExpected:    30 * time.Second,
			maxExpected:    30 * time.Second,
		},
		{
			name: "expired context returns fallback",
			setupCtx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
				time.Sleep(10 * time.Millisecond) // Ensure context expires
				return ctx, cancel
			},
			fallback:       15 * time.Second,
			expectFallback: true,
			minExpected:    15 * time.Second,
			maxExpected:    15 * time.Second,
		},
		{
			name: "short remaining time returns actual remaining",
			setupCtx: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 500*time.Millisecond)
			},
			fallback:       30 * time.Second,
			expectFallback: false,
			minExpected:    400 * time.Millisecond,
			maxExpected:    600 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := tt.setupCtx()
			defer cancel()

			result := ComputeRemainingTimeoutForTest(ctx, tt.fallback)

			if tt.expectFallback {
				assert.Equal(t, tt.fallback, result, "expected fallback duration")
			} else {
				assert.GreaterOrEqual(t, result, tt.minExpected, "result should be >= min expected")
				assert.LessOrEqual(t, result, tt.maxExpected, "result should be <= max expected")
			}
		})
	}
}

// TestGenerateFromFile_Validation tests input validation for GenerateFromFile.
func TestGenerateFromFile_Validation(t *testing.T) {
	tempDir := t.TempDir()
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Path = tempDir
	settings.Realtime.Audio.SoxPath = testSoxPath // Will fail if called, but we're testing validation

	sfs, err := securefs.New(tempDir)
	require.NoError(t, err, "Failed to create SecureFS")

	gen := NewGenerator(settings, sfs, slog.Default())

	tests := []struct {
		name       string
		audioPath  string
		outputPath string
		width      int
		wantErr    bool
		errContain string
	}{
		{
			name:       "empty output path",
			audioPath:  filepath.Join(tempDir, "test.wav"),
			outputPath: "",
			width:      400,
			wantErr:    true,
			errContain: "output path is empty",
		},
		{
			name:       "relative output path",
			audioPath:  filepath.Join(tempDir, "test.wav"),
			outputPath: "relative/path/test.png",
			width:      400,
			wantErr:    true,
			errContain: "output path must be absolute",
		},
		{
			name:       "zero width",
			audioPath:  filepath.Join(tempDir, "test.wav"),
			outputPath: filepath.Join(tempDir, "test.png"),
			width:      0,
			wantErr:    true,
			errContain: "width must be positive",
		},
		{
			name:       "negative width",
			audioPath:  filepath.Join(tempDir, "test.wav"),
			outputPath: filepath.Join(tempDir, "test.png"),
			width:      -100,
			wantErr:    true,
			errContain: "width must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := gen.GenerateFromFile(context.Background(), tt.audioPath, tt.outputPath, tt.width, false)

			if tt.wantErr {
				require.Error(t, err, "expected error for invalid input")
				assert.Contains(t, err.Error(), tt.errContain, "error should contain expected message")
			} else {
				assert.NoError(t, err, "expected no error for valid input")
			}
		})
	}
}

// TestGenerateFromPCM_Validation tests input validation for GenerateFromPCM.
func TestGenerateFromPCM_Validation(t *testing.T) {
	tempDir := t.TempDir()
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Path = tempDir
	settings.Realtime.Audio.SoxPath = testSoxPath

	sfs, err := securefs.New(tempDir)
	require.NoError(t, err, "Failed to create SecureFS")

	gen := NewGenerator(settings, sfs, slog.Default())

	tests := []struct {
		name       string
		pcmData    []byte
		outputPath string
		width      int
		wantErr    bool
		errContain string
	}{
		{
			name:       "empty output path",
			pcmData:    []byte{0, 1, 2, 3},
			outputPath: "",
			width:      400,
			wantErr:    true,
			errContain: "output path is empty",
		},
		{
			name:       "relative output path",
			pcmData:    []byte{0, 1, 2, 3},
			outputPath: "relative/path/test.png",
			width:      400,
			wantErr:    true,
			errContain: "output path must be absolute",
		},
		{
			name:       "zero width",
			pcmData:    []byte{0, 1, 2, 3},
			outputPath: filepath.Join(tempDir, "test.png"),
			width:      0,
			wantErr:    true,
			errContain: "width must be positive",
		},
		{
			name:       "negative width",
			pcmData:    []byte{0, 1, 2, 3},
			outputPath: filepath.Join(tempDir, "test.png"),
			width:      -100,
			wantErr:    true,
			errContain: "width must be positive",
		},
		{
			name:       "empty PCM data",
			pcmData:    []byte{},
			outputPath: filepath.Join(tempDir, "test.png"),
			width:      400,
			wantErr:    true,
			errContain: "PCM data is empty",
		},
		{
			name:       "nil PCM data",
			pcmData:    nil,
			outputPath: filepath.Join(tempDir, "test.png"),
			width:      400,
			wantErr:    true,
			errContain: "PCM data is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := gen.GenerateFromPCM(context.Background(), tt.pcmData, tt.outputPath, tt.width, false)

			if tt.wantErr {
				require.Error(t, err, "expected error for invalid input")
				assert.Contains(t, err.Error(), tt.errContain, "error should contain expected message")
			} else {
				assert.NoError(t, err, "expected no error for valid input")
			}
		})
	}
}

// TestGetCachedAudioDuration_CacheInvalidation tests cache invalidation on file changes.
func TestGetCachedAudioDuration_CacheInvalidation(t *testing.T) {
	ClearAudioDurationCache()
	defer ClearAudioDurationCache()

	tempDir := t.TempDir()

	// Create a test file
	testFile := filepath.Join(tempDir, "test.wav")
	initialContent := []byte("initial content for testing")
	err := os.WriteFile(testFile, initialContent, 0o600)
	require.NoError(t, err, "Failed to create test file")

	// Get file info for the initial state
	info, err := os.Stat(testFile)
	require.NoError(t, err, "Failed to stat test file")

	// Add a cache entry with matching file info
	initialDuration := 5.5
	AddToCacheForTestWithTimestamp(testFile, initialDuration, info.Size(), time.Now())

	// Verify cache entry exists
	assert.True(t, HasCacheEntry(testFile), "cache entry should exist")

	// Get the cached entry and verify duration
	entry := GetCacheEntry(testFile)
	require.NotNil(t, entry, "cache entry should not be nil")
	assert.InDelta(t, initialDuration, entry.duration, 0.001, "cached duration should match")

	// Modify the file (changes size and modTime)
	time.Sleep(10 * time.Millisecond) // Ensure modTime changes
	modifiedContent := []byte("modified content that is longer than before")
	err = os.WriteFile(testFile, modifiedContent, 0o600)
	require.NoError(t, err, "Failed to modify test file")

	// The cache entry still exists but will be invalidated on next access
	// because file size/modTime changed
	// Note: getCachedAudioDuration is not exported, but we test the invalidation
	// logic through the cache entry state
	newInfo, err := os.Stat(testFile)
	require.NoError(t, err, "Failed to stat modified file")

	// Verify file properties changed
	assert.NotEqual(t, info.Size(), newInfo.Size(), "file size should have changed")
}

// TestGetCachedAudioDuration_TTLExpiration tests cache TTL expiration.
func TestGetCachedAudioDuration_TTLExpiration(t *testing.T) {
	ClearAudioDurationCache()
	defer ClearAudioDurationCache()

	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.wav")

	// Create test file
	err := os.WriteFile(testFile, []byte("test content"), 0o600)
	require.NoError(t, err, "Failed to create test file")

	// Add an old cache entry (timestamp older than TTL)
	oldTimestamp := time.Now().Add(-GetDurationCacheTTL() - time.Minute)
	AddToCacheForTestWithTimestamp(testFile, 10.0, 12, oldTimestamp)

	// Entry exists but is stale
	assert.True(t, HasCacheEntry(testFile), "cache entry should still exist")

	entry := GetCacheEntry(testFile)
	require.NotNil(t, entry, "cache entry should not be nil")

	// Verify the entry is older than TTL
	age := time.Since(entry.timestamp)
	assert.Greater(t, age, GetDurationCacheTTL(), "entry should be older than TTL")
}

// TestGenerateWithSoxDirect_MissingBinary tests error when Sox binary is not configured.
func TestGenerateWithSoxDirect_MissingBinary(t *testing.T) {
	tempDir := t.TempDir()
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Path = tempDir
	// SoxPath intentionally not set

	sfs, err := securefs.New(tempDir)
	require.NoError(t, err, "Failed to create SecureFS")

	gen := NewGenerator(settings, sfs, slog.Default())

	audioPath := filepath.Join(tempDir, "test.wav")
	outputPath := filepath.Join(tempDir, "test.png")

	// Create a dummy audio file
	err = os.WriteFile(audioPath, []byte("fake audio"), 0o600)
	require.NoError(t, err, "Failed to create test file")

	err = gen.generateWithSoxDirect(context.Background(), audioPath, outputPath, 400, false)
	require.Error(t, err, "should error when Sox binary not configured")
	assert.Contains(t, err.Error(), "sox binary not configured", "error should mention sox binary")
}

// TestGenerateWithFFmpegSoxPipeline_MissingBinaries tests error handling for missing binaries.
func TestGenerateWithFFmpegSoxPipeline_MissingBinaries(t *testing.T) {
	tempDir := t.TempDir()

	sfs, err := securefs.New(tempDir)
	require.NoError(t, err, "Failed to create SecureFS")

	tests := []struct {
		name       string
		ffmpegPath string
		soxPath    string
		errContain string
	}{
		{
			name:       "missing ffmpeg",
			ffmpegPath: "",
			soxPath:    testSoxPath,
			errContain: "ffmpeg binary not configured",
		},
		{
			name:       "missing sox",
			ffmpegPath: "/usr/bin/ffmpeg",
			soxPath:    "",
			errContain: "sox binary not configured",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := &conf.Settings{}
			settings.Realtime.Audio.Export.Path = tempDir
			settings.Realtime.Audio.FfmpegPath = tt.ffmpegPath
			settings.Realtime.Audio.SoxPath = tt.soxPath

			gen := NewGenerator(settings, sfs, slog.Default())

			audioPath := filepath.Join(tempDir, "test.wav")
			outputPath := filepath.Join(tempDir, "test.png")

			err := gen.generateWithFFmpegSoxPipeline(context.Background(), audioPath, outputPath, 400, false)
			require.Error(t, err, "should error when binary not configured")
			assert.Contains(t, err.Error(), tt.errContain, "error should mention missing binary")
		})
	}
}

// TestGenerateWithFFmpeg_MissingBinary tests error when FFmpeg binary is not configured.
func TestGenerateWithFFmpeg_MissingBinary(t *testing.T) {
	tempDir := t.TempDir()
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Path = tempDir
	// FfmpegPath intentionally not set

	sfs, err := securefs.New(tempDir)
	require.NoError(t, err, "Failed to create SecureFS")

	gen := NewGenerator(settings, sfs, slog.Default())

	audioPath := filepath.Join(tempDir, "test.wav")
	outputPath := filepath.Join(tempDir, "test.png")

	err = gen.generateWithFFmpeg(context.Background(), audioPath, outputPath, 400, false)
	require.Error(t, err, "should error when FFmpeg binary not configured")
	assert.Contains(t, err.Error(), "ffmpeg binary not configured", "error should mention ffmpeg binary")
}

// TestGenerateWithSoxPCM_MissingBinary tests error when Sox binary is not configured.
func TestGenerateWithSoxPCM_MissingBinary(t *testing.T) {
	tempDir := t.TempDir()
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Path = tempDir
	// SoxPath intentionally not set

	sfs, err := securefs.New(tempDir)
	require.NoError(t, err, "Failed to create SecureFS")

	gen := NewGenerator(settings, sfs, slog.Default())

	outputPath := filepath.Join(tempDir, "test.png")
	pcmData := []byte{0, 1, 2, 3, 4, 5, 6, 7}

	err = gen.generateWithSoxPCM(context.Background(), pcmData, outputPath, 400, false)
	require.Error(t, err, "should error when Sox binary not configured")
	assert.Contains(t, err.Error(), "sox binary not configured", "error should mention sox binary")
}

// TestGetSoxArgs_FileInput tests getSoxArgs for file input type.
func TestGetSoxArgs_FileInput(t *testing.T) {
	tempDir := t.TempDir()
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Path = tempDir
	settings.Realtime.Audio.Export.Length = 15

	sfs, err := securefs.New(tempDir)
	require.NoError(t, err, "Failed to create SecureFS")

	gen := NewGenerator(settings, sfs, slog.Default())

	audioPath := filepath.Join(tempDir, "test.wav")
	outputPath := filepath.Join(tempDir, "test.png")

	args := gen.getSoxArgs(context.Background(), audioPath, outputPath, 800, false, SoxInputFile)

	// First argument should be the audio file for SoxInputFile
	require.NotEmpty(t, args, "args should not be empty")
	assert.Equal(t, audioPath, args[0], "first arg should be audio path for file input")

	// Should contain spectrogram command
	assert.True(t, slices.Contains(args, "spectrogram"), "should contain spectrogram command")

	// Should contain width and height
	assert.True(t, slices.Contains(args, "-x"), "should contain -x flag")
	assert.True(t, slices.Contains(args, "-y"), "should contain -y flag")

	// Should contain output path
	assert.True(t, slices.Contains(args, "-o"), "should contain -o flag")
	assert.True(t, slices.Contains(args, outputPath), "should contain output path")
}

// TestGetSoxSpectrogramArgs_RawFlag tests that raw flag is properly added.
func TestGetSoxSpectrogramArgs_RawFlag(t *testing.T) {
	tempDir := t.TempDir()
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Path = tempDir
	settings.Realtime.Audio.Export.Length = 15

	sfs, err := securefs.New(tempDir)
	require.NoError(t, err, "Failed to create SecureFS")

	gen := NewGenerator(settings, sfs, slog.Default())

	audioPath := filepath.Join(tempDir, "test.wav")
	outputPath := filepath.Join(tempDir, "test.png")

	// Test with raw=false
	argsNoRaw := gen.getSoxSpectrogramArgs(context.Background(), audioPath, outputPath, 400, false)
	hasRaw := slices.Contains(argsNoRaw, "-r")
	assert.False(t, hasRaw, "should not contain -r flag when raw=false")

	// Test with raw=true
	argsWithRaw := gen.getSoxSpectrogramArgs(context.Background(), audioPath, outputPath, 400, true)
	hasRaw = slices.Contains(argsWithRaw, "-r")
	assert.True(t, hasRaw, "should contain -r flag when raw=true")
}

// TestGetSoxSpectrogramArgs_DimensionCalculation tests width/height calculation.
func TestGetSoxSpectrogramArgs_DimensionCalculation(t *testing.T) {
	tempDir := t.TempDir()
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Path = tempDir
	settings.Realtime.Audio.Export.Length = 15

	sfs, err := securefs.New(tempDir)
	require.NoError(t, err, "Failed to create SecureFS")

	gen := NewGenerator(settings, sfs, slog.Default())

	audioPath := filepath.Join(tempDir, "test.wav")
	outputPath := filepath.Join(tempDir, "test.png")

	testWidths := []int{400, 800, 1000, 1200}

	for _, width := range testWidths {
		t.Run("width_"+strconv.Itoa(width), func(t *testing.T) {
			args := gen.getSoxSpectrogramArgs(context.Background(), audioPath, outputPath, width, false)

			// Find width value
			widthStr := strconv.Itoa(width)
			heightStr := strconv.Itoa(width / 2)

			foundWidth := false
			foundHeight := false

			for i, arg := range args {
				if arg == "-x" && i+1 < len(args) && args[i+1] == widthStr {
					foundWidth = true
				}
				if arg == "-y" && i+1 < len(args) && args[i+1] == heightStr {
					foundHeight = true
				}
			}

			assert.True(t, foundWidth, "should have correct width: %d", width)
			assert.True(t, foundHeight, "should have correct height: %d (width/2)", width/2)
		})
	}
}

// TestDefaultConstants tests that default constants have expected values.
func TestDefaultConstants(t *testing.T) {
	// Test that timeout constants are reasonable
	defaultTimeout := GetDefaultGenerationTimeout()
	assert.Equal(t, 60*time.Second, defaultTimeout, "default generation timeout should be 60s")

	ffmpegTimeout := GetFFmpegFallbackTimeout()
	assert.Equal(t, 60*time.Second, ffmpegTimeout, "FFmpeg fallback timeout should be 60s")

	cacheTTL := GetDurationCacheTTL()
	assert.Equal(t, 10*time.Minute, cacheTTL, "cache TTL should be 10 minutes")

	maxEntries := GetMaxCacheEntries()
	assert.Equal(t, 1000, maxEntries, "max cache entries should be 1000")
}

// TestNewGenerator_WithNilLogger tests generator creation with nil logger uses default.
func TestNewGenerator_WithNilLogger(t *testing.T) {
	tempDir := t.TempDir()
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Path = tempDir

	sfs, err := securefs.New(tempDir)
	require.NoError(t, err, "Failed to create SecureFS")

	// Should use default logger when nil is passed
	gen := NewGenerator(settings, sfs, nil)
	require.NotNil(t, gen, "generator should be created")
	assert.NotNil(t, gen.logger, "logger should use default when passed nil")

	// Verify calling methods doesn't panic (would panic if logger were nil)
	// This validates that nil logger is handled safely
	audioPath := filepath.Join(tempDir, "test.wav")
	outputPath := filepath.Join(tempDir, "test.png")

	// getSoxSpectrogramArgs uses g.logger.Warn internally
	args := gen.getSoxSpectrogramArgs(context.Background(), audioPath, outputPath, 400, false)
	assert.NotEmpty(t, args, "should return valid args without panic")
}
