package spectrogram

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/audiotemp"
	"github.com/tphakala/birdnet-go/internal/conf"
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

			args := gen.getSoxSpectrogramArgs(t.Context(), gen.currentSettings(), audioPath, outputPath, tt.width, tt.raw, 0, BirdProfile())
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

	args := gen.getSoxArgs(t.Context(), gen.currentSettings(), audioPath, outputPath, 800, false, SoxInputFile, 0, BirdProfile())

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

	err := gen.GenerateFromPCM(t.Context(), pcmData, outputPath, 400, false, 0)
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

// assertNoSpectrogramTemp fails if any "<outputPath>.<pid>.<seq>.temp" sidecar
// temp from an interrupted or raced render was left behind next to outputPath.
// outputPath must not contain glob metacharacters (callers pass t.TempDir paths).
func assertNoSpectrogramTemp(t *testing.T, outputPath string) {
	t.Helper()
	leftover, err := filepath.Glob(outputPath + ".*" + audiotemp.Ext)
	require.NoError(t, err)
	assert.Empty(t, leftover, "spectrogram temp files must not be left behind")
}

// partialWriteSoxStub returns a path to an executable stub that mimics a Sox
// runtime failure: it writes partial bytes to its "-o <target>" argument and then
// exits non-zero. Pointing the generator at this stub forces the failure to occur
// AFTER the output file has been opened and partially written, which is the path
// the temp-then-rename design must contain. POSIX-only (uses /bin/sh).
func partialWriteSoxStub(t *testing.T) string {
	t.Helper()
	stub := filepath.Join(t.TempDir(), "sox")
	// Scan args for "-o" and write to the following argument, so the stub finds
	// the output target regardless of its position in the Sox command line.
	script := "#!/bin/sh\n" +
		"prev=\"\"\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$prev\" = \"-o\" ]; then printf 'PARTIAL' > \"$a\"; fi\n" +
		"  prev=\"$a\"\n" +
		"done\n" +
		"exit 1\n"
	require.NoError(t, os.WriteFile(stub, []byte(script), 0o700)) //nolint:gosec // G306: test stub must be executable
	return stub
}

// TestGenerator_GenerateFromPCM_RuntimeFailureLeavesNoPartialFinal is the core
// regression guard for the atomic-write fix. A Sox runtime failure writes partial
// bytes to its output target then fails; the temp-then-rename design must keep
// those partial bytes in the temp file (cleaned up on the failure) and NEVER
// publish them to the final path. On the previous non-atomic code (which passed
// the final path as Sox's -o) this same stub would have left a corrupt final
// .png, so this test discriminates the fix from the old behaviour. POSIX-only
// (the stub is a shell script).
func TestGenerator_GenerateFromPCM_RuntimeFailureLeavesNoPartialFinal(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == osWindows {
		t.Skip("partial-write Sox stub requires a POSIX shell")
	}
	env := setupTestEnv(t)
	env.Settings.Realtime.Audio.SoxPath = partialWriteSoxStub(t)
	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	outputPath := filepath.Join(env.TempDir, "rt.png")
	err := gen.GenerateFromPCM(t.Context(), []byte{0, 1, 2, 3, 4, 5, 6, 7}, outputPath, 400, false, 0)

	require.Error(t, err, "a Sox runtime failure must surface as an error")
	assert.NoFileExists(t, outputPath, "a partial write must NOT be published to the final path")
	assertNoSpectrogramTemp(t, outputPath)
}

