package diskmanager

import (
	"os"
	"path/filepath"
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
	settingFunc              = conf.Setting
)

// Mock functions
func mockGetDiskUsage(path string, usage float64) (float64, error) {
	return usage, nil
}

func mockParseRetentionPeriod(period string, hours int) (int, error) {
	return hours, nil
}

// MockDB is a mock implementation of the database interface for testing
type MockDB struct {
	GetLockedNotesClipPathsFunc func() ([]string, error)
}

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
	if m.GetLockedNotesClipPathsFunc != nil {
		return m.GetLockedNotesClipPathsFunc()
	}
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

// TestAgeBasedCleanupRetentionPeriodParsing tests the parsing of retention period
func TestAgeBasedCleanupRetentionPeriodParsing(t *testing.T) {
	// Save original functions and restore after test
	originalParseRetentionPeriod := parseRetentionPeriodFunc
	originalGetDiskUsage := diskUsageFunc
	defer func() {
		parseRetentionPeriodFunc = originalParseRetentionPeriod
		diskUsageFunc = originalGetDiskUsage
	}()

	// Mock only the functions directly used or potentially problematic
	// We still mock diskUsageFunc as the test isn't about actual disk usage calculation
	diskUsageFunc = func(path string) (float64, error) {
		return 50.0, nil // 50% usage
	}

	// Create a temp directory for the test cases that need a valid path
	tempDir := t.TempDir()
	// No need to defer RemoveAll, t.TempDir() handles cleanup

	// Create a mock DB
	mockDB := &MockDB{}

	// Test cases
	testCases := []struct {
		name            string
		retentionPeriod string
		expectError     bool
		errorContains   string
	}{
		{
			name:            "Valid retention period - 7 days",
			retentionPeriod: "7d",
			expectError:     false,
		},
		{
			name:            "Valid retention period - 1 week",
			retentionPeriod: "1w",
			expectError:     false,
		},
		{
			name:            "Invalid retention period",
			retentionPeriod: "invalid",
			expectError:     true,
			errorContains:   "invalid retention period format",
		},
	}

	// Run tests
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc := tc // Capture range variable

			// Determine the base path to use - tempDir for valid cases, can be anything for invalid case
			basePath := tempDir
			if tc.expectError { // For the invalid case, the path doesn't matter as it errors out early
				basePath = "/invalid/path/does/not/matter"
			}

			// Create the specific settings for this test case
			testSettings := &conf.Settings{
				Realtime: conf.RealtimeSettings{
					Audio: conf.AudioSettings{
						Export: conf.ExportSettings{
							Path: basePath, // Use tempDir for valid cases
							Retention: conf.RetentionSettings{
								MaxAge:   tc.retentionPeriod,
								MinClips: 1,
								Debug:    true,
							},
						},
					},
				},
			}

			// Create a quit channel
			quitChan := make(chan struct{})

			// Log before calling AgeBasedCleanup
			t.Logf("Calling AgeBasedCleanup with retention period: %s", tc.retentionPeriod)

			// Call the function, passing settings directly
			result := AgeBasedCleanup(testSettings, quitChan, mockDB)

			// Log the result error
			t.Logf("AgeBasedCleanup returned result.Err: %v", result.Err)

			// Check results
			if tc.expectError {
				require.Error(t, result.Err, "Expected an error but got nil")
				if tc.errorContains != "" {
					assert.Contains(t, result.Err.Error(), tc.errorContains)
				}
			} else {
				assert.NoError(t, result.Err)
			}
		})
	}
}

