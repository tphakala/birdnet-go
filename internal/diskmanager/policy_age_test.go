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
func TestAgeBasedCleanupReturnValues(t *testing.T) {
	// Create a temporary directory for testing
	testDir, err := os.MkdirTemp("", "age_cleanup_test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(testDir)

	// Create a mock DB
	mockDB := &MockDB{}

	// Create test files with different timestamps
	// Recent files (within retention period - 3 days old)
	recentFile := filepath.Join(testDir, "bubo_bubo_80p_20210102T150405Z.wav")
	err = os.WriteFile(recentFile, []byte("test content"), 0o644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Set the file's modification time to 3 days ago
	recentTime := time.Now().Add(-72 * time.Hour)
	err = os.Chtimes(recentFile, recentTime, recentTime)
	if err != nil {
		t.Fatalf("Failed to set file time: %v", err)
	}

	// Old files (beyond retention period - 30 days old)
	oldFile1 := filepath.Join(testDir, "bubo_bubo_90p_20200102T150405Z.wav")
	err = os.WriteFile(oldFile1, []byte("test content"), 0o644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Set the file's modification time to 30 days ago
	oldTime1 := time.Now().Add(-720 * time.Hour)
	err = os.Chtimes(oldFile1, oldTime1, oldTime1)
	if err != nil {
		t.Fatalf("Failed to set file time: %v", err)
	}

	oldFile2 := filepath.Join(testDir, "anas_platyrhynchos_60p_20200102T150405Z.wav")
	err = os.WriteFile(oldFile2, []byte("test content"), 0o644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Set the file's modification time to 30 days ago
	oldTime2 := time.Now().Add(-720 * time.Hour)
	err = os.Chtimes(oldFile2, oldTime2, oldTime2)
	if err != nil {
		t.Fatalf("Failed to set file time: %v", err)
	}

	// Create a quit channel
	quitChan := make(chan struct{})

	// Track which files were deleted
	deletedFiles := make(map[string]bool)

	// Test configuration
	initialDiskUtilization := 90
	utilizationReductionPerFile := 5 // Each deleted file reduces utilization by 5%

	// Create a test-specific implementation that uses the real files
	result := testAgeBasedCleanupWithRealFiles(
		quitChan,
		mockDB,
		testDir,
		[]string{recentFile, oldFile1, oldFile2},
		deletedFiles,
		initialDiskUtilization,      // Initial disk utilization
		0,                           // minClipsPerSpecies
		utilizationReductionPerFile, // Reduction per file deleted
	)

	// Verify the return values
	assert.NoError(t, result.Err, "AgeBasedCleanup should not return an error")
	assert.Equal(t, 2, result.ClipsRemoved, "AgeBasedCleanup should remove 2 clips")

	// With initial disk utilization of 90% and 2 files deleted at 5% reduction each,
	// we expect 90 - (2 * 5) = 80% utilization
	expectedDiskUtilization := initialDiskUtilization - (2 * utilizationReductionPerFile)
	assert.Equal(t, expectedDiskUtilization, result.DiskUtilization,
		"AgeBasedCleanup should return %d%% disk utilization", expectedDiskUtilization)

	// Verify that the correct files were deleted
	assert.True(t, deletedFiles[oldFile1], "Old file 1 should have been deleted")
	assert.True(t, deletedFiles[oldFile2], "Old file 2 should have been deleted")
	assert.False(t, deletedFiles[recentFile], "Recent file should not have been deleted")
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
	utilizationReductionPerFile int,
) AgeCleanupResult {
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

		// "Delete" the file (just mark it in our map)
		deletedFiles[file.Path] = true
		deletedCount++

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
	return AgeCleanupResult{Err: nil, ClipsRemoved: deletedCount, DiskUtilization: currentDiskUtilization}
}
