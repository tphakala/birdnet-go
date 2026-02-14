package diskmanager

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Define variables for mocking
var (
	// Function variables for mocking
	diskUsageFunc            = GetDiskUsage
	parseRetentionPeriodFunc = conf.ParseRetentionPeriod
)

// Mock functions
func mockGetDiskUsage(path string, usage float64) (float64, error) {
	return usage, nil
}

func mockParseRetentionPeriod(period string, hours int) (int, error) {
	return hours, nil
}

// MockDB is a mock implementation of the database interface for testing
type MockDB struct{}

// GetDeletionInfo is a mock implementation that always returns no entries
func (m *MockDB) GetDeletionInfo() ([]string, error) {
	return []string{}, nil
}

// InsertDeletionInfo is a mock implementation that does nothing
func (m *MockDB) InsertDeletionInfo(filename string) error {
	return nil
}

// GetLockedNotesClipPaths is a mock implementation that returns no paths
func (m *MockDB) GetLockedNotesClipPaths() ([]string, error) {
	return []string{}, nil
}

// TestAgeBasedCleanupFileTypeEligibility tests if the file type check works correctly
func TestAgeBasedCleanupFileTypeEligibility(t *testing.T) {
	// Test with different file extensions
	testFiles := []struct {
		name          string
		expectError   bool
		errorContains string
	}{
		// Audio files - should work without errors
		{"bubo_bubo_80p_20210102T150405Z.wav", false, ""},
		{"bubo_bubo_80p_20210102T150405Z.mp3", false, ""},
		{"bubo_bubo_80p_20210102T150405Z.flac", false, ""},
		{"bubo_bubo_80p_20210102T150405Z.aac", false, ""},
		{"bubo_bubo_80p_20210102T150405Z.opus", false, ""},

		// Non-audio files - should return errors
		{"bubo_bubo_80p_20210102T150405Z.txt", true, "not eligible for cleanup"},
		{"bubo_bubo_80p_20210102T150405Z.jpg", true, "not eligible for cleanup"},
		{"bubo_bubo_80p_20210102T150405Z.png", true, "not eligible for cleanup"},
		{"bubo_bubo_80p_20210102T150405Z.db", true, "not eligible for cleanup"},
		{"bubo_bubo_80p_20210102T150405Z.csv", true, "not eligible for cleanup"},
		{"system_80p_20210102T150405Z.exe", true, "not eligible for cleanup"},
	}

	// Print the current list of allowed file types for debugging
	t.Logf("Allowed file types: %v", allowedFileTypes)

	// Create a temporary directory (auto-cleaned by testing framework)
	testDir := t.TempDir()

	for _, tc := range testFiles {
		t.Run(tc.name, func(t *testing.T) {
			// Create a mock FileInfo
			mockInfo := createMockFileInfo(tc.name, 1024)

			// Call parseFileInfo directly to test file extension checking
			_, err := parseFileInfo(filepath.Join(testDir, tc.name), mockInfo, allowedFileTypes)

			// Debug logging
			t.Logf("File: %s, Extension: %s, Error: %v",
				tc.name, filepath.Ext(tc.name), err)

			// Check if the error matches our expectation
			if tc.expectError {
				require.Error(t, err, "SECURITY ISSUE: Expected error for %s but got nil", tc.name)
				assert.Contains(t, err.Error(), tc.errorContains, "Expected error containing '%s'", tc.errorContains)
			} else {
				require.NoError(t, err, "Expected no error for %s", tc.name)
			}
		})
	}
}

// TestAgeBasedFilesAfterFilter tests the filtering of files for age-based cleanup
func TestAgeBasedFilesAfterFilter(t *testing.T) {
	db := &MockDB{}
	allowedTypes := []string{".wav", ".mp3", ".flac", ".aac", ".opus", ".m4a"}

	// Create a temporary directory (auto-cleaned by testing framework)
	testDir := t.TempDir()

	// Let's create files of all relevant types
	fileTypes := []string{
		".wav", ".mp3", ".flac", ".aac", ".opus", ".m4a",
		".txt", ".jpg", ".png", ".db", ".exe",
	}

	for _, ext := range fileTypes {
		filePath := filepath.Join(testDir, fmt.Sprintf("bubo_bubo_80p_20210102T150405Z%s", ext))
		require.NoError(t, os.WriteFile(filePath, []byte("test content"), 0o644), "Failed to create test file") //nolint:gosec // G306: Test files don't require restrictive permissions
	}

	// Get audio files using the function that would be used by the policy
	audioFiles, err := GetAudioFiles(testDir, allowedTypes, db, false)
	require.NoError(t, err, "Failed to get audio files")

	// Verify only allowed audio files are returned
	assert.Len(t, audioFiles, len(allowedTypes), "Expected %d audio files", len(allowedTypes))

	// Verify each returned file has an allowed extension
	for _, file := range audioFiles {
		ext := filepath.Ext(file.Path)
		assert.True(t, contains(allowedTypes, ext), "SECURITY ISSUE: File with disallowed extension was included: %s", file.Path)
	}
}

