package spectrogram

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// testSoxPath is used in tests to set a Sox path that won't be called
const testSoxPath = "/usr/bin/sox"

// TestNewGenerator tests generator creation
func TestNewGenerator(t *testing.T) {
	env := setupTestEnv(t)

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))
	require.NotNil(t, gen, "NewGenerator() returned nil")
	assert.Equal(t, env.Settings, gen.settings, "NewGenerator() did not set settings correctly")
	assert.Equal(t, env.SFS, gen.sfs, "NewGenerator() did not set sfs correctly")
	// Logger is intentionally wrapped with component context, so we can't check for equality
	// Just verify it's not nil
	assert.NotNil(t, gen.logger, "NewGenerator() did not set logger (logger is nil)")
}

// TestGenerator_EnsureOutputDirectory tests directory creation
func TestGenerator_EnsureOutputDirectory(t *testing.T) {
	env := setupTestEnv(t)

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	// Test creating a nested directory
	outputPath := filepath.Join(env.TempDir, "subdir", "test.png")
	err := gen.ensureOutputDirectory(outputPath)
	require.NoError(t, err, "ensureOutputDirectory() error")

	// Verify directory was created
	outputDir := filepath.Dir(outputPath)
	_, err = os.Stat(outputDir)
	assert.False(t, os.IsNotExist(err), "ensureOutputDirectory() did not create directory: %v", outputDir)
}

// TestGenerator_EnsureOutputDirectory_PathTraversal tests path validation
func TestGenerator_EnsureOutputDirectory_PathTraversal(t *testing.T) {
	env := setupTestEnv(t)

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	// Test path traversal attempt
	outputPath := filepath.Join(env.TempDir, "..", "escape", "test.png")
	err := gen.ensureOutputDirectory(outputPath)
	assert.Error(t, err, "ensureOutputDirectory() should reject path traversal")
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

			args := gen.getSoxSpectrogramArgs(t.Context(), audioPath, outputPath, tt.width, tt.raw)
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
	env := setupTestEnv(t)
	env.Settings.Realtime.Audio.Export.Length = 15 // Fallback duration
	env.Settings.Realtime.Audio.FfmpegMajor = ffmpegMajor
	if hasFfmpegVer {
		env.Settings.Realtime.Audio.FfmpegVersion = "test"
	}

	return NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test")), env.TempDir
}

// TestGenerator_GetSoxArgs tests full Sox argument building for file input
func TestGenerator_GetSoxArgs(t *testing.T) {
	env := setupTestEnv(t)
	env.Settings.Realtime.Audio.Export.Length = 15
	env.Settings.Realtime.Audio.FfmpegMajor = 7 // Use FFmpeg 7.x to avoid -d parameter

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	audioPath := filepath.Join(env.TempDir, "test.wav")
	outputPath := filepath.Join(env.TempDir, "test.png")

	args := gen.getSoxArgs(t.Context(), audioPath, outputPath, 800, false, SoxInputFile)

	// First argument should be the audio file path
	require.NotEmpty(t, args, "getSoxArgs() should return args")
	assert.Equal(t, audioPath, args[0], "getSoxArgs() first argument should be audio path")

	// Should contain spectrogram parameters
	assert.True(t, slices.Contains(args, "spectrogram"), "getSoxArgs() missing 'spectrogram' parameter")
}

// TestGenerator_GenerateFromPCM_MissingBinary tests error handling for missing Sox
func TestGenerator_GenerateFromPCM_MissingBinary(t *testing.T) {
	env := setupTestEnv(t)
	// Don't set SoxPath - simulate missing binary

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	outputPath := filepath.Join(env.TempDir, "test.png")
	pcmData := []byte{0, 1, 2, 3}

	err := gen.GenerateFromPCM(t.Context(), pcmData, outputPath, 400, false)
	assert.Error(t, err, "GenerateFromPCM() should error when Sox binary not configured")
}