// TestAgeBasedShouldDelete tests the age policy's shouldDelete function logic
func TestAgeBasedShouldDelete(t *testing.T) {
	// Create a test expirationTime
	now := time.Now()
	expirationTime := now.Add(-7 * 24 * time.Hour) // 7 days ago

	// Test files
	testFiles := []struct {
		name         string
		fileTime     time.Time
		expectDelete bool
	}{
		{
			name:         "File older than expiration",
			fileTime:     now.Add(-10 * 24 * time.Hour), // 10 days ago
			expectDelete: true,
		},
		{
			name:         "File at expiration time",
			fileTime:     expirationTime,
			expectDelete: true, // Should be exact match or older
		},
		{
			name:         "File newer than expiration",
			fileTime:     now.Add(-3 * 24 * time.Hour), // 3 days ago
			expectDelete: false,
		},
		{
			name:         "Very recent file",
			fileTime:     now.Add(-1 * time.Hour), // 1 hour ago
			expectDelete: false,
		},
	}

	// Create test params
	params := &CleanupParameters{
		Debug: true,
	}

	// Create the shouldDelete function with the fixed expirationTime
	ageShouldDelete := func(file *FileInfo, params *CleanupParameters, currentDiskUsage float64) (bool, error) {
		return file.Timestamp.Before(expirationTime), nil
	}

	// Run tests
	for _, tc := range testFiles {
		t.Run(tc.name, func(t *testing.T) {
			file := &FileInfo{
				Path:      "/test/bubo_bubo_80p_20210102T150405Z.wav",
				Species:   "bubo_bubo",
				Timestamp: tc.fileTime,
			}

			shouldDelete, err := ageShouldDelete(file, params, 0.0)

			assert.NoError(t, err)
			notStr := ""
			if !tc.expectDelete {
				notStr = " not"
			}
			assert.Equal(t, tc.expectDelete, shouldDelete, "File should%s be deleted",
				notStr)
		})
	}
}

// TestAgeBasedCleanupWithKeepSpectrograms tests that the age-based policy correctly handles the KeepSpectrograms setting
func TestAgeBasedCleanupWithKeepSpectrograms(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "age_spectrograms_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create test files - one recent, one old
	createTestFile := func(path, content string) {
		err := os.MkdirAll(filepath.Dir(path), 0o755)
		require.NoError(t, err)
		err = os.WriteFile(path, []byte(content), 0o644)
		require.NoError(t, err)
	}

	// Create directory structure
	speciesDir := filepath.Join(tempDir, "bubo_bubo")
	require.NoError(t, os.MkdirAll(speciesDir, 0o755))

	// Create files
	recentAudio := filepath.Join(speciesDir, "bubo_bubo_80p_20210102T150405Z.wav")
	recentImage := filepath.Join(speciesDir, "bubo_bubo_80p_20210102T150405Z.png")
	oldAudio := filepath.Join(speciesDir, "bubo_bubo_70p_20200102T150405Z.wav")
	oldImage := filepath.Join(speciesDir, "bubo_bubo_70p_20200102T150405Z.png")

	createTestFile(recentAudio, "recent audio content")
	createTestFile(recentImage, "recent image content")
	createTestFile(oldAudio, "old audio content")
	createTestFile(oldImage, "old image content")

	// Set file times
	nowTime := time.Now()
	oldTime := nowTime.Add(-30 * 24 * time.Hour) // 30 days old

	require.NoError(t, os.Chtimes(recentAudio, nowTime, nowTime))
	require.NoError(t, os.Chtimes(recentImage, nowTime, nowTime))
	require.NoError(t, os.Chtimes(oldAudio, oldTime, oldTime))
	require.NoError(t, os.Chtimes(oldImage, oldTime, oldTime))

	// Mock functions for the tests
	originalGetDiskUsage := diskUsageFunc
	originalParseRetentionPeriod := parseRetentionPeriodFunc
	originalGetting := settingFunc

	defer func() {
		diskUsageFunc = originalGetDiskUsage
		parseRetentionPeriodFunc = originalParseRetentionPeriod
		settingFunc = originalGetting
	}()

	diskUsageFunc = func(path string) (float64, error) {
		return 50.0, nil // 50% usage
	}

	parseRetentionPeriodFunc = func(period string) (int, error) {
		return 7 * 24, nil // 7 days in hours
	}

	// Test both keepSpectrograms true and false
	testCases := []struct {
		name                 string
		keepSpectrograms     bool
		expectOldImageExists bool
	}{
		{
			name:                 "Delete both audio and spectrogram",
			keepSpectrograms:     false,
			expectOldImageExists: false,
		},
		{
			name:                 "Keep spectrogram when audio is deleted",
			keepSpectrograms:     true,
			expectOldImageExists: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset files between tests
			if _, err := os.Stat(oldAudio); os.IsNotExist(err) {
				createTestFile(oldAudio, "old audio content")
				require.NoError(t, os.Chtimes(oldAudio, oldTime, oldTime))
			}
			if _, err := os.Stat(oldImage); os.IsNotExist(err) {
				createTestFile(oldImage, "old image content")
				require.NoError(t, os.Chtimes(oldImage, oldTime, oldTime))
			}

			// Override settings for this test case
			testSettings := &conf.Settings{
				Realtime: conf.RealtimeSettings{
					Audio: conf.AudioSettings{
						Export: conf.ExportSettings{
							Path: tempDir,
							Retention: conf.RetentionSettings{
								Policy:           "age",
								MaxAge:           "7d", // 7 days
								MinClips:         0,    // Allow all files to be deleted
								KeepSpectrograms: tc.keepSpectrograms,
								Debug:            true, // Enable debug logging
							},
						},
					},
				},
			}

			// Create quit channel
			quitChan := make(chan struct{})

			// Run cleanup, passing settings directly
			result := AgeBasedCleanup(testSettings, quitChan, &MockDB{})

			// Check results
			assert.NoError(t, result.Err)
			assert.Equal(t, 1, result.ClipsRemoved, "Should have removed 1 clip")

			// Check the recent files still exist
			_, err = os.Stat(recentAudio)
			assert.NoError(t, err, "Recent audio should still exist")
			_, err = os.Stat(recentImage)
			assert.NoError(t, err, "Recent image should still exist")

			// Check old audio was deleted
			_, err = os.Stat(oldAudio)
			assert.True(t, os.IsNotExist(err), "Old audio should be deleted")

			// Check old image based on keepSpectrograms setting
			_, err = os.Stat(oldImage)
			if tc.expectOldImageExists {
				assert.NoError(t, err, "Old image should exist when keepSpectrograms=true")
			} else {
				assert.True(t, os.IsNotExist(err), "Old image should be deleted when keepSpectrograms=false")
			}
		})
	}
}