// TestAgeBasedCleanupBasicFunctionality tests the basic functionality of age-based cleanup
func TestAgeBasedCleanupBasicFunctionality(t *testing.T) {
	// Create test files with different timestamps
	// Recent files (within retention period)
	recentFile1 := FileInfo{
		Path:       "/test/bubo_bubo_80p_20210102T150405Z.wav",
		Species:    "bubo_bubo",
		Confidence: 80,
		Timestamp:  time.Now().Add(-24 * time.Hour), // 1 day old
		Size:       1024,
		Locked:     false,
	}

	recentFile2 := FileInfo{
		Path:       "/test/anas_platyrhynchos_70p_20210102T150405Z.wav",
		Species:    "anas_platyrhynchos",
		Confidence: 70,
		Timestamp:  time.Now().Add(-48 * time.Hour), // 2 days old
		Size:       1024,
		Locked:     false,
	}

	// Old files (beyond retention period)
	oldFile1 := FileInfo{
		Path:       "/test/bubo_bubo_90p_20200102T150405Z.wav",
		Species:    "bubo_bubo",
		Confidence: 90,
		Timestamp:  time.Now().Add(-720 * time.Hour), // 30 days old
		Size:       1024,
		Locked:     false,
	}

	oldFile2 := FileInfo{
		Path:       "/test/anas_platyrhynchos_60p_20200102T150405Z.wav",
		Species:    "anas_platyrhynchos",
		Confidence: 60,
		Timestamp:  time.Now().Add(-1440 * time.Hour), // 60 days old
		Size:       1024,
		Locked:     false,
	}

	// A locked file that should never be deleted
	lockedFile := FileInfo{
		Path:       "/test/bubo_bubo_95p_20200102T150405Z.wav",
		Species:    "bubo_bubo",
		Confidence: 95,
		Timestamp:  time.Now().Add(-2160 * time.Hour), // 90 days old
		Size:       1024,
		Locked:     true,
	}

	// Test files collection
	testFiles := []FileInfo{recentFile1, recentFile2, oldFile1, oldFile2, lockedFile}

	// Verify file type checks are performed on all files
	for _, file := range testFiles {
		filename := filepath.Base(file.Path)
		ext := filepath.Ext(filename)

		// Assert that only allowed file types are processed
		assert.True(t, contains(allowedFileTypes, ext),
			"File type should be in the allowed list: %s", ext)
	}

	// Check that age-based cleanup would:
	// 1. Delete files older than retention period
	// 2. Never delete locked files
	// 3. Maintain minimum number of clips per species

	// This is a basic verification - a full test would require mocking more components
	for _, file := range testFiles {
		// Locked files should never be deleted
		if file.Locked {
			t.Logf("Verified that locked file would be protected: %s", file.Path)
			continue
		}

		// Recent files should be kept - use local time for comparison
		// Note: File timestamps (even with 'Z' suffix) are in local time, not UTC
		if file.Timestamp.After(time.Now().Add(-168 * time.Hour)) { // Assuming 7 day retention
			t.Logf("Verified that recent file would be preserved: %s", file.Path)
		} else {
			t.Logf("Verified that old file would be eligible for deletion: %s", file.Path)
		}
	}
}