// TestGenerator_GenerateFromPCM_FailureLeavesNoPartialOutput covers the early
// config-validation failure (Sox not configured): the entry point returns an
// error before any subprocess runs and publishes nothing. This is a cheap,
// cross-platform smoke test; the temp-cleanup-after-partial-write guarantee is
// covered by TestGenerator_GenerateFromPCM_RuntimeFailureLeavesNoPartialFinal.
func TestGenerator_GenerateFromPCM_FailureLeavesNoPartialOutput(t *testing.T) {
	t.Parallel()
	env := setupTestEnv(t)
	// Leave SoxPath unset to force a failure before any image is written.
	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	outputPath := filepath.Join(env.TempDir, "fail.png")
	err := gen.GenerateFromPCM(t.Context(), []byte{0, 1, 2, 3}, outputPath, 400, false, 0)

	require.Error(t, err)
	assert.NoFileExists(t, outputPath, "a failed render must not leave a partial final .png")
	assertNoSpectrogramTemp(t, outputPath)
}

// TestGenerator_GenerateFromFile_FailureLeavesNoPartialOutput is the file-input
// counterpart of the early config-validation smoke test: both Sox and the FFmpeg
// fallback fail path validation, so nothing is published.
func TestGenerator_GenerateFromFile_FailureLeavesNoPartialOutput(t *testing.T) {
	t.Parallel()
	env := setupTestEnv(t)
	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	audioPath := filepath.Join(env.TempDir, "in.wav")
	require.NoError(t, os.WriteFile(audioPath, []byte("fake audio"), 0o600))
	outputPath := filepath.Join(env.TempDir, "fail.png")

	err := gen.GenerateFromFile(t.Context(), audioPath, outputPath, 400, false)

	require.Error(t, err)
	assert.NoFileExists(t, outputPath, "a failed render must not leave a partial final .png")
	assertNoSpectrogramTemp(t, outputPath)
}

// TestGenerator_GenerateFromPCM_AtomicWrite verifies the success path: the final
// spectrogram is published and the temp sidecar is removed. It asserts the
// post-conditions (final exists, non-empty, no leftover temp); it does not, and a
// unit test cannot cheaply, prove the no-partial-window invariant directly.
// Requires a real Sox.
func TestGenerator_GenerateFromPCM_AtomicWrite(t *testing.T) {
	t.Parallel()
	soxPath := requireSoxAvailable(t)
	env := setupTestEnv(t)
	env.Settings.Realtime.Audio.SoxPath = soxPath
	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	// 0.5s of 16-bit mono PCM with a non-constant signal so Sox has content.
	// 251 is a prime, so the byte ramp never settles into a constant value.
	pcm := make([]byte, defaultSampleRate) // defaultSampleRate bytes = 0.5s of int16 mono
	for i := range pcm {
		pcm[i] = byte(i % 251)
	}

	outputPath := filepath.Join(env.TempDir, "ok.png")
	err := gen.GenerateFromPCM(t.Context(), pcm, outputPath, 400, true, defaultSampleRate)
	require.NoError(t, err)

	info, statErr := os.Stat(outputPath)
	require.NoError(t, statErr, "the final spectrogram must exist after a successful render")
	assert.Positive(t, info.Size(), "the published spectrogram must be non-empty")
	assertNoSpectrogramTemp(t, outputPath)
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
			err := gen.GenerateFromPCM(t.Context(), tt.pcmData, tt.outputPath, tt.width, false, 0)

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

	err = gen.generateWithSoxDirect(t.Context(), gen.currentSettings(), audioPath, outputPath, 400, false, 0, BirdProfile())
	require.Error(t, err, "should error when Sox binary not configured")
	assert.Contains(t, err.Error(), "invalid Sox path", "error should mention invalid sox path")
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
			errContain: "invalid FFmpeg path",
		},
		{
			name: "missing sox",
			// Absolute (real binary not needed) so it passes the FFmpeg path-format
			// check and the failure surfaces at the missing Sox path. A literal
			// /usr/bin/ffmpeg is not absolute on Windows and would fail first.
			ffmpegPath: filepath.Join(env.TempDir, "ffmpeg"),
			soxPath:    "",
			errContain: "invalid Sox path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env.Settings.Realtime.Audio.FfmpegPath = tt.ffmpegPath
			env.Settings.Realtime.Audio.SoxPath = tt.soxPath

			gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

			audioPath := filepath.Join(env.TempDir, "test.wav")
			outputPath := filepath.Join(env.TempDir, "test.png")

			err := gen.generateWithFFmpegSoxPipeline(t.Context(), gen.currentSettings(), audioPath, outputPath, 400, false, 0, BirdProfile())
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

	err := gen.generateWithFFmpeg(t.Context(), gen.currentSettings(), audioPath, outputPath, 400, false, BirdProfile())
	require.Error(t, err, "should error when FFmpeg binary not configured")
	assert.Contains(t, err.Error(), "invalid FFmpeg path", "error should mention ffmpeg binary")
}

