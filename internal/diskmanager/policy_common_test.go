package diskmanager

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// MockSettings creates a minimal settings struct for testing
func createMockSettings(keepSpectrograms bool) *conf.Settings {
	return &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				Export: conf.ExportSettings{
					Retention: conf.RetentionSettings{
						KeepSpectrograms: keepSpectrograms,
					},
				},
			},
		},
	}
}

// TestBuildSpeciesCountMap tests that the map of species counts per directory is built correctly
func TestBuildSpeciesCountMap(t *testing.T) {
	// Create test files with different species in different directories
	files := []FileInfo{
		{
			Path:    "/test/dir1/bubo_bubo_80p_20210101T150405Z.wav",
			Species: "bubo_bubo",
		},
		{
			Path:    "/test/dir1/bubo_bubo_90p_20210102T150405Z.wav",
			Species: "bubo_bubo",
		},
		{
			Path:    "/test/dir2/bubo_bubo_95p_20210103T150405Z.wav",
			Species: "bubo_bubo",
		},
		{
			Path:    "/test/dir1/anas_platyrhynchos_80p_20210104T150405Z.wav",
			Species: "anas_platyrhynchos",
		},
	}

	// Build the map
	speciesDirCount := buildSpeciesCountMap(files)

	// Verify the map structure and counts
	assert.Equal(t, 2, len(speciesDirCount), "Map should have 2 species")
	assert.Contains(t, speciesDirCount, "bubo_bubo", "Map should contain bubo_bubo")
	assert.Contains(t, speciesDirCount, "anas_platyrhynchos", "Map should contain anas_platyrhynchos")

	// Check the directory counts for bubo_bubo
	assert.Equal(t, 2, speciesDirCount["bubo_bubo"]["/test/dir1"], "bubo_bubo should have 2 files in dir1")
	assert.Equal(t, 1, speciesDirCount["bubo_bubo"]["/test/dir2"], "bubo_bubo should have 1 file in dir2")

	// Check the directory count for anas_platyrhynchos
	assert.Equal(t, 1, speciesDirCount["anas_platyrhynchos"]["/test/dir1"], "anas_platyrhynchos should have 1 file in dir1")
}

// TestBuildGlobalSpeciesCountMap tests that the global species count map is built correctly
func TestBuildGlobalSpeciesCountMap(t *testing.T) {
	// Create test files with different species
	files := []FileInfo{
		{
			Path:    "/test/dir1/bubo_bubo_80p_20210101T150405Z.wav",
			Species: "bubo_bubo",
		},
		{
			Path:    "/test/dir1/bubo_bubo_90p_20210102T150405Z.wav",
			Species: "bubo_bubo",
		},
		{
			Path:    "/test/dir2/bubo_bubo_95p_20210103T150405Z.wav",
			Species: "bubo_bubo",
		},
		{
			Path:    "/test/dir1/anas_platyrhynchos_80p_20210104T150405Z.wav",
			Species: "anas_platyrhynchos",
		},
	}

	// Build the map
	globalSpeciesCount := buildGlobalSpeciesCountMap(files)

	// Verify the map structure and counts
	assert.Equal(t, 2, len(globalSpeciesCount), "Map should have 2 species")
	assert.Equal(t, 3, globalSpeciesCount["bubo_bubo"], "bubo_bubo should have 3 files total")
	assert.Equal(t, 1, globalSpeciesCount["anas_platyrhynchos"], "anas_platyrhynchos should have 1 file total")
}

// TestCanDeleteBasedOnMinClips tests the directory-specific min clips check
func TestCanDeleteBasedOnMinClips(t *testing.T) {
	// Create a species count map
	speciesDirCount := map[string]map[string]int{
		"bubo_bubo": {
			"/test/dir1": 3,
			"/test/dir2": 1,
		},
		"anas_platyrhynchos": {
			"/test/dir1": 2,
		},
	}

	// Test cases
	tests := []struct {
		name               string
		file               FileInfo
		parentDir          string
		minClipsPerSpecies int
		expectCanDelete    bool
	}{
		{
			name: "Species above min clips in directory",
			file: FileInfo{
				Path:    "/test/dir1/bubo_bubo_80p_20210101T150405Z.wav",
				Species: "bubo_bubo",
			},
			parentDir:          "/test/dir1",
			minClipsPerSpecies: 2,
			expectCanDelete:    true,
		},
		{
			name: "Species at min clips in directory",
			file: FileInfo{
				Path:    "/test/dir1/anas_platyrhynchos_80p_20210101T150405Z.wav",
				Species: "anas_platyrhynchos",
			},
			parentDir:          "/test/dir1",
			minClipsPerSpecies: 2,
			expectCanDelete:    false,
		},
		{
			name: "Species below min clips in directory",
			file: FileInfo{
				Path:    "/test/dir2/bubo_bubo_80p_20210101T150405Z.wav",
				Species: "bubo_bubo",
			},
			parentDir:          "/test/dir2",
			minClipsPerSpecies: 2,
			expectCanDelete:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := canDeleteBasedOnMinClips(&tt.file, tt.parentDir, speciesDirCount, tt.minClipsPerSpecies, true)
			assert.Equal(t, tt.expectCanDelete, result)
		})
	}
}