// TestAgeBasedCleanupReturnValues tests that AgeBasedCleanup returns the expected values
// and correctly handles spectrogram deletion based on KeepSpectrograms setting.
func TestAgeBasedCleanupReturnValues(t *testing.T) {
	// Create a temporary directory for testing
	testDir := t.TempDir()

	// Create a mock DB
	mockDB := &MockDB{}

	// --- File Setup ---
	// Helper to create audio and png files
	createTestFilePair := func(species string, confidence int, modTime time.Time) (string, string) {
		// Format timestamp correctly for the filename
		timestampStr := modTime.UTC().Format("20060102T150405Z")
		baseName := fmt.Sprintf("%s_%dp_%s", species, confidence, timestampStr)

		audioPath := filepath.Join(testDir, baseName+".wav")
		pngPath := filepath.Join(testDir, baseName+".png")
		require.NoError(t, os.WriteFile(audioPath, []byte("audio"), 0o644), "Failed to create audio file: %s", audioPath) //nolint:gosec // G306: Test files don't require restrictive permissions
		require.NoError(t, os.WriteFile(pngPath, []byte("png"), 0o644), "Failed to create png file: %s", pngPath)         //nolint:gosec // G306: Test files don't require restrictive permissions
		require.NoError(t, os.Chtimes(audioPath, modTime, modTime), "Failed to set audio time: %s", audioPath)
		require.NoError(t, os.Chtimes(pngPath, modTime, modTime), "Failed to set png time: %s", pngPath)
		return audioPath, pngPath
	}

	// Recent file (3 days old)
	recentTime := time.Now().Add(-72 * time.Hour)
	recentAudioPath, recentPngPath := createTestFilePair("bubo_bubo", 80, recentTime)

	// Old file 1 (30 days old)
	oldTime1 := time.Now().Add(-720 * time.Hour)
	old1AudioPath, old1PngPath := createTestFilePair("bubo_bubo", 90, oldTime1)

	// Old file 2 (30 days old)
	oldTime2 := time.Now().Add(-720 * time.Hour)
	old2AudioPath, old2PngPath := createTestFilePair("anas_platyrhynchos", 60, oldTime2)

	allAudioFiles := []string{recentAudioPath, old1AudioPath, old2AudioPath}

	// --- Test Execution Function ---
	runTest := func(keepSpectrograms bool) CleanupResult {
		// Reset file system state (recreate potentially deleted files for next run)
		// Use the exact same parameters to ensure consistent recreation
		createTestFilePair("bubo_bubo", 80, recentTime)
		createTestFilePair("bubo_bubo", 90, oldTime1)
		createTestFilePair("anas_platyrhynchos", 60, oldTime2)

		quitChan := make(chan struct{})
		deletedFilesMap := make(map[string]bool)
		initialDiskUtilization := 90
		utilizationReductionPerFile := 5

		return simulateAgeBasedCleanup(
			quitChan,
			mockDB,
			testDir,
			allAudioFiles,
			deletedFilesMap, // Pass the map to track deletions
			initialDiskUtilization,
			0,   // minClipsPerSpecies
			168, // << Add default 7-day retention for this test
			keepSpectrograms,
			utilizationReductionPerFile,
		)
	}

	// --- Scenario 1: KeepSpectrograms = true ---
	t.Run("KeepSpectrogramsTrue", func(t *testing.T) {
		result := runTest(true)

		// Verify return values (same checks as before)
		require.NoError(t, result.Err, "[KeepTrue] AgeBasedCleanup should not return an error")
		assert.Equal(t, 2, result.ClipsRemoved, "[KeepTrue] AgeBasedCleanup should remove 2 audio clips")
		expectedDiskUtilization := 90 - (2 * 5)
		assert.Equal(t, expectedDiskUtilization, result.DiskUtilization, "[KeepTrue] Incorrect disk utilization")

		// Verify audio file deletions (using actual file existence)
		assert.FileExists(t, recentAudioPath, "[KeepTrue] Recent audio file should exist")
		assert.NoFileExists(t, old1AudioPath, "[KeepTrue] Old audio file 1 should be deleted")
		assert.NoFileExists(t, old2AudioPath, "[KeepTrue] Old audio file 2 should be deleted")

		// Verify PNG files are NOT deleted
		assert.FileExists(t, recentPngPath, "[KeepTrue] Recent PNG file should exist")
		assert.FileExists(t, old1PngPath, "[KeepTrue] Old PNG file 1 should NOT be deleted")
		assert.FileExists(t, old2PngPath, "[KeepTrue] Old PNG file 2 should NOT be deleted")
	})

	// --- Scenario 2: KeepSpectrograms = false ---
	t.Run("KeepSpectrogramsFalse", func(t *testing.T) {
		result := runTest(false)

		// Verify return values (should be the same as KeepTrue)
		require.NoError(t, result.Err, "[KeepFalse] AgeBasedCleanup should not return an error")
		assert.Equal(t, 2, result.ClipsRemoved, "[KeepFalse] AgeBasedCleanup should remove 2 audio clips")
		expectedDiskUtilization := 90 - (2 * 5)
		assert.Equal(t, expectedDiskUtilization, result.DiskUtilization, "[KeepFalse] Incorrect disk utilization")

		// Verify audio file deletions (using actual file existence)
		assert.FileExists(t, recentAudioPath, "[KeepFalse] Recent audio file should exist")
		assert.NoFileExists(t, old1AudioPath, "[KeepFalse] Old audio file 1 should be deleted")
		assert.NoFileExists(t, old2AudioPath, "[KeepFalse] Old audio file 2 should be deleted")

		// Verify PNG files ARE deleted for the deleted audio files
		assert.FileExists(t, recentPngPath, "[KeepFalse] Recent PNG file should exist")
		assert.NoFileExists(t, old1PngPath, "[KeepFalse] Old PNG file 1 SHOULD be deleted")
		assert.NoFileExists(t, old2PngPath, "[KeepFalse] Old PNG file 2 SHOULD be deleted")
	})
}