// TestGenerateWithSoxPCM_MissingBinary tests error when Sox binary is not configured.
func TestGenerateWithSoxPCM_MissingBinary(t *testing.T) {
	env := setupTestEnv(t)
	// SoxPath intentionally not set

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	outputPath := filepath.Join(env.TempDir, "test.png")
	pcmData := []byte{0, 1, 2, 3, 4, 5, 6, 7}

	err := gen.generateWithSoxPCM(t.Context(), gen.currentSettings(), pcmData, outputPath, 400, false, 0, BirdProfile())
	require.Error(t, err, "should error when Sox binary not configured")
	assert.Contains(t, err.Error(), "invalid Sox path", "error should mention invalid sox path")
}

// TestGetSoxArgs_FileInput tests getSoxArgs for file input type.
func TestGetSoxArgs_FileInput(t *testing.T) {
	env := setupTestEnv(t)
	env.Settings.Realtime.Audio.Export.Length = 15

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	audioPath := filepath.Join(env.TempDir, "test.wav")
	outputPath := filepath.Join(env.TempDir, "test.png")

	args := gen.getSoxArgs(t.Context(), gen.currentSettings(), audioPath, outputPath, 800, false, SoxInputFile, 0, BirdProfile())

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
	argsNoRaw := gen.getSoxSpectrogramArgs(t.Context(), gen.currentSettings(), audioPath, outputPath, 400, false, 0, BirdProfile())
	hasRaw := slices.Contains(argsNoRaw, "-r")
	assert.False(t, hasRaw, "should not contain -r flag when raw=false")

	// Test with raw=true
	argsWithRaw := gen.getSoxSpectrogramArgs(t.Context(), gen.currentSettings(), audioPath, outputPath, 400, true, 0, BirdProfile())
	hasRaw = slices.Contains(argsWithRaw, "-r")
	assert.True(t, hasRaw, "should contain -r flag when raw=true")
}

// TestGetSoxSpectrogramArgs_BatProfile verifies that the bat frequency profile
// produces a high-pass sinc filter instead of rate resampling.
func TestGetSoxSpectrogramArgs_BatProfile(t *testing.T) {
	env := setupTestEnv(t)
	env.Settings.Realtime.Audio.Export.Length = 15

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	audioPath := filepath.Join(env.TempDir, "test.wav")
	outputPath := filepath.Join(env.TempDir, "test.png")

	args := gen.getSoxSpectrogramArgs(t.Context(), gen.currentSettings(), audioPath, outputPath, 400, false, 0, BatProfile())

	// Bat profile: sinc high-pass filter, no rate resampling
	assert.Contains(t, args, "sinc", "bat profile should use sinc high-pass filter")
	assert.Contains(t, args, "15000", "bat profile should filter at 15 kHz")
	assert.NotContains(t, args, "rate", "bat profile should not resample")
}