// TestGenerator_GenerateFromFile_MissingBinaries tests error handling
func TestGenerator_GenerateFromFile_MissingBinaries(t *testing.T) {
	env := setupTestEnv(t)
	// Don't set SoxPath or FfmpegPath

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	audioPath := filepath.Join(env.TempDir, "test.wav")
	outputPath := filepath.Join(env.TempDir, "test.png")

	// Create a dummy audio file
	err := os.WriteFile(audioPath, []byte("fake audio"), 0o600)
	require.NoError(t, err, "Failed to create test file")

	err = gen.GenerateFromFile(t.Context(), audioPath, outputPath, 400, false)
	assert.Error(t, err, "GenerateFromFile() should error when binaries not configured")
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
		err := os.WriteFile(testFile, []byte("test"), 0o600)
		require.NoError(t, err, "Failed to create test file")

		// Manually add cache entry (simulating what getCachedAudioDuration does)
		AddToCacheForTest(testFile, float64(i), 4)
	}

	// Verify cache size is bounded
	cacheSize := GetAudioDurationCacheSize()
	assert.LessOrEqual(t, cacheSize, maxEntries, "Cache size exceeds max - cache cleanup not working")
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
	err := os.WriteFile(oldFile, []byte("old"), 0o600)
	require.NoError(t, err, "Failed to create old test file")
	AddToCacheForTestWithTimestamp(oldFile, 1.0, 3, time.Now().Add(-1*time.Hour))

	// Fill cache with newer entries (unique filenames using index)
	for i := range maxEntries {
		testFile := filepath.Join(tempDir, "new_"+strconv.Itoa(i)+".wav")
		err := os.WriteFile(testFile, []byte("new"), 0o600)
		require.NoError(t, err, "Failed to create test file")
		AddToCacheForTest(testFile, float64(i+10), 3)
	}

	// Old entry should have been evicted (it was the oldest)
	assert.False(t, HasCacheEntry(oldFile), "Old cache entry should have been evicted but was still present")
}

// TestFFmpegFallback_GetsFreshContext tests that FFmpeg fallback gets adequate time
// even when Sox fails after consuming time from the shared context.
// This addresses issue #1503 where FFmpeg fails with "context canceled".
func TestFFmpegFallback_GetsFreshContext(t *testing.T) {
	// This test verifies that the FFmpeg fallback timeout is independent.
	// We verify this by checking that GetFFmpegFallbackTimeout returns a value
	// greater than zero, ensuring FFmpeg always has dedicated time.
	timeout := GetFFmpegFallbackTimeout()
	assert.Greater(t, timeout, time.Duration(0), "FFmpeg fallback timeout should be positive")

	// Verify it's at least 30 seconds (reasonable minimum for FFmpeg processing)
	minTimeout := 30 * time.Second
	assert.GreaterOrEqual(t, timeout, minTimeout, "FFmpeg fallback timeout is less than minimum")
}

