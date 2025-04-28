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
	mock_diskmanager "github.com/tphakala/birdnet-go/internal/diskmanager/mocks"
	gomock "go.uber.org/mock/gomock"
)

// Original function signature references for testing
var (
	originalGetDiskUsage  = GetDiskUsage
	originalGetAudioFiles = GetAudioFiles
	originalOsRemove      = osRemove
	// Add a variable for mocking conf.Setting in usage tests
	usageSettingFunc = conf.Setting
)

// MockFileInfo implements os.FileInfo for testing
type MockFileInfo struct {
	FileName    string
	FileSize    int64
	FileMode    os.FileMode
	FileModTime time.Time
	FileIsDir   bool
	FileSys     interface{}
}

func (m *MockFileInfo) Name() string       { return m.FileName }
func (m *MockFileInfo) Size() int64        { return m.FileSize }
func (m *MockFileInfo) Mode() os.FileMode  { return m.FileMode }
func (m *MockFileInfo) ModTime() time.Time { return m.FileModTime }
func (m *MockFileInfo) IsDir() bool        { return m.FileIsDir }
func (m *MockFileInfo) Sys() interface{}   { return m.FileSys }

// Helper function to create a mock FileInfo
func createMockFileInfo(filename string, size int64) os.FileInfo {
	return &MockFileInfo{
		FileName:    filename,
		FileSize:    size,
		FileMode:    0o644,
		FileModTime: time.Now(),
		FileIsDir:   false,
	}
}

// Helper function to parse time string
func parseTime(timeStr string) time.Time {
	t, _ := time.Parse("20060102T150405Z", timeStr)
	return t
}

// TestFileTypesEligibleForDeletion tests that only allowed file types can be deleted
func TestFileTypesEligibleForDeletion(t *testing.T) {
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
		{"bubo_bubo_80p_20210102T150405Z.m4a", false, ""},

		// Non-audio files - should return errors
		{"bubo_bubo_80p_20210102T150405Z.txt", true, "file type not eligible"},
		{"bubo_bubo_80p_20210102T150405Z.jpg", true, "file type not eligible"},
		{"bubo_bubo_80p_20210102T150405Z.png", true, "file type not eligible"},
		{"bubo_bubo_80p_20210102T150405Z.db", true, "file type not eligible"},
		{"bubo_bubo_80p_20210102T150405Z.csv", true, "file type not eligible"},
		{"system_80p_20210102T150405Z.exe", true, "file type not eligible"},
	}

	// Create a temporary directory
	testDir, err := os.MkdirTemp("", "file_types_test")
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

// TestParseFileInfoWithDifferentExtensions tests that file info parsing works with different extensions
func TestParseFileInfoWithDifferentExtensions(t *testing.T) {
	// Different valid file name patterns to test
	testFiles := []struct {
		name          string
		expectSpecies string
		expectConf    int
	}{
		{"bubo_bubo_80p_20210102T150405Z.wav", "bubo_bubo", 80},
		{"bubo_bubo_90p_20210102T150405Z.mp3", "bubo_bubo", 90},
		{"turdus_merula_75p_20210102T150405Z.flac", "turdus_merula", 75},
		{"turdus_merula_95p_20210102T150405Z.aac", "turdus_merula", 95},
		{"corvus_corax_60p_20210102T150405Z.opus", "corvus_corax", 60},
		{"corvus_corax_65p_20210102T150405Z.m4a", "corvus_corax", 65},
	}

	for _, tc := range testFiles {
		t.Run(tc.name, func(t *testing.T) {
			mockInfo := createMockFileInfo(tc.name, 1024)
			fileInfo, err := parseFileInfo("/test/path/"+tc.name, mockInfo)

			assert.NoError(t, err, "Should parse valid file name without error")
			assert.Equal(t, tc.expectSpecies, fileInfo.Species, "Should extract correct species")
			assert.Equal(t, tc.expectConf, fileInfo.Confidence, "Should extract correct confidence")
		})
	}
}

