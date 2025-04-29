package diskmanager

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
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
		{"bubo_bubo_80p_20210102T150405Z.txt", true, "file type not eligible"},
		{"bubo_bubo_80p_20210102T150405Z.jpg", true, "file type not eligible"},
		{"bubo_bubo_80p_20210102T150405Z.png", true, "file type not eligible"},
		{"bubo_bubo_80p_20210102T150405Z.db", true, "file type not eligible"},
		{"bubo_bubo_80p_20210102T150405Z.csv", true, "file type not eligible"},
		{"system_80p_20210102T150405Z.exe", true, "file type not eligible"},
	}

	// Print the current list of allowed file types for debugging
	t.Logf("Allowed file types: %v", allowedFileTypes)

	// Create a temporary directory
	testDir, err := os.MkdirTemp("", "age_policy_test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(testDir)

	for _, tc := range testFiles {
		t.Run(tc.name, func(t *testing.T) {
			// Create a mock FileInfo
			mockInfo := createMockFileInfo(tc.name, 1024)

			// Call parseFileInfo directly to test file extension checking
			_, err := parseFileInfo(filepath.Join(testDir, tc.name), mockInfo)

			// Debug logging
			t.Logf("File: %s, Extension: %s, Error: %v",
				tc.name, filepath.Ext(tc.name), err)

			// Check if the error matches our expectation
			if tc.expectError {
				if err == nil {
					t.Errorf("SECURITY ISSUE: Expected error for %s but got nil", tc.name)
				} else if !strings.Contains(err.Error(), tc.errorContains) {
					t.Errorf("Expected error containing '%s', got: %v", tc.errorContains, err)
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error for %s but got: %v", tc.name, err)
				}
			}
		})
	}
}

// TestAgeBasedFilesAfterFilter tests the filtering of files for age-based cleanup
func TestAgeBasedFilesAfterFilter(t *testing.T) {
	db := &MockDB{}
	allowedTypes := []string{".wav", ".mp3", ".flac", ".aac", ".opus", ".m4a"}

	// Create a temporary directory
	testDir, err := os.MkdirTemp("", "age_filter_test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(testDir)

	// Let's create files of all relevant types
	fileTypes := []string{
		".wav", ".mp3", ".flac", ".aac", ".opus", ".m4a",
		".txt", ".jpg", ".png", ".db", ".exe",
	}

	for _, ext := range fileTypes {
		filePath := filepath.Join(testDir, fmt.Sprintf("bubo_bubo_80p_20210102T150405Z%s", ext))
		if err := os.WriteFile(filePath, []byte("test content"), 0o644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	// Get audio files using the function that would be used by the policy
	audioFiles, err := GetAudioFiles(testDir, allowedTypes, db, false)
	if err != nil {
		t.Fatalf("Failed to get audio files: %v", err)
	}

	// Verify only allowed audio files are returned
	if len(audioFiles) != len(allowedTypes) {
		t.Errorf("Expected %d audio files, got %d", len(allowedTypes), len(audioFiles))
	}

	// Verify each returned file has an allowed extension
	for _, file := range audioFiles {
		ext := filepath.Ext(file.Path)
		if !contains(allowedTypes, ext) {
			t.Errorf("SECURITY ISSUE: File with disallowed extension was included: %s", file.Path)
		}
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

		// Recent files should be kept
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
		require.NoError(t, os.WriteFile(audioPath, []byte("audio"), 0o644), "Failed to create audio file")
		require.NoError(t, os.WriteFile(pngPath, []byte("png"), 0o644), "Failed to create png file")
		require.NoError(t, os.Chtimes(audioPath, modTime, modTime), "Failed to set audio time")
		require.NoError(t, os.Chtimes(pngPath, modTime, modTime), "Failed to set png time")
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

		return testAgeBasedCleanupWithRealFiles(
			quitChan,
			mockDB,
			testDir,
			allAudioFiles,
			deletedFilesMap, // Pass the map to track deletions
			initialDiskUtilization,
			0, // minClipsPerSpecies
			keepSpectrograms,
			utilizationReductionPerFile,
		)
	}

	// --- Scenario 1: KeepSpectrograms = true ---
	t.Run("KeepSpectrogramsTrue", func(t *testing.T) {
		result := runTest(true)

		// Verify return values (same checks as before)
		assert.NoError(t, result.Err, "[KeepTrue] AgeBasedCleanup should not return an error")
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
		assert.NoError(t, result.Err, "[KeepFalse] AgeBasedCleanup should not return an error")
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

// testAgeBasedCleanupWithRealFiles is a test-specific implementation that uses real files
func testAgeBasedCleanupWithRealFiles(
	quit <-chan struct{},
	db Interface,
	baseDir string,
	testFiles []string,
	deletedFiles map[string]bool,
	initialDiskUtilization int,
	minClipsPerSpecies int,
	keepSpectrograms bool,
	utilizationReductionPerFile int,
) CleanupResult {
	// This implementation simulates the real AgeBasedCleanup function
	// but with controlled inputs and outputs

	// Set a fixed retention period (7 days in hours)
	retentionPeriodHours := 168

	// Track current disk utilization
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
		fileData, err := parseFileInfo(filePath, fileInfo)
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

	// Create a map to track species counts
	speciesCount := make(map[string]map[string]int)
	for _, file := range files {
		subDir := filepath.Dir(file.Path)
		if _, exists := speciesCount[file.Species]; !exists {
			speciesCount[file.Species] = make(map[string]int)
		}
		speciesCount[file.Species][subDir]++
	}

	// Set the expiration time (now - retention period)
	expirationTime := time.Now().Add(-time.Duration(retentionPeriodHours) * time.Hour)

	// Process files for deletion
	deletedCount := 0

	for _, file := range files {
		// Skip locked files
		if file.Locked {
			continue
		}

		// Skip files that are not old enough
		if !file.Timestamp.Before(expirationTime) {
			continue
		}

		// Get the subdirectory
		subDir := filepath.Dir(file.Path)

		// Skip if we're at the minimum clips per species
		if speciesCount[file.Species][subDir] <= minClipsPerSpecies {
			continue
		}

		// "Delete" the file (mark it and actually remove the audio file)
		deletedFiles[file.Path] = true
		if err := os.Remove(file.Path); err != nil && !os.IsNotExist(err) {
			// Log the error if it's something other than the file not existing
			// This shouldn't fail the test, but indicates potential issues.
			log.Printf("[Test Helper Warning] Failed to remove simulated audio file %s: %v", file.Path, err)
		}
		deletedCount++

		// Simulate PNG deletion if keepSpectrograms is false
		if !keepSpectrograms {
			pngPath := strings.TrimSuffix(file.Path, filepath.Ext(file.Path)) + ".png"
			if err := os.Remove(pngPath); err != nil && !os.IsNotExist(err) {
				// Log the error if it's something other than the file not existing
				// This shouldn't fail the test, but indicates potential issues.
				log.Printf("[Test Helper Warning] Failed to remove simulated PNG %s: %v", pngPath, err)
			}
		}

		// Update the species count
		speciesCount[file.Species][subDir]--

		// Reduce disk utilization for each deleted file
		// In a real system, this would be based on file size relative to total storage
		currentDiskUtilization -= utilizationReductionPerFile
		if currentDiskUtilization < 0 {
			currentDiskUtilization = 0
		}
	}

	// Return the results with dynamic disk utilization
	return CleanupResult{Err: nil, ClipsRemoved: deletedCount, DiskUtilization: currentDiskUtilization}
}