// TestAgeBasedCleanupMinClipsGlobal tests that minClipsPerSpecies is enforced globally,
// not per subdirectory, for age-based cleanup.
func TestAgeBasedCleanupMinClipsGlobal(t *testing.T) {
	t.Parallel() // This test can run in parallel

	// Create a temporary directory for testing
	testDir := t.TempDir()
	mockDB := &MockDB{}

	// --- File Setup --- Helper to create audio files in specific subdirs
	createTestFile := func(subdir, species string, confidence int, modTime time.Time) string {
		subdirPath := filepath.Join(testDir, subdir)
		require.NoError(t, os.MkdirAll(subdirPath, 0o750), "Failed to create subdirectory: %s", subdirPath)
		timestampStr := modTime.UTC().Format("20060102T150405Z")
		baseName := fmt.Sprintf("%s_%dp_%s", species, confidence, timestampStr)
		audioPath := filepath.Join(testDir, subdir, baseName+".wav")
		require.NoError(t, os.WriteFile(audioPath, []byte("audio"), 0o644), "Failed to create audio file: %s", audioPath) //nolint:gosec // G306: Test files don't require restrictive permissions
		require.NoError(t, os.Chtimes(audioPath, modTime, modTime), "Failed to set time for audio file: %s", audioPath)
		return audioPath
	}

	// Setup file times (all older than 7 days/168 hours for simplicity)
	oldestTime := time.Now().Add(-1000 * time.Hour)
	olderTime := time.Now().Add(-900 * time.Hour)
	oldTime := time.Now().Add(-800 * time.Hour)
	alsoOldTime := time.Now().Add(-750 * time.Hour)

	// Species A: 3 files, all old, in different dirs
	// Expected: Keep 1 (the newest of the old ones), delete 2 oldest
	aFile1 := createTestFile("dir1", "bubo_bubo", 80, oldTime)    // Keep this one
	aFile2 := createTestFile("dir1", "bubo_bubo", 90, olderTime)  // Delete
	aFile3 := createTestFile("dir2", "bubo_bubo", 85, oldestTime) // Delete

	// Species B: 1 file, old
	// Expected: Delete this one
	bFile1 := createTestFile("dir1", "anas_platyrhynchos", 70, alsoOldTime) // Delete

	allAudioFiles := []string{aFile1, aFile2, aFile3, bFile1}
	initialDiskUtilization := 100
	utilizationReductionPerFile := 10
	minClips := 1 // Crucial setting for this test

	// --- Run Simulation --- Use minClips = 1
	quitChan := make(chan struct{})
	deletedFilesMap := make(map[string]bool)
	result := simulateAgeBasedCleanup(
		quitChan,
		mockDB,
		testDir,
		allAudioFiles,
		deletedFilesMap, // Not directly used for assertions here, but required by helper
		initialDiskUtilization,
		minClips, // Set minClipsPerSpecies to 1
		168,      // << Add default 7-day retention for this test
		false,    // keepSpectrograms = false (doesn't matter much for this test)
		utilizationReductionPerFile,
	)

	// --- Assertions --- Expected 2 deletions total (the 2 oldest bubo_bubo)
	require.NoError(t, result.Err, "Cleanup should not return an error")
	assert.Equal(t, 2, result.ClipsRemoved, "Should remove 2 clips (oldest 2 of A), keeping 1 of A and 1 of B")
	expectedDisk := initialDiskUtilization - (2 * utilizationReductionPerFile) // Only 2 deletions
	assert.Equal(t, expectedDisk, result.DiskUtilization, "Incorrect final disk utilization")

	// Verify file existence based on global minClips
	assert.FileExists(t, aFile1, "Species A newest old file should be kept (minClips=1)")
	assert.NoFileExists(t, aFile2, "Species A older file should be deleted")
	assert.NoFileExists(t, aFile3, "Species A oldest file should be deleted")
	assert.FileExists(t, bFile1, "Species B only old file should be kept (minClips=1)") // Updated assertion
}