// TestCanDeleteBasedOnGlobalMinClips tests the global min clips check
func TestCanDeleteBasedOnGlobalMinClips(t *testing.T) {
	// Create a global species count map
	globalSpeciesCount := map[string]int{
		"bubo_bubo":          3,
		"anas_platyrhynchos": 1,
	}

	// Test cases
	tests := []struct {
		name               string
		file               FileInfo
		minClipsPerSpecies int
		expectCanDelete    bool
	}{
		{
			name: "Species above min clips globally",
			file: FileInfo{
				Path:    "/test/dir1/bubo_bubo_80p_20210101T150405Z.wav",
				Species: "bubo_bubo",
			},
			minClipsPerSpecies: 2,
			expectCanDelete:    true,
		},
		{
			name: "Species at min clips globally",
			file: FileInfo{
				Path:    "/test/dir1/bubo_bubo_80p_20210101T150405Z.wav",
				Species: "bubo_bubo",
			},
			minClipsPerSpecies: 3,
			expectCanDelete:    false,
		},
		{
			name: "Species below min clips globally",
			file: FileInfo{
				Path:    "/test/dir1/anas_platyrhynchos_80p_20210101T150405Z.wav",
				Species: "anas_platyrhynchos",
			},
			minClipsPerSpecies: 2,
			expectCanDelete:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := canDeleteBasedOnGlobalMinClips(&tt.file, globalSpeciesCount, tt.minClipsPerSpecies, true)
			assert.Equal(t, tt.expectCanDelete, result)
		})
	}
}

// Test the deleteFile function for both audio and spectrogram files
func TestDeleteFile(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "delete_file_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create test cases with both keepSpectrograms true and false
	testCases := []struct {
		name             string
		keepSpectrograms bool
		expectPngDeleted bool
	}{
		{
			name:             "Delete audio and spectrogram when KeepSpectrograms is false",
			keepSpectrograms: false,
			expectPngDeleted: true,
		},
		{
			name:             "Delete only audio when KeepSpectrograms is true",
			keepSpectrograms: true,
			expectPngDeleted: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create test files
			audioFilename := filepath.Join(tempDir, "bubo_bubo_80p_20210101T150405Z.wav")
			pngFilename := filepath.Join(tempDir, "bubo_bubo_80p_20210101T150405Z.png")

			err = os.WriteFile(audioFilename, []byte("test audio content"), 0o644)
			require.NoError(t, err)

			err = os.WriteFile(pngFilename, []byte("test png content"), 0o644)
			require.NoError(t, err)

			// Verify files were created
			_, err = os.Stat(audioFilename)
			require.NoError(t, err)
			_, err = os.Stat(pngFilename)
			require.NoError(t, err)

			// Create file info and test params
			file := &FileInfo{
				Path:    audioFilename,
				Species: "bubo_bubo",
				Size:    16, // Size of test content
			}

			params := &CleanupParameters{
				Debug:    true,
				Settings: createMockSettings(tc.keepSpectrograms),
			}

			// Call deleteFile
			err = deleteFile(file, params)
			require.NoError(t, err)

			// Verify audio file was deleted
			_, err = os.Stat(audioFilename)
			require.True(t, os.IsNotExist(err), "Audio file should be deleted")

			// Check PNG file based on keepSpectrograms setting
			_, err = os.Stat(pngFilename)
			if tc.expectPngDeleted {
				require.True(t, os.IsNotExist(err), "PNG file should be deleted when keepSpectrograms is false")
			} else {
				require.NoError(t, err, "PNG file should exist when keepSpectrograms is true")
			}
		})
	}
}