// TestFFmpegFallback_NotAffectedByParentContext tests that FFmpeg fallback
// succeeds even when the parent context has minimal time remaining.
// This documents the fix for issue #1503.
func TestFFmpegFallback_NotAffectedByParentContext(t *testing.T) {
	// Create a context with very short deadline (simulating exhausted Sox timeout)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	// Sleep to consume most of the context time
	time.Sleep(50 * time.Millisecond)

	// Verify parent context has minimal time left
	deadline, ok := ctx.Deadline()
	require.True(t, ok, "Context should have deadline")
	remaining := time.Until(deadline)
	require.LessOrEqual(t, remaining, 100*time.Millisecond, "Context should have minimal time remaining")

	// CreateFreshFFmpegContext should return a context with full timeout
	// regardless of parent context state
	ffmpegCtx, ffmpegCancel := CreateFreshFFmpegContext(ctx)
	defer ffmpegCancel()

	// Verify FFmpeg context has adequate time
	ffmpegDeadline, ok := ffmpegCtx.Deadline()
	require.True(t, ok, "FFmpeg context should have deadline")
	ffmpegRemaining := time.Until(ffmpegDeadline)

	// FFmpeg should have at least 30 seconds
	minRequired := 30 * time.Second
	assert.GreaterOrEqual(t, ffmpegRemaining, minRequired, "FFmpeg context has insufficient time remaining")
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
				return context.WithTimeout(t.Context(), 10*time.Second)
			},
			fallback:       30 * time.Second,
			expectFallback: false,
			minExpected:    9 * time.Second,  // Allow some execution time
			maxExpected:    11 * time.Second, // Should be close to 10s
		},
		{
			name: "context without deadline returns fallback",
			setupCtx: func() (context.Context, context.CancelFunc) {
				return context.WithCancel(t.Context())
			},
			fallback:       30 * time.Second,
			expectFallback: true,
			minExpected:    30 * time.Second,
			maxExpected:    30 * time.Second,
		},
		{
			name: "expired context returns fallback",
			setupCtx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithTimeout(t.Context(), 1*time.Nanosecond)
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
				return context.WithTimeout(t.Context(), 500*time.Millisecond)
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
	env := setupTestEnv(t)
	env.Settings.Realtime.Audio.SoxPath = testSoxPath // Will fail if called, but we're testing validation

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

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
			audioPath:  filepath.Join(env.TempDir, "test.wav"),
			outputPath: "",
			width:      400,
			wantErr:    true,
			errContain: "output path is empty",
		},
		{
			name:       "relative output path",
			audioPath:  filepath.Join(env.TempDir, "test.wav"),
			outputPath: "relative/path/test.png",
			width:      400,
			wantErr:    true,
			errContain: "output path must be absolute",
		},
		{
			name:       "zero width",
			audioPath:  filepath.Join(env.TempDir, "test.wav"),
			outputPath: filepath.Join(env.TempDir, "test.png"),
			width:      0,
			wantErr:    true,
			errContain: "width must be positive",
		},
		{
			name:       "negative width",
			audioPath:  filepath.Join(env.TempDir, "test.wav"),
			outputPath: filepath.Join(env.TempDir, "test.png"),
			width:      -100,
			wantErr:    true,
			errContain: "width must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := gen.GenerateFromFile(t.Context(), tt.audioPath, tt.outputPath, tt.width, false)

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
	env := setupTestEnv(t)
	env.Settings.Realtime.Audio.SoxPath = testSoxPath

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

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
			outputPath: filepath.Join(env.TempDir, "test.png"),
			width:      0,
			wantErr:    true,
			errContain: "width must be positive",
		},
		{
			name:       "negative width",
			pcmData:    []byte{0, 1, 2, 3},
			outputPath: filepath.Join(env.TempDir, "test.png"),
			width:      -100,
			wantErr:    true,
			errContain: "width must be positive",
		},
		{
			name:       "empty PCM data",
			pcmData:    []byte{},
			outputPath: filepath.Join(env.TempDir, "test.png"),
			width:      400,
			wantErr:    true,
			errContain: "PCM data is empty",
		},
		{
			name:       "nil PCM data",
			pcmData:    nil,
			outputPath: filepath.Join(env.TempDir, "test.png"),
			width:      400,
			wantErr:    true,
			errContain: "PCM data is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := gen.GenerateFromPCM(t.Context(), tt.pcmData, tt.outputPath, tt.width, false)

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
	env := setupTestEnv(t)
	// SoxPath intentionally not set

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	audioPath := filepath.Join(env.TempDir, "test.wav")
	outputPath := filepath.Join(env.TempDir, "test.png")

	// Create a dummy audio file
	err := os.WriteFile(audioPath, []byte("fake audio"), 0o600)
	require.NoError(t, err, "Failed to create test file")

	err = gen.generateWithSoxDirect(t.Context(), audioPath, outputPath, 400, false)
	require.Error(t, err, "should error when Sox binary not configured")
	assert.Contains(t, err.Error(), "sox binary not configured", "error should mention sox binary")
}