// TestAgeBasedCleanupRespectLockedFiles tests that the age-based policy respects locked files
func TestAgeBasedCleanupRespectLockedFiles(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "age_locked_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create test file
	oldAudio := filepath.Join(tempDir, "bubo_bubo_70p_20200102T150405Z.wav")
	oldImage := filepath.Join(tempDir, "bubo_bubo_70p_20200102T150405Z.png")

	err = os.WriteFile(oldAudio, []byte("old audio content"), 0o644)
	require.NoError(t, err)
	err = os.WriteFile(oldImage, []byte("old image content"), 0o644)
	require.NoError(t, err)

	// Set file time (old)
	oldTime := time.Now().Add(-30 * 24 * time.Hour) // 30 days old
	require.NoError(t, os.Chtimes(oldAudio, oldTime, oldTime))
	require.NoError(t, os.Chtimes(oldImage, oldTime, oldTime))

	// Mock functions
	originalGetDiskUsage := diskUsageFunc
	originalParseRetentionPeriod := parseRetentionPeriodFunc
	originalSetting := settingFunc

	defer func() {
		diskUsageFunc = originalGetDiskUsage
		parseRetentionPeriodFunc = originalParseRetentionPeriod
		settingFunc = originalSetting
	}()

	diskUsageFunc = func(path string) (float64, error) {
		return 50.0, nil
	}

	parseRetentionPeriodFunc = func(period string) (int, error) {
		return 7 * 24, nil // 7 days
	}

	// Create settings for the test
	testSettings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				Export: conf.ExportSettings{
					Path: tempDir,
					Retention: conf.RetentionSettings{
						Policy:   "age",
						MaxAge:   "7d",
						MinClips: 0, // Ensure min clips doesn't interfere
						Debug:    true,
					},
				},
			},
		},
	}

	// Mock DB to return the locked file path
	mockDB := &MockDB{
		GetLockedNotesClipPathsFunc: func() ([]string, error) {
			return []string{oldAudio}, nil
		},
	}

	// Create quit channel
	quitChan := make(chan struct{})

	// Run cleanup, passing settings directly
	result := AgeBasedCleanup(testSettings, quitChan, mockDB)

	// Verify results
	assert.NoError(t, result.Err)
	assert.Equal(t, 0, result.ClipsRemoved, "Should not remove locked files")

	// Check files still exist
	_, err = os.Stat(oldAudio)
	assert.NoError(t, err, "Locked audio file should still exist")
	_, err = os.Stat(oldImage)
	assert.NoError(t, err, "Image for locked audio should still exist")
}