// TestPerformCleanupLoop tests the main cleanup loop logic
func TestPerformCleanupLoop(t *testing.T) {
	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "cleanup_loop_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create some test files
	createTestFile := func(path, content string) {
		err := os.MkdirAll(filepath.Dir(path), 0o755)
		require.NoError(t, err)
		err = os.WriteFile(path, []byte(content), 0o644)
		require.NoError(t, err)
	}

	// Test files
	audioFile1 := filepath.Join(tempDir, "species1", "species1_80p_20210101T150405Z.wav")
	pngFile1 := filepath.Join(tempDir, "species1", "species1_80p_20210101T150405Z.png")
	audioFile2 := filepath.Join(tempDir, "species1", "species1_90p_20210102T150405Z.wav")
	pngFile2 := filepath.Join(tempDir, "species1", "species1_90p_20210102T150405Z.png")
	audioFile3 := filepath.Join(tempDir, "species2", "species2_85p_20210103T150405Z.wav")
	pngFile3 := filepath.Join(tempDir, "species2", "species2_85p_20210103T150405Z.png")

	createTestFile(audioFile1, "test audio content 1")
	createTestFile(pngFile1, "test png content 1")
	createTestFile(audioFile2, "test audio content 2")
	createTestFile(pngFile2, "test png content 2")
	createTestFile(audioFile3, "test audio content 3")
	createTestFile(pngFile3, "test png content 3")

	// Create file info objects
	fileInfo1 := FileInfo{
		Path:       audioFile1,
		Species:    "species1",
		Confidence: 80,
		Timestamp:  time.Now().Add(-24 * time.Hour),
		Size:       19, // Length of content
		Locked:     false,
	}
	fileInfo2 := FileInfo{
		Path:       audioFile2,
		Species:    "species1",
		Confidence: 90,
		Timestamp:  time.Now().Add(-48 * time.Hour),
		Size:       19,
		Locked:     false,
	}
	fileInfo3 := FileInfo{
		Path:       audioFile3,
		Species:    "species2",
		Confidence: 85,
		Timestamp:  time.Now().Add(-72 * time.Hour),
		Size:       19,
		Locked:     false,
	}

	// Create mock settings
	settings := createMockSettings(false) // Don't keep spectrograms

	// Create test parameters
	params := &CleanupParameters{
		BaseDir:            tempDir,
		MinClipsPerSpecies: 1,
		Debug:              true,
		QuitChan:           make(<-chan struct{}), // Empty channel for testing
		DB:                 &MockDB{},
		AllowedFileTypes:   []string{".wav", ".mp3", ".flac"},
		Settings:           settings,
		CheckUsageInLoop:   false,
	}

	// Create a policy function that always returns true for deletion
	alwaysDeleteFunc := func(file *FileInfo, params *CleanupParameters, currentDiskUsage float64) (bool, error) {
		return true, nil
	}

	// Build count maps
	files := []FileInfo{fileInfo1, fileInfo2, fileInfo3}
	speciesDirCount := buildSpeciesCountMap(files)
	globalSpeciesCount := buildGlobalSpeciesCountMap(files)

	// Execute the cleanup loop
	deletedCount, err := performCleanupLoop(
		files,
		params,
		speciesDirCount,
		globalSpeciesCount,
		alwaysDeleteFunc,
	)

	// Verify results
	require.NoError(t, err)
	assert.Equal(t, 2, deletedCount, "Should delete 2 files (respect min clips constraint)")

	// Check which files exist after cleanup
	// Due to minimum clips constraint, one file of each species should remain
	_, err = os.Stat(audioFile1)
	_, err2 := os.Stat(audioFile2)
	species1Exists := !os.IsNotExist(err) || !os.IsNotExist(err2)
	assert.True(t, species1Exists, "One species1 file should still exist")

	_, err = os.Stat(audioFile3)
	species2Exists := !os.IsNotExist(err)
	assert.True(t, species2Exists, "One species2 file should still exist")

	// Check that PNG files were also deleted
	pngFilesExist := 0
	for _, path := range []string{pngFile1, pngFile2, pngFile3} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			pngFilesExist++
		}
	}
	assert.Equal(t, 1, pngFilesExist, "Should be only 1 PNG file remaining")

	// Now test with keepSpectrograms=true
	// Reset the test environment
	os.RemoveAll(tempDir)
	createTestFile(audioFile1, "test audio content 1")
	createTestFile(pngFile1, "test png content 1")
	createTestFile(audioFile2, "test audio content 2")
	createTestFile(pngFile2, "test png content 2")
	createTestFile(audioFile3, "test audio content 3")
	createTestFile(pngFile3, "test png content 3")

	// Update settings to keep spectrograms
	params.Settings = createMockSettings(true) // Keep spectrograms

	// Execute the cleanup loop again
	deletedCount, err = performCleanupLoop(
		files,
		params,
		speciesDirCount,
		globalSpeciesCount,
		alwaysDeleteFunc,
	)

	// Verify results
	require.NoError(t, err)
	assert.Equal(t, 2, deletedCount, "Should delete 2 files (respect min clips constraint)")

	// Check PNG files - all should still exist
	pngFilesExist = 0
	for _, path := range []string{pngFile1, pngFile2, pngFile3} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			pngFilesExist++
		}
	}
	assert.Equal(t, 3, pngFilesExist, "All 3 PNG files should remain when keepSpectrograms=true")
}