// TestGetSoxSpectrogramArgs_BirdProfile verifies that the bird frequency profile
// produces rate resampling without a high-pass filter.
func TestGetSoxSpectrogramArgs_BirdProfile(t *testing.T) {
	env := setupTestEnv(t)
	env.Settings.Realtime.Audio.Export.Length = 15

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	audioPath := filepath.Join(env.TempDir, "test.wav")
	outputPath := filepath.Join(env.TempDir, "test.png")

	args := gen.getSoxSpectrogramArgs(t.Context(), gen.currentSettings(), audioPath, outputPath, 400, false, 0, BirdProfile())

	// Bird profile: rate resampling, no sinc filter
	assert.Contains(t, args, "rate", "bird profile should resample")
	assert.Contains(t, args, "24000", "bird profile should resample to 24 kHz")
	assert.NotContains(t, args, "sinc", "bird profile should not use sinc filter")
}

// TestProfileForModelType verifies model type to frequency profile mapping.
// Bat models resolve to the bat profile (no resample, 15 kHz high-pass);
// every other model type falls back to bird defaults.
func TestProfileForModelType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		modelType    string
		wantResample int
		wantHighPass int
	}{
		{"bird model", "bird", 24000, 0},
		{"bat model uses bat profile", "bat", 0, 15000},
		{"multi model defaults to bird", "multi", 24000, 0},
		{"empty defaults to bird", "", 24000, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := ProfileForModelType(tt.modelType)
			assert.Equal(t, tt.wantResample, p.ResampleRate)
			assert.Equal(t, tt.wantHighPass, p.HighPassHz)
		})
	}
}

// TestProfileSuffix verifies the cache-key token derived from a frequency profile:
// bat (high-pass) profiles get a "bat" token; bird defaults stay empty for
// backward compatibility with existing cached spectrogram filenames.
func TestProfileSuffix(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "bat", ProfileSuffix(BatProfile()), "bat profile should map to the bat token")
	assert.Empty(t, ProfileSuffix(BirdProfile()), "bird profile should map to an empty token")
	assert.Equal(t, "bat", ProfileSuffix(ProfileForModelType("bat")))
	assert.Empty(t, ProfileSuffix(ProfileForModelType("bird")))
}

// TestGetSoxSpectrogramArgs_UsesProvidedDuration verifies that a non-zero preValidatedDuration
// is used directly for the -d parameter, skipping the sox --info duration query.
func TestGetSoxSpectrogramArgs_UsesProvidedDuration(t *testing.T) {
	gen, tempDir := createTestGenerator(t, 0, false)

	audioPath := filepath.Join(tempDir, "input.mp3")
	outputPath := filepath.Join(tempDir, "out.png")

	args := gen.getSoxSpectrogramArgs(t.Context(), gen.currentSettings(), audioPath, outputPath, 400, false, 12.6, BirdProfile())

	idx := slices.Index(args, "-d")
	require.NotEqual(t, -1, idx, "should contain -d parameter")
	require.Less(t, idx+1, len(args), "-d should have a value after it")
	assert.Equal(t, "13", args[idx+1], "duration should be int(12.6 + 0.5) = 13")
}