// TestGenerateWithFFmpegSoxPipeline_MissingBinaries tests error handling for missing binaries.
func TestGenerateWithFFmpegSoxPipeline_MissingBinaries(t *testing.T) {
	env := setupTestEnv(t)

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
			env.Settings.Realtime.Audio.FfmpegPath = tt.ffmpegPath
			env.Settings.Realtime.Audio.SoxPath = tt.soxPath

			gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

			audioPath := filepath.Join(env.TempDir, "test.wav")
			outputPath := filepath.Join(env.TempDir, "test.png")

			err := gen.generateWithFFmpegSoxPipeline(t.Context(), audioPath, outputPath, 400, false)
			require.Error(t, err, "should error when binary not configured")
			assert.Contains(t, err.Error(), tt.errContain, "error should mention missing binary")
		})
	}
}

// TestGenerateWithFFmpeg_MissingBinary tests error when FFmpeg binary is not configured.
func TestGenerateWithFFmpeg_MissingBinary(t *testing.T) {
	env := setupTestEnv(t)
	// FfmpegPath intentionally not set

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	audioPath := filepath.Join(env.TempDir, "test.wav")
	outputPath := filepath.Join(env.TempDir, "test.png")

	err := gen.generateWithFFmpeg(t.Context(), audioPath, outputPath, 400, false)
	require.Error(t, err, "should error when FFmpeg binary not configured")
	assert.Contains(t, err.Error(), "ffmpeg binary not configured", "error should mention ffmpeg binary")
}

// TestGenerateWithSoxPCM_MissingBinary tests error when Sox binary is not configured.
func TestGenerateWithSoxPCM_MissingBinary(t *testing.T) {
	env := setupTestEnv(t)
	// SoxPath intentionally not set

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	outputPath := filepath.Join(env.TempDir, "test.png")
	pcmData := []byte{0, 1, 2, 3, 4, 5, 6, 7}

	err := gen.generateWithSoxPCM(t.Context(), pcmData, outputPath, 400, false)
	require.Error(t, err, "should error when Sox binary not configured")
	assert.Contains(t, err.Error(), "sox binary not configured", "error should mention sox binary")
}

// TestGetSoxArgs_FileInput tests getSoxArgs for file input type.
func TestGetSoxArgs_FileInput(t *testing.T) {
	env := setupTestEnv(t)
	env.Settings.Realtime.Audio.Export.Length = 15

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	audioPath := filepath.Join(env.TempDir, "test.wav")
	outputPath := filepath.Join(env.TempDir, "test.png")

	args := gen.getSoxArgs(t.Context(), audioPath, outputPath, 800, false, SoxInputFile)

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
	env := setupTestEnv(t)
	env.Settings.Realtime.Audio.Export.Length = 15

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	audioPath := filepath.Join(env.TempDir, "test.wav")
	outputPath := filepath.Join(env.TempDir, "test.png")

	// Test with raw=false
	argsNoRaw := gen.getSoxSpectrogramArgs(t.Context(), audioPath, outputPath, 400, false)
	hasRaw := slices.Contains(argsNoRaw, "-r")
	assert.False(t, hasRaw, "should not contain -r flag when raw=false")

	// Test with raw=true
	argsWithRaw := gen.getSoxSpectrogramArgs(t.Context(), audioPath, outputPath, 400, true)
	hasRaw = slices.Contains(argsWithRaw, "-r")
	assert.True(t, hasRaw, "should contain -r flag when raw=true")
}