// Test the quit signal handling in the cleanup loop
func TestPerformCleanupLoopWithQuit(t *testing.T) {
	// Create a quit channel
	quitChan := make(chan struct{})

	// Create test files
	files := []FileInfo{
		{
			Path:       "/test/bubo_bubo_80p_20210101T150405Z.wav",
			Species:    "bubo_bubo",
			Confidence: 80,
			Timestamp:  time.Now(),
			Size:       1024,
			Locked:     false,
		},
		{
			Path:       "/test/bubo_bubo_90p_20210102T150405Z.wav",
			Species:    "bubo_bubo",
			Confidence: 90,
			Timestamp:  time.Now(),
			Size:       1024,
			Locked:     false,
		},
	}

	// Create cleanup parameters
	params := &CleanupParameters{
		BaseDir:            "/test",
		MinClipsPerSpecies: 1,
		Debug:              true,
		QuitChan:           quitChan,
		DB:                 &MockDB{},
		AllowedFileTypes:   []string{".wav"},
		Settings:           createMockSettings(false),
	}

	// A slow deletion function that will be interrupted
	slowDeleteFunc := func(file *FileInfo, params *CleanupParameters, currentDiskUsage float64) (bool, error) {
		// Simulate a slow operation that can be interrupted
		select {
		case <-time.After(50 * time.Millisecond):
			return true, nil
		default:
			return true, nil
		}
	}

	// Send quit signal after a short delay
	go func() {
		time.Sleep(10 * time.Millisecond)
		close(quitChan)
	}()

	// Execute cleanup loop with quit signal
	deletedCount, err := performCleanupLoop(
		files,
		params,
		nil,
		buildGlobalSpeciesCountMap(files),
		slowDeleteFunc,
	)

	// Verify the loop was interrupted gracefully
	assert.NoError(t, err, "Should exit gracefully on quit signal")
	assert.True(t, deletedCount < len(files), "Should not process all files due to quit signal")
}

// Test that errors in the deletion process are handled correctly
func TestPerformCleanupLoopWithErrors(t *testing.T) {
	// Create test files
	files := []FileInfo{}

	// Add several files to test with
	for i := 0; i < 15; i++ {
		files = append(files, FileInfo{
			Path:       fmt.Sprintf("/test/species_%d.wav", i),
			Species:    fmt.Sprintf("species_%d", i%3), // Create a few different species
			Confidence: 80,
			Timestamp:  time.Now(),
			Size:       1024,
			Locked:     false,
		})
	}

	// Create cleanup parameters
	params := &CleanupParameters{
		BaseDir:            "/test",
		MinClipsPerSpecies: 1,
		Debug:              true,
		QuitChan:           make(<-chan struct{}),
		DB:                 &MockDB{},
		AllowedFileTypes:   []string{".wav"},
		Settings:           createMockSettings(false),
	}

	// Override the osRemove function to simulate errors
	originalOsRemove := osRemove
	defer func() { osRemove = originalOsRemove }()

	errorCount := 0
	osRemove = func(name string) error {
		errorCount++
		if errorCount <= 10 { // First 10 calls will fail
			return fmt.Errorf("simulated error for file %s", name)
		}
		return nil // Subsequent calls succeed
	}

	// Always want to delete function
	alwaysDeleteFunc := func(file *FileInfo, params *CleanupParameters, currentDiskUsage float64) (bool, error) {
		return true, nil
	}

	// Execute cleanup loop
	_, err := performCleanupLoop(
		files,
		params,
		nil,
		buildGlobalSpeciesCountMap(files),
		alwaysDeleteFunc,
	)

	// Verify the loop stops after max errors
	assert.Error(t, err, "Should return error after exceeding max error count")
	assert.Contains(t, err.Error(), "too many errors", "Error message should indicate too many errors")
}
