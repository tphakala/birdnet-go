package diskmanager

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInvalidFileNameErrorMessages tests that the error messages for invalid file names are detailed
func TestInvalidFileNameErrorMessages(t *testing.T) {
	// Test cases with invalid file names
	testCases := []struct {
		filename        string
		expectedErrText string
	}{
		// Too few parts
		{"bubo_bubo.wav", "diskmanager: invalid audio filename format 'bubo_bubo.wav' (has 2 parts, expected at least 3)"},
		// This actually gets parsed as species="bubo", confidence="bubo_80p", which fails at the confidence parsing step
		{"bubo_bubo_80p.wav", "strconv.Atoi: parsing \"bubo\": invalid syntax"},

		// Invalid confidence value
		{"bubo_bubo_XXp_20210102T150405Z.wav", "strconv.Atoi: parsing \"XX\": invalid syntax"},

		// Invalid timestamp format
		{"bubo_bubo_80p_invalid.wav", "parsing time \"invalid\" as \"20060102T150405Z\": cannot parse \"invalid\" as \"2006\""},
	}

	for _, tc := range testCases {
		t.Run(tc.filename, func(t *testing.T) {
			// Create a mock FileInfo
			mockInfo := &MockFileInfo{
				FileName:    tc.filename,
				FileSize:    1024,
				FileMode:    0o644,
				FileModTime: parseTime("20210102T150405Z"),
				FileIsDir:   false,
			}

			// Call parseFileInfo and check the error message
			_, err := parseFileInfo("/test/"+tc.filename, mockInfo)
			require.Error(t, err, "Should return an error for invalid file name")
			assert.Contains(t, err.Error(), tc.expectedErrText, "Error message should contain expected text")
		})
	}
}

// TestGetAudioFilesContinuesOnError tests that GetAudioFiles continues processing after encountering invalid files
func TestGetAudioFilesContinuesOnError(t *testing.T) {
	// Create a temporary directory
	tempDir := t.TempDir()

	// Create some valid and invalid files
	validFile := tempDir + "/bubo_bubo_80p_20210102T150405Z.wav"
	invalidFile := tempDir + "/invalid_file.wav"

	// Create the files
	err := os.WriteFile(validFile, []byte("test content"), 0o644) //nolint:gosec // G306: Test files don't require restrictive permissions
	require.NoError(t, err, "Should be able to create valid file")

	err = os.WriteFile(invalidFile, []byte("test content"), 0o644) //nolint:gosec // G306: Test files don't require restrictive permissions
	require.NoError(t, err, "Should be able to create invalid file")

	// Create a mock DB
	mockDB := &MockDB{}

	// Call GetAudioFiles with debug enabled to see the error messages
	files, err := GetAudioFiles(tempDir, allowedFileTypes, mockDB, true)

	// Should not return an error as long as at least one file is valid
	require.NoError(t, err, "Should not return an error when at least one file is valid")
	assert.Len(t, files, 1, "Should return one valid file")
	assert.Equal(t, "bubo_bubo", files[0].Species, "Should correctly parse the valid file")
}

// TestGetAudioFilesWithMixedFiles tests that GetAudioFiles correctly processes a mix of valid and invalid files
func TestGetAudioFilesWithMixedFiles(t *testing.T) {
	// Create a temporary directory
	tempDir := t.TempDir()

	// Create a mix of valid and invalid files
	validFiles := []string{
		tempDir + "/bubo_bubo_80p_20210102T150405Z.wav",
		tempDir + "/anas_platyrhynchos_70p_20210103T150405Z.mp3",
		tempDir + "/erithacus_rubecula_60p_20210104T150405Z.flac",
	}

	invalidFiles := []string{
		tempDir + "/invalid_file.wav",                   // Invalid format
		tempDir + "/bubo_bubo_XXp_20210102T150405Z.wav", // Invalid confidence
		tempDir + "/bubo_bubo_80p_invalid.wav",          // Invalid timestamp
	}

	// Create all the files
	for _, file := range append(validFiles, invalidFiles...) {
		err := os.WriteFile(file, []byte("test content"), 0o644) //nolint:gosec // G306: Test files don't require restrictive permissions
		require.NoError(t, err, "Should be able to create file: %s", file)
	}

	// Create a mock DB
	mockDB := &MockDB{}

	// Call GetAudioFiles with debug enabled to see the error messages
	files, err := GetAudioFiles(tempDir, allowedFileTypes, mockDB, true)

	// Should not return an error as long as at least one file is valid
	require.NoError(t, err, "Should not return an error when at least one file is valid")
	assert.Len(t, files, len(validFiles), "Should return only the valid files")

	// Verify that all valid files were processed correctly
	processedPaths := make(map[string]bool)
	for _, file := range files {
		processedPaths[file.Path] = true
	}

	for _, validFile := range validFiles {
		assert.True(t, processedPaths[validFile], "Valid file should be processed: %s", validFile)
	}

	// Now test with all invalid files
	invalidOnlyDir := t.TempDir()
	for _, file := range invalidFiles {
		baseName := filepath.Base(file)
		newPath := filepath.Join(invalidOnlyDir, baseName)
		err := os.WriteFile(newPath, []byte("test content"), 0o644) //nolint:gosec // G306: Test files don't require restrictive permissions
		require.NoError(t, err, "Should be able to create file: %s", newPath)
	}

	// Call GetAudioFiles with all invalid files
	files, err = GetAudioFiles(invalidOnlyDir, allowedFileTypes, mockDB, true)

	// Should return an error when all files are invalid
	require.Error(t, err, "Should return an error when all files are invalid")
	assert.Contains(t, err.Error(), "diskmanager: failed to parse any audio files", "Error should indicate no valid files were found")
	assert.Empty(t, files, "Should return no files when all are invalid")
}