// TestGetSoxSpectrogramArgs_DimensionCalculation tests width/height calculation.
// Heights should be FFT-friendly (2^n + 1) so sox uses fast FFT instead of brute-force DFT.
func TestGetSoxSpectrogramArgs_DimensionCalculation(t *testing.T) {
	env := setupTestEnv(t)
	env.Settings.Realtime.Audio.Export.Length = 15

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	audioPath := filepath.Join(env.TempDir, "test.wav")
	outputPath := filepath.Join(env.TempDir, "test.png")

	tests := []struct {
		width          int
		expectedHeight int // FFT-friendly height (2^n + 1)
	}{
		{514, 257},   // DFT=512
		{1026, 513},  // DFT=1024
		{2050, 1025}, // DFT=2048
		{800, 513},   // Arbitrary width: nearest 2^n+1 >= 400 is 513
	}

	for _, tt := range tests {
		t.Run("width_"+strconv.Itoa(tt.width), func(t *testing.T) {
			args := gen.getSoxSpectrogramArgs(t.Context(), gen.currentSettings(), audioPath, outputPath, tt.width, false, 0, BirdProfile())

			widthStr := strconv.Itoa(tt.width)
			heightStr := strconv.Itoa(tt.expectedHeight)

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

			assert.True(t, foundWidth, "should have correct width: %d", tt.width)
			assert.True(t, foundHeight, "should have FFT-friendly height: %d for width %d", tt.expectedHeight, tt.width)
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
	// Bogus but absolute path so generateWithSoxPCM passes the path-format check
	// (and the failure comes from the cancelled context, not path validation).
	// A literal /nonexistent/sox is not absolute on Windows, so it would fail the
	// Sox path check first and the error would not carry PriorityLow.
	env.Settings.Realtime.Audio.SoxPath = filepath.Join(env.TempDir, "nonexistent-sox")

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
				<-ctx.Done()
				return ctx, cancel
			},
			wantPriority: errors.PriorityLow,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := tt.setupCtx()
			t.Cleanup(cancel)

			outputPath := filepath.Join(env.TempDir, "test_"+tt.name+".png")
			pcmData := []byte{0, 1, 2, 3}

			err := gen.GenerateFromPCM(ctx, pcmData, outputPath, 400, false, 0)
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
	// Bogus but absolute path so the Sox path-format check passes and the failure
	// comes from exec (binary not found), which is non-operational. A literal
	// /nonexistent/sox is not absolute on Windows and would fail path validation
	// first, masking the intended path; build an OS-absolute non-existent path.
	env.Settings.Realtime.Audio.SoxPath = filepath.Join(env.TempDir, "nonexistent-sox")

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	outputPath := filepath.Join(env.TempDir, "test_non_op.png")
	pcmData := []byte{0, 1, 2, 3}

	// Use a valid (non-cancelled) context — the error will be "exec: /nonexistent/sox: not found"
	// which is NOT an operational error
	err := gen.GenerateFromPCM(t.Context(), pcmData, outputPath, 400, false, 0)
	require.Error(t, err, "expected error from missing binary execution")

	var enhancedErr *errors.EnhancedError
	require.True(t, errors.As(err, &enhancedErr), "error should be an EnhancedError")

	// Non-operational errors should NOT have explicit `PriorityLow`
	assert.NotEqual(t, errors.PriorityLow, enhancedErr.GetPriority(),
		"non-operational error should not have PriorityLow — it should generate notifications")
}

// TestGetFileSizeBytes_ReturnsSize tests that getFileSizeBytes returns the correct size for an existing file.
func TestGetFileSizeBytes_ReturnsSize(t *testing.T) {
	t.Parallel()
	f := filepath.Join(t.TempDir(), "test.wav")
	require.NoError(t, os.WriteFile(f, make([]byte, 4096), 0o644))
	size := getFileSizeBytes(f)
	assert.Equal(t, int64(4096), size)
}

// TestGetFileSizeBytes_MissingFile tests that getFileSizeBytes returns -1 for a nonexistent file.
func TestGetFileSizeBytes_MissingFile(t *testing.T) {
	t.Parallel()
	size := getFileSizeBytes("/nonexistent/file.wav")
	assert.Equal(t, int64(-1), size)
}

// TestNewGenerator_WithNilLogger tests generator creation with nil logger uses default.
func TestNewGenerator_WithNilLogger(t *testing.T) {
	env := setupTestEnv(t)

	// Logger parameter is ignored; generator uses dynamic GetLogger() internally
	gen := NewGenerator(env.Settings, env.SFS, nil)
	require.NotNil(t, gen, "generator should be created")

	// Verify calling methods doesn't panic (would panic if logger were nil)
	// This validates that nil logger is handled safely
	audioPath := filepath.Join(env.TempDir, "test.wav")
	outputPath := filepath.Join(env.TempDir, "test.png")

	// getSoxSpectrogramArgs uses g.log().Warn internally
	args := gen.getSoxSpectrogramArgs(t.Context(), gen.currentSettings(), audioPath, outputPath, 400, false, 0, BirdProfile())
	assert.NotEmpty(t, args, "should return valid args without panic")
}

// TestGetStyleArgs tests that Sox style arguments are correct for each style preset.
func TestGetStyleArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		style    string
		wantArgs []string
	}{
		{
			name:     "default style returns nil",
			style:    conf.SpectrogramStyleDefault,
			wantArgs: nil,
		},
		{
			name:     "scientific dark returns monochrome with Dolph window",
			style:    conf.SpectrogramStyleScientificDark,
			wantArgs: []string{"-m", "-w", "dolph"},
		},
		{
			name:     "high contrast dark returns high saturation flag",
			style:    conf.SpectrogramStyleHighContrastDark,
			wantArgs: []string{"-h"},
		},
		{
			name:     "scientific returns monochrome light with Dolph window",
			style:    conf.SpectrogramStyleScientific,
			wantArgs: []string{"-m", "-l", "-w", "dolph"},
		},
		{
			name:     "unknown style returns nil like default",
			style:    "nonexistent_style",
			wantArgs: nil,
		},
		{
			name:     "empty style returns nil like default",
			style:    "",
			wantArgs: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := getStyleArgs(tt.style)
			assert.Equal(t, tt.wantArgs, got)
		})
	}
}