// TestParseFileInfoMp3Extension tests that mp3 files are parsed correctly
func TestParseFileInfoMp3Extension(t *testing.T) {
	mockInfo := createMockFileInfo("bubo_bubo_80p_20210102T150405Z.mp3", 1024)
	fileInfo, err := parseFileInfo("/test/path/bubo_bubo_80p_20210102T150405Z.mp3", mockInfo)

	assert.NoError(t, err, "Should parse valid mp3 file name without error")
	assert.Equal(t, "bubo_bubo", fileInfo.Species, "Should extract correct species")
	assert.Equal(t, 80, fileInfo.Confidence, "Should extract correct confidence")
	assert.Equal(t, "/test/path/bubo_bubo_80p_20210102T150405Z.mp3", fileInfo.Path, "Should keep the full path")
	assert.Equal(t, int64(1024), fileInfo.Size, "Should store the file size")
	assert.False(t, fileInfo.Locked, "File should not be locked by default")
}

// TestParseFileInfoProductionFormat tests file naming pattern from production use
func TestParseFileInfoProductionFormat(t *testing.T) {
	// Production format files with extra parts in the name
	testFiles := []struct {
		name          string
		expectSpecies string
		expectConf    int
	}{
		{"bubo_bubo_80p_20210102T150405Z.wav", "bubo_bubo", 80},
		{"bubo_bubo_90p_20210102T150405Z_400px.wav", "bubo_bubo", 90},
		{"turdus_merula_75p_20210102T150405Z_large.wav", "turdus_merula", 75},
		{"turdus_merula_95p_20210102T150405Z_trimmed.wav", "turdus_merula", 95},
		{"corvus_corax_60p_20210102T150405Z_analyzed.wav", "corvus_corax", 60},
	}

	for _, tc := range testFiles {
		t.Run(tc.name, func(t *testing.T) {
			mockInfo := createMockFileInfo(tc.name, 1024)
			fileInfo, err := parseFileInfo("/test/path/"+tc.name, mockInfo)

			assert.NoError(t, err, "Should parse production format file name without error")
			assert.Equal(t, tc.expectSpecies, fileInfo.Species, "Should extract correct species")
			assert.Equal(t, tc.expectConf, fileInfo.Confidence, "Should extract correct confidence")
		})
	}
}

// TestSortFiles tests the priority sorting for usage-based policy
func TestSortFiles(t *testing.T) {
	// Create test files with different characteristics
	files := []FileInfo{
		{
			Path:       "/test/species1/file1.wav",
			Species:    "species1",
			Confidence: 80,
			Timestamp:  time.Now().Add(-5 * 24 * time.Hour), // 5 days old
		},
		{
			Path:       "/test/species1/file2.wav",
			Species:    "species1",
			Confidence: 90,
			Timestamp:  time.Now().Add(-7 * 24 * time.Hour), // 7 days old - older than file1
		},
		{
			Path:       "/test/species2/file3.wav",
			Species:    "species2",
			Confidence: 70,
			Timestamp:  time.Now().Add(-7 * 24 * time.Hour), // Same age as file2
		},
		{
			Path:       "/test/species1/file4.wav",
			Species:    "species1",
			Confidence: 60,
			Timestamp:  time.Now().Add(-7 * 24 * time.Hour), // Same age as file2, lower confidence
		},
	}

	// Sort the files by usage policy priority
	speciesDirCount := sortFiles(files, true)

	// Verify the sorting order
	assert.Equal(t, 4, len(files), "Should still have 4 files after sorting")

	// Based on priority (oldest first, then most frequent species, then lowest confidence),
	// the order should be:
	// file2 or file4 or file3 (same age, depends on species and confidence)
	// then file1 (newest)

	// First file should be one of the 7-day old files
	oldFileFound := (files[0].Path == "/test/species1/file2.wav" ||
		files[0].Path == "/test/species1/file4.wav" ||
		files[0].Path == "/test/species2/file3.wav")
	assert.True(t, oldFileFound, "First file should be one of the older files")

	// Last file should be file1 (newest)
	assert.Equal(t, "/test/species1/file1.wav", files[len(files)-1].Path, "Newest file should be sorted last")

	// Verify the species count map was built correctly
	assert.Equal(t, 2, len(speciesDirCount), "Should have 2 species in the count map")
	assert.Equal(t, 3, speciesDirCount["species1"]["/test/species1"], "species1 should have 3 files in /test/species1")
	assert.Equal(t, 1, speciesDirCount["species2"]["/test/species2"], "species2 should have 1 file in /test/species2")
}