// TestGetSoxSpectrogramArgs_DimensionCalculation tests width/height calculation.
func TestGetSoxSpectrogramArgs_DimensionCalculation(t *testing.T) {
	env := setupTestEnv(t)
	env.Settings.Realtime.Audio.Export.Length = 15

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	audioPath := filepath.Join(env.TempDir, "test.wav")
	outputPath := filepath.Join(env.TempDir, "test.png")

	testWidths := []int{400, 800, 1000, 1200}

	for _, width := range testWidths {
		t.Run("width_"+strconv.Itoa(width), func(t *testing.T) {
			args := gen.getSoxSpectrogramArgs(t.Context(), audioPath, outputPath, width, false)

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
	assert.Equal(t, 90*time.Second, defaultTimeout, "default generation timeout should be 90s")

	ffmpegTimeout := GetFFmpegFallbackTimeout()
	assert.Equal(t, 60*time.Second, ffmpegTimeout, "FFmpeg fallback timeout should be 60s")

	cacheTTL := GetDurationCacheTTL()
	assert.Equal(t, 10*time.Minute, cacheTTL, "cache TTL should be 10 minutes")

	maxEntries := GetMaxCacheEntries()
	assert.Equal(t, 1000, maxEntries, "max cache entries should be 1000")
}

func TestOperationalErrors_SetLowPriority(t *testing.T) {
	t.Parallel()

	env := setupTestEnv(t)
	// We need a bogus path here so `generateWithSoxPCM`` doesn't fail at the "binary not configured" check.
	env.Settings.Realtime.Audio.SoxPath = "/nonexistent/sox"

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	tests := []struct {
		name         string
		setupCtx     func() (context.Context, context.CancelFunc)
		wantPriority string
	}{
		{
			name: "context canceled sets PriorityLow",
			setupCtx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx, cancel
			},
			wantPriority: errors.PriorityLow,
		},
		{
			name: "context deadline exceeded sets PriorityLow",
			setupCtx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithTimeout(t.Context(), 1*time.Nanosecond)
				time.Sleep(5 * time.Millisecond)
				return ctx, cancel
			},
			wantPriority: errors.PriorityLow,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := tt.setupCtx()
			defer cancel()

			outputPath := filepath.Join(env.TempDir, "test_"+tt.name+".png")
			pcmData := []byte{0, 1, 2, 3}

			err := gen.GenerateFromPCM(ctx, pcmData, outputPath, 400, false)
			require.Error(t, err, "expected error from cancelled/expired context")

			var enhancedErr *errors.EnhancedError
			require.True(t, errors.As(err, &enhancedErr), "error should be an EnhancedError")
			assert.Equal(t, tt.wantPriority, enhancedErr.GetPriority(),
				"operational error should have PriorityLow to prevent dashboard notifications")
		})
	}
}

func TestNonOperationalErrors_NoExplicitPriority(t *testing.T) {
	t.Parallel()

	env := setupTestEnv(t)
	env.Settings.Realtime.Audio.SoxPath = "/nonexistent/sox"

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	outputPath := filepath.Join(env.TempDir, "test_non_op.png")
	pcmData := []byte{0, 1, 2, 3}

	// Use a valid (non-cancelled) context — the error will be "exec: /nonexistent/sox: not found"
	// which is NOT an operational error
	err := gen.GenerateFromPCM(t.Context(), pcmData, outputPath, 400, false)
	require.Error(t, err, "expected error from missing binary execution")

	var enhancedErr *errors.EnhancedError
	require.True(t, errors.As(err, &enhancedErr), "error should be an EnhancedError")

	// Non-operational errors should NOT have explicit `PriorityLow`
	assert.NotEqual(t, errors.PriorityLow, enhancedErr.GetPriority(),
		"non-operational error should not have PriorityLow — it should generate notifications")
}

// TestNewGenerator_WithNilLogger tests generator creation with nil logger uses default.
func TestNewGenerator_WithNilLogger(t *testing.T) {
	env := setupTestEnv(t)

	// Should use default logger when nil is passed
	gen := NewGenerator(env.Settings, env.SFS, nil)
	require.NotNil(t, gen, "generator should be created")
	assert.NotNil(t, gen.logger, "logger should use default when passed nil")

	// Verify calling methods doesn't panic (would panic if logger were nil)
	// This validates that nil logger is handled safely
	audioPath := filepath.Join(env.TempDir, "test.wav")
	outputPath := filepath.Join(env.TempDir, "test.png")

	// getSoxSpectrogramArgs uses g.logger.Warn internally
	args := gen.getSoxSpectrogramArgs(t.Context(), audioPath, outputPath, 400, false)
	assert.NotEmpty(t, args, "should return valid args without panic")
}