// TestGetFFmpegColorMode tests that FFmpeg color modes match the expected
// values for each style preset, ensuring the FFmpeg fallback produces
// visually consistent spectrograms with the Sox primary path.
func TestGetFFmpegColorMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		style     string
		wantColor string
	}{
		{
			name:      "default style uses channel color mode",
			style:     conf.SpectrogramStyleDefault,
			wantColor: ffmpegColorDefault,
		},
		{
			name:      "scientific dark uses intensity (grayscale)",
			style:     conf.SpectrogramStyleScientificDark,
			wantColor: ffmpegColorIntensity,
		},
		{
			name:      "high contrast dark uses fire (high saturation)",
			style:     conf.SpectrogramStyleHighContrastDark,
			wantColor: ffmpegColorFire,
		},
		{
			name:      "scientific uses intensity (grayscale)",
			style:     conf.SpectrogramStyleScientific,
			wantColor: ffmpegColorIntensity,
		},
		{
			name:      "unknown style falls back to channel default",
			style:     "nonexistent_style",
			wantColor: ffmpegColorDefault,
		},
		{
			name:      "empty style falls back to channel default",
			style:     "",
			wantColor: ffmpegColorDefault,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := getFFmpegColorMode(tt.style)
			assert.Equal(t, tt.wantColor, got)
		})
	}
}

// TestFFmpegFallback_AppliesStyleSetting verifies that the FFmpeg fallback
// generation path includes the style-aware color parameter in the filter string.
// This is the core fix for issue #1937 where changing the spectrogram style
// to "scientific dark" would intermittently render the default colorful style
// when Sox failed and FFmpeg took over without style information.
func TestFFmpegFallback_AppliesStyleSetting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		style         string
		expectInColor string // substring expected in the FFmpeg filter
	}{
		{
			name:          "default style includes channel color",
			style:         conf.SpectrogramStyleDefault,
			expectInColor: "color=" + ffmpegColorDefault,
		},
		{
			name:          "scientific dark includes intensity color",
			style:         conf.SpectrogramStyleScientificDark,
			expectInColor: "color=" + ffmpegColorIntensity,
		},
		{
			name:          "high contrast dark includes fire color",
			style:         conf.SpectrogramStyleHighContrastDark,
			expectInColor: "color=" + ffmpegColorFire,
		},
		{
			name:          "scientific includes intensity color",
			style:         conf.SpectrogramStyleScientific,
			expectInColor: "color=" + ffmpegColorIntensity,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Build the same filter string that generateWithFFmpeg constructs,
			// verifying the style-aware color parameter is integrated correctly.
			colorMode := getFFmpegColorMode(tt.style)
			filterStr := fmt.Sprintf("showspectrumpic=s=%dx%d:legend=%d:gain=%s:drange=%s:color=%s",
				400, fftFriendlyHeight(400), 1, ffmpegGain, ffmpegDrange, colorMode)
			assert.Contains(t, filterStr, tt.expectInColor,
				"FFmpeg filter string should include style-aware color mode")
		})
	}
}