// MockSettingsGetter is a function that returns mocked settings
type MockSettingsGetter func() *conf.Settings

// TestUsageBasedCleanupThresholdChecking tests threshold checking in usage-based policy
func TestUsageBasedCleanupThresholdChecking(t *testing.T) {
	// Set up a mock controller
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Create mock DB interface
	mockDB := mock_diskmanager.NewMockInterface(ctrl)
	mockDB.EXPECT().GetLockedNotesClipPaths().Return([]string{}, nil).AnyTimes()

	// Test cases with different thresholds and disk usages
	testCases := []struct {
		name          string  // Threshold as a string (e.g., "80%")
		threshold     string  // Current disk usage percentage
		diskUsage     float64 // Whether cleanup should be triggered
		expectCleanup bool
	}{
		{
			name:          "Usage above threshold triggers cleanup",
			threshold:     "80%",
			diskUsage:     85.0,
			expectCleanup: true,
		},
		{
			name:          "Usage at threshold does not trigger cleanup",
			threshold:     "80%",
			diskUsage:     80.0,
			expectCleanup: false,
		},
		{
			name:          "Usage below threshold does not trigger cleanup",
			threshold:     "80%",
			diskUsage:     75.0,
			expectCleanup: false,
		},
		{
			name:          "Very high usage triggers cleanup",
			threshold:     "90%",
			diskUsage:     95.0,
			expectCleanup: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Save original functions to restore later
			origGetDiskUsage := originalGetDiskUsage // Use the package var
			originalGetSetting := usageSettingFunc

			// Create mock GetDiskUsage function
			mockGetDiskUsage := func(path string) (float64, error) {
				return tc.diskUsage, nil
			}

			// Create mock settings
			mockSettings := func() *conf.Settings {
				return &conf.Settings{
					Realtime: conf.RealtimeSettings{
						Audio: conf.AudioSettings{
							Export: conf.ExportSettings{
								Path: "/test",
								Retention: conf.RetentionSettings{
									Policy:   "usage",
									MaxUsage: tc.threshold,
									MinClips: 1,
								},
							},
						},
					},
				}
			}

			// Replace the actual functions with mocks
			originalGetDiskUsage = mockGetDiskUsage // Assign to the package var
			usageSettingFunc = mockSettings

			// Restore the original functions after test
			defer func() {
				originalGetDiskUsage = origGetDiskUsage // Restore the package var
				usageSettingFunc = originalGetSetting
			}()

			// Create a quit channel
			quitChan := make(chan struct{})

			// Call the function
			result := UsageBasedCleanup(quitChan, mockDB)

			// For cleanup triggered cases, we expect no error but cannot assert on ClipsRemoved
			// because the actual deletion depends on available files
			if tc.expectCleanup {
				assert.NoError(t, result.Err, "Should not return error when cleanup is triggered")
			} else {
				assert.NoError(t, result.Err, "Should not return error when cleanup is not needed")
				assert.Equal(t, 0, result.ClipsRemoved, "Should not remove any clips when below threshold")
				assert.Equal(t, int(tc.diskUsage), result.DiskUtilization, "Should report correct disk utilization")
			}
		})
	}
}