// TestGetDiskUsage tests that GetDiskUsage returns a valid disk usage percentage
func TestGetDiskUsage(t *testing.T) {
	// Create a temporary directory
	tempDir := t.TempDir()

	// Call GetDiskUsage
	usage, err := GetDiskUsage(tempDir)

	// Verify the return values
	require.NoError(t, err, "GetDiskUsage should not return an error")
	assert.GreaterOrEqual(t, usage, 0.0, "Disk usage should be greater than or equal to 0%")
	assert.LessOrEqual(t, usage, 100.0, "Disk usage should be less than or equal to 100%")
}

// TestCleanupReturnValues tests that cleanup functions return the expected values
func TestCleanupReturnValues(t *testing.T) {
	// Create a mock DB
	mockDB := &MockDB{}

	// Create a quit channel
	quitChan := make(chan struct{})

	// Test AgeBasedCleanup
	t.Run("AgeBasedCleanup", func(t *testing.T) {
		// Call AgeBasedCleanup
		result := AgeBasedCleanup(quitChan, mockDB)

		// Verify the return values
		if result.Err != nil {
			t.Logf("AgeBasedCleanup returned error: %v", result.Err)
		}

		// Verify that clipsRemoved is a valid count (zero or positive)
		assert.GreaterOrEqual(t, result.ClipsRemoved, 0, "AgeBasedCleanup should return a valid clips removed count")

		// Verify that diskUtilization is a valid percentage (0-100)
		assert.GreaterOrEqual(t, result.DiskUtilization, 0, "Disk utilization should be greater than or equal to 0%")
		assert.LessOrEqual(t, result.DiskUtilization, 100, "Disk utilization should be less than or equal to 100%")
	})

	// Test UsageBasedCleanup
	t.Run("UsageBasedCleanup", func(t *testing.T) {
		// Call UsageBasedCleanup
		result := UsageBasedCleanup(quitChan, mockDB)

		// Verify the return values
		if result.Err != nil {
			t.Logf("UsageBasedCleanup returned error: %v", result.Err)
		}

		// Verify that clipsRemoved is a valid count (zero or positive)
		assert.GreaterOrEqual(t, result.ClipsRemoved, 0, "UsageBasedCleanup should return a valid clips removed count")

		// Verify that diskUtilization is a valid percentage (0-100)
		assert.GreaterOrEqual(t, result.DiskUtilization, 0, "Disk utilization should be greater than or equal to 0%")
		assert.LessOrEqual(t, result.DiskUtilization, 100, "Disk utilization should be less than or equal to 100%")
	})
}

// TestGetAudioFilesIgnoresTempFiles tests that GetAudioFiles correctly ignores .temp files
func TestGetAudioFilesIgnoresTempFiles(t *testing.T) {
	// Create a temporary directory
	tempDir := t.TempDir()

	// Create valid audio files
	validFiles := []string{
		"bubo_bubo_80p_20210102T150405Z.wav",
		"anas_platyrhynchos_70p_20210103T150405Z.mp3",
	}

	// Create temp files that should be ignored
	tempFiles := []string{
		"bubo_bubo_85p_20210102T150405Z.wav.temp",    // Lowercase .temp
		"corvus_corax_90p_20210104T150405Z.wav.TEMP", // Uppercase .TEMP
		"parus_major_75p_20210105T150405Z.wav.Temp",  // Mixed case .Temp
	}

	// Create valid files
	for _, file := range validFiles {
		filePath := filepath.Join(tempDir, file)
		err := os.WriteFile(filePath, []byte("test content"), 0o644)
		require.NoError(t, err, "Should be able to create valid file: %s", filePath)
	}

	// Create temp files
	for _, file := range tempFiles {
		filePath := filepath.Join(tempDir, file)
		err := os.WriteFile(filePath, []byte("test content"), 0o644)
		require.NoError(t, err, "Should be able to create temp file: %s", filePath)
	}

	// Create a mock DB
	mockDB := &MockDB{}

	// Call GetAudioFiles
	files, err := GetAudioFiles(tempDir, allowedFileTypes, mockDB, false)

	// Should not return an error
	require.NoError(t, err, "Should not return an error")

	// Should return only the valid files, not the temp files
	assert.Len(t, files, len(validFiles), "Should return only valid files, ignoring .temp files")

	// Verify the correct files were processed
	processedSpecies := make(map[string]bool)
	for _, file := range files {
		processedSpecies[file.Species] = true
	}

	// Check that only valid files were processed
	assert.True(t, processedSpecies["bubo_bubo"], "Should process bubo_bubo")
	assert.True(t, processedSpecies["anas_platyrhynchos"], "Should process anas_platyrhynchos")

	// Check that temp files were not processed
	assert.False(t, processedSpecies["corvus_corax"], "Should not process corvus_corax from temp file")
	assert.False(t, processedSpecies["parus_major"], "Should not process parus_major from temp file")
}