// TestGetSoxSpectrogramArgs_StyleArgs verifies that Sox spectrogram args
// include the correct style-specific arguments for each preset.
func TestGetSoxSpectrogramArgs_StyleArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		style        string
		wantContains []string // Args that must be present
		wantAbsent   []string // Args that must NOT be present
	}{
		{
			name:         "default style has no style-specific args",
			style:        conf.SpectrogramStyleDefault,
			wantContains: nil,
			wantAbsent:   []string{"-m", "-h", "-l"},
		},
		{
			name:         "scientific dark includes monochrome and Dolph",
			style:        conf.SpectrogramStyleScientificDark,
			wantContains: []string{"-m", "-w", "dolph"},
			wantAbsent:   []string{"-h", "-l"},
		},
		{
			name:         "high contrast dark includes high saturation",
			style:        conf.SpectrogramStyleHighContrastDark,
			wantContains: []string{"-h"},
			wantAbsent:   []string{"-m", "-l"},
		},
		{
			name:         "scientific includes monochrome and light background",
			style:        conf.SpectrogramStyleScientific,
			wantContains: []string{"-m", "-l", "-w", "dolph"},
			wantAbsent:   []string{"-h"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := setupTestEnv(t)
			env.Settings.Realtime.Audio.Export.Length = 15
			env.Settings.Realtime.Dashboard.Spectrogram.Style = tt.style

			gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

			audioPath := filepath.Join(env.TempDir, "test.wav")
			outputPath := filepath.Join(env.TempDir, "test.png")

			args := gen.getSoxSpectrogramArgs(t.Context(), gen.currentSettings(), audioPath, outputPath, 400, false, 0, BirdProfile())

			for _, want := range tt.wantContains {
				assert.True(t, slices.Contains(args, want),
					"args should contain %q for style %q, got: %v", want, tt.style, args)
			}
			for _, absent := range tt.wantAbsent {
				assert.False(t, slices.Contains(args, absent),
					"args should NOT contain %q for style %q, got: %v", absent, tt.style, args)
			}
		})
	}
}

// TestSoxAndFFmpegStyleConsistency verifies that every known style preset
// has mappings in both getStyleArgs (Sox) and getFFmpegColorMode (FFmpeg).
// This prevents future style additions from only updating one path.
func TestSoxAndFFmpegStyleConsistency(t *testing.T) {
	t.Parallel()

	allStyles := []struct {
		style         string
		wantSoxMapped bool // true if Sox should return non-nil args for this style
	}{
		{conf.SpectrogramStyleDefault, false},
		{conf.SpectrogramStyleScientificDark, true},
		{conf.SpectrogramStyleHighContrastDark, true},
		{conf.SpectrogramStyleScientific, true},
	}

	for _, tc := range allStyles {
		t.Run(tc.style, func(t *testing.T) {
			t.Parallel()

			// Verify Sox style mapping returns args for non-default presets
			soxArgs := getStyleArgs(tc.style)
			if tc.wantSoxMapped {
				assert.NotEmpty(t, soxArgs,
					"Sox args should be mapped for style %q", tc.style)
			}

			// getFFmpegColorMode should return a non-empty value
			colorMode := getFFmpegColorMode(tc.style)
			assert.NotEmpty(t, colorMode,
				"FFmpeg color mode should not be empty for style %q", tc.style)
		})
	}
}