// TestUsageBasedCleanupWithKeepSpectrograms tests the KeepSpectrograms setting in usage policy
func TestUsageBasedCleanupWithKeepSpectrograms(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "usage_spectrograms_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create test files
	createTestFile := func(path, content string) {
		err := os.MkdirAll(filepath.Dir(path), 0o755)
		require.NoError(t, err)
		err = os.WriteFile(path, []byte(content), 0o644)
		require.NoError(t, err)
	}

	// Create two species directories with two files each
	species1Dir := filepath.Join(tempDir, "species1")
	species2Dir := filepath.Join(tempDir, "species2")

	audioFile1 := filepath.Join(species1Dir, "species1_80p_20210101T150405Z.wav")
	imgFile1 := filepath.Join(species1Dir, "species1_80p_20210101T150405Z.png")
	audioFile2 := filepath.Join(species1Dir, "species1_85p_20210102T150405Z.wav")
	imgFile2 := filepath.Join(species1Dir, "species1_85p_20210102T150405Z.png")
	audioFile3 := filepath.Join(species2Dir, "species2_75p_20210101T150405Z.wav")
	imgFile3 := filepath.Join(species2Dir, "species2_75p_20210101T150405Z.png")
	audioFile4 := filepath.Join(species2Dir, "species2_70p_20210102T150405Z.wav")
	imgFile4 := filepath.Join(species2Dir, "species2_70p_20210102T150405Z.png")

	createTestFile(audioFile1, "audio content 1")
	createTestFile(imgFile1, "image content 1")
	createTestFile(audioFile2, "audio content 2")
	createTestFile(imgFile2, "image content 2")
	createTestFile(audioFile3, "audio content 3")
	createTestFile(imgFile3, "image content 3")
	createTestFile(audioFile4, "audio content 4")
	createTestFile(imgFile4, "image content 4")

	// Test both keepSpectrograms true and false
	testCases := []struct {
		name               string
		keepSpectrograms   bool
		expectImagesRemain bool
	}{
		{
			name:               "Delete both audio and spectrograms",
			keepSpectrograms:   false,
			expectImagesRemain: false,
		},
		{
			name:               "Keep spectrograms when audio is deleted",
			keepSpectrograms:   true,
			expectImagesRemain: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Recreate files between tests if they were deleted
			for _, path := range []string{audioFile1, audioFile2, audioFile3, audioFile4, imgFile1, imgFile2, imgFile3, imgFile4} {
				if _, err := os.Stat(path); os.IsNotExist(err) {
					content := "content"
					if strings.Contains(path, "audio") {
						content = "audio " + content
					} else {
						content = "image " + content
					}
					createTestFile(path, content)
				}
			}

			// Save original functions
			origGetDiskUsage := originalGetDiskUsage // Use the package var
			origGetSetting := usageSettingFunc

			// First call returns high usage, subsequent calls return lower usage
			// This ensures at least some files will be deleted
			diskUsageCallCount := 0
			mockGetDiskUsage := func(path string) (float64, error) {
				diskUsageCallCount++
				if diskUsageCallCount == 1 {
					return 90.0, nil // Initial high usage to trigger cleanup
				}
				return 75.0, nil // Below threshold after some deletions
			}

			// Mock settings
			mockSettings := func() *conf.Settings {
				return &conf.Settings{
					Realtime: conf.RealtimeSettings{
						Audio: conf.AudioSettings{
							Export: conf.ExportSettings{
								Path: tempDir,
								Retention: conf.RetentionSettings{
									Policy:           "usage",
									MaxUsage:         "80%",
									MinClips:         1, // Keep at least 1 file per species
									KeepSpectrograms: tc.keepSpectrograms,
								},
							},
						},
					},
				}
			}

			// Replace functions with mocks
			originalGetDiskUsage = mockGetDiskUsage // Assign to the package var
			usageSettingFunc = mockSettings

			// Restore original functions after test
			defer func() {
				originalGetDiskUsage = origGetDiskUsage // Restore the package var
				usageSettingFunc = origGetSetting
			}()

			// Create mock DB
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			mockDB := mock_diskmanager.NewMockInterface(ctrl)
			mockDB.EXPECT().GetLockedNotesClipPaths().Return([]string{}, nil).AnyTimes()

			// Run cleanup
			quitChan := make(chan struct{})
			result := UsageBasedCleanup(quitChan, mockDB)

			// Verify results
			assert.NoError(t, result.Err, "Cleanup should succeed")

			// Count remaining files
			remainingAudioFiles := 0
			remainingImageFiles := 0

			for _, path := range []string{audioFile1, audioFile2} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					remainingAudioFiles++
				}
			}
			assert.GreaterOrEqual(t, remainingAudioFiles, 1, "Should keep at least 1 audio file from species1")

			for _, path := range []string{audioFile3, audioFile4} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					remainingAudioFiles++
				}
			}
			assert.GreaterOrEqual(t, remainingAudioFiles, 2, "Should keep at least 1 audio file from each species")

			// Check image files
			for _, path := range []string{imgFile1, imgFile2, imgFile3, imgFile4} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					remainingImageFiles++
				}
			}

			if tc.expectImagesRemain {
				assert.Equal(t, 4, remainingImageFiles, "All image files should remain when keepSpectrograms=true")
			} else {
				assert.Equal(t, remainingAudioFiles, remainingImageFiles,
					"Number of image files should match audio files when keepSpectrograms=false")
			}
		})
	}
}