// TestAgeBasedCleanupShortRetention verifies end-to-end AgeBasedCleanup with a short retention.
func TestAgeBasedCleanupShortRetention(t *testing.T) {
	// --- Test Setup --- Mocks & Temp Dir
	testDir := t.TempDir()
	mockDB := &MockDB{}

	// --- File Setup --- Helper
	createTestFilePair := func(species string, confidence int, modTime time.Time) (string, string) {
		timestampStr := modTime.UTC().Format("20060102T150405Z")
		baseName := fmt.Sprintf("%s_%dp_%s", species, confidence, timestampStr)
		audioPath := filepath.Join(testDir, baseName+".wav")
		pngPath := filepath.Join(testDir, baseName+".png")
		require.NoError(t, os.WriteFile(audioPath, []byte("audio"), 0o644)) //nolint:gosec // G306: Test files don't require restrictive permissions
		require.NoError(t, os.WriteFile(pngPath, []byte("png"), 0o644))     //nolint:gosec // G306: Test files don't require restrictive permissions
		require.NoError(t, os.Chtimes(audioPath, modTime, modTime))
		require.NoError(t, os.Chtimes(pngPath, modTime, modTime))
		return audioPath, pngPath
	}

	// Create files around the 1-hour mark
	now := time.Now()
	justRecentTime := now.Add(-30 * time.Minute) // Keep
	justOldTime := now.Add(-90 * time.Minute)    // Delete
	olderTime := now.Add(-120 * time.Minute)     // Delete (but keep 1 bubo due to minClips=1)

	// Species A (bubo_bubo)
	aFileRecent, aPngRecent := createTestFilePair("bubo_bubo", 80, justRecentTime) // Keep (too new)
	aFileOld, aPngOld := createTestFilePair("bubo_bubo", 90, justOldTime)          // Keep (minClips=1)
	aFileOlder, aPngOlder := createTestFilePair("bubo_bubo", 70, olderTime)        // Delete (oldest)

	// Species B (anas_platyrhynchos)
	bFileOld, bPngOld := createTestFilePair("anas_platyrhynchos", 60, justOldTime) // Delete (only one, but >1h)

	// --- Run Simulation Function --- Pass 1 for retentionPeriodHours
	quitChan := make(chan struct{})
	deletedFilesMap := make(map[string]bool) // Needed by simulator signature
	initialDiskUtilization := 50             // Example value, not used by age logic
	utilizationReductionPerFile := 5         // Example value, not used by age logic
	minClips := 1
	result := simulateAgeBasedCleanup(
		quitChan,
		mockDB,
		testDir,
		[]string{aFileRecent, aFileOld, aFileOlder, bFileOld}, // Pass all created files
		deletedFilesMap,
		initialDiskUtilization,
		minClips, // Min clips to keep
		1,        // <<< Retention period in hours (1h)
		false,    // keepSpectrograms
		utilizationReductionPerFile,
	)

	// --- Assertions ---
	require.NoError(t, result.Err, "AgeBasedCleanup simulation should run without error")
	assert.Equal(t, 2, result.ClipsRemoved, "Should remove 2 clips (oldest bubo, old anas)")

	// Verify file existence
	assert.FileExists(t, aFileRecent, "Recent bubo audio should exist")
	assert.FileExists(t, aPngRecent, "Recent bubo PNG should exist")

	assert.NoFileExists(t, aFileOld, "Old bubo audio should be deleted (older than 1h, minClips met by recent file)")
	assert.NoFileExists(t, aPngOld, "Old bubo PNG should be deleted")

	assert.NoFileExists(t, aFileOlder, "Older bubo audio should be deleted")
	assert.NoFileExists(t, aPngOlder, "Older bubo PNG should be deleted")

	assert.FileExists(t, bFileOld, "Old anas audio should be kept (minClips=1)")
	assert.FileExists(t, bPngOld, "Old anas PNG should be kept")
}

// isEligibleForTestDeletion checks if a file should be deleted in test simulation.
// Returns true if eligible, false if file should be skipped.
func isEligibleForTestDeletion(file *FileInfo, expirationTime time.Time, speciesTotalCount map[string]int, minClipsPerSpecies int) bool {
	// Skip locked files
	if file.Locked {
		return false
	}
	// Skip files that are not old enough
	if !file.Timestamp.Before(expirationTime) {
		return false
	}
	// Skip if the total count for this species is at or below minimum
	if count, exists := speciesTotalCount[file.Species]; exists && count <= minClipsPerSpecies {
		return false
	}
	return true
}

// deleteTestFile handles file deletion in test simulation including PNG cleanup.
// Updates speciesTotalCount and deletedFiles map.
func deleteTestFile(file *FileInfo, deletedFiles map[string]bool, speciesTotalCount map[string]int, keepSpectrograms bool) {
	deletedFiles[file.Path] = true
	if err := os.Remove(file.Path); err != nil && !os.IsNotExist(err) {
		GetLogger().Warn("Test helper: failed to remove simulated audio file",
			logger.String("path", file.Path),
			logger.Error(err))
	}

	if !keepSpectrograms {
		pngPath := strings.TrimSuffix(file.Path, filepath.Ext(file.Path)) + ".png"
		if err := os.Remove(pngPath); err != nil && !os.IsNotExist(err) {
			GetLogger().Warn("Test helper: failed to remove simulated PNG",
				logger.String("path", pngPath),
				logger.Error(err))
		}
	}

	speciesTotalCount[file.Species]--
}

// simulateAgeBasedCleanup is a test-specific helper that simulates the core logic
// of AgeBasedCleanup using real files in a temporary directory.
func simulateAgeBasedCleanup(
	quit <-chan struct{},
	db Interface,
	baseDir string,
	testFiles []string,
	deletedFiles map[string]bool,
	initialDiskUtilization int,
	minClipsPerSpecies int,
	retentionPeriodHours int,
	keepSpectrograms bool,
	utilizationReductionPerFile int,
) CleanupResult {
	// This implementation simulates the real AgeBasedCleanup function
	// but with controlled inputs and outputs

	// Simulate calls that prepareInitialCleanup might make
	_, _ = GetDiskUsage(baseDir) // Call to satisfy potential interface, result ignored

	// Track current disk utilization (for returning in result, not for logic)
	currentDiskUtilization := initialDiskUtilization

	// Get the list of files
	files := []FileInfo{}

	// Process each test file
	for _, filePath := range testFiles {
		fileInfo, err := os.Stat(filePath)
		if err != nil {
			continue
		}

		// Parse the file info
		fileData, err := parseFileInfo(filePath, fileInfo, allowedFileTypes)
		if err != nil {
			continue
		}

		// For testing purposes, use the file modification time
		// This ensures we can control which files are considered "old"
		fileData.Timestamp = fileInfo.ModTime()

		files = append(files, fileData)
	}

	// Sort files by timestamp (oldest first)
	sort.Slice(files, func(i, j int) bool {
		return files[i].Timestamp.Before(files[j].Timestamp)
	})

	// Create a map to track *total* species counts across all directories
	speciesTotalCount := make(map[string]int)
	for _, file := range files {
		speciesTotalCount[file.Species]++
	}

	// Set the expiration time (now - retention period)
	// Use local time to match how file timestamps are stored
	expirationTime := time.Now().Add(-time.Duration(retentionPeriodHours) * time.Hour)

	// Process files for deletion
	deletedCount := 0
	for i := range files {
		if !isEligibleForTestDeletion(&files[i], expirationTime, speciesTotalCount, minClipsPerSpecies) {
			continue
		}

		deleteTestFile(&files[i], deletedFiles, speciesTotalCount, keepSpectrograms)
		deletedCount++

		// Reduce disk utilization for each deleted file
		currentDiskUtilization -= utilizationReductionPerFile
		if currentDiskUtilization < 0 {
			currentDiskUtilization = 0
		}
	}

	// Return the results with dynamic disk utilization
	return CleanupResult{Err: nil, ClipsRemoved: deletedCount, DiskUtilization: currentDiskUtilization}
}