// TestUsageBasedCleanupRespectLockedFiles tests that the usage-based policy respects locked files
func TestUsageBasedCleanupRespectLockedFiles(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "usage_locked_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create test files
	audioFile := filepath.Join(tempDir, "bubo_bubo_80p_20210101T150405Z.wav")
	imgFile := filepath.Join(tempDir, "bubo_bubo_80p_20210101T150405Z.png")

	err = os.WriteFile(audioFile, []byte("audio content"), 0o644)
	require.NoError(t, err)
	err = os.WriteFile(imgFile, []byte("image content"), 0o644)
	require.NoError(t, err)

	// Save original functions
	origGetDiskUsage := originalGetDiskUsage // Use the package var
	origGetSetting := usageSettingFunc

	// Return high disk usage to trigger cleanup
	mockGetDiskUsage := func(path string) (float64, error) {
		return 90.0, nil
	}

	// Mock settings
	mockSettings := func() *conf.Settings {
		return &conf.Settings{
			Realtime: conf.RealtimeSettings{
				Audio: conf.AudioSettings{
					Export: conf.ExportSettings{
						Path: tempDir,
						Retention: conf.RetentionSettings{
							Policy:           "usage",
							MaxUsage:         "80%",
							MinClips:         0, // Allow all files to be deleted
							KeepSpectrograms: false,
						},
					},
				},
			},
		}
	}

	// Replace functions with mocks
	originalGetDiskUsage = mockGetDiskUsage // Assign to the package var
	usageSettingFunc = mockSettings

	// Restore original functions after test
	defer func() {
		originalGetDiskUsage = origGetDiskUsage // Restore the package var
		usageSettingFunc = origGetSetting
	}()

	// Create mock DB that returns our file as locked
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockDB := mock_diskmanager.NewMockInterface(ctrl)
	mockDB.EXPECT().GetLockedNotesClipPaths().Return([]string{audioFile}, nil).AnyTimes()

	// Run cleanup
	quitChan := make(chan struct{})
	result := UsageBasedCleanup(quitChan, mockDB)

	// Verify results
	assert.NoError(t, result.Err)
	assert.Equal(t, 0, result.ClipsRemoved, "Should not remove any clips when all are locked")

	// Verify files still exist
	_, err = os.Stat(audioFile)
	assert.NoError(t, err, "Locked audio file should still exist")
	_, err = os.Stat(imgFile)
	assert.NoError(t, err, "Image for locked audio should still exist")
}

// Define a variable for os.Remove to allow mocking in tests
var osRemove = os.Remove
