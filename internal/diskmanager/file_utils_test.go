package diskmanager

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestInvalidFileNameErrorMessages tests that the error messages for invalid file names are detailed
func TestInvalidFileNameErrorMessages(t *testing.T) {
	// Test cases with invalid file names
	testCases := []struct {
		filename        string
		expectedErrText string
	}{
		// Too few parts
		{"bubo_bubo.wav", "invalid file name format: bubo_bubo.wav (has 2 parts, expected at least 3)"},
		// This actually gets parsed as species="bubo", confidence="bubo_80p", which fails at the confidence parsing step
		{"bubo_bubo_80p.wav", "invalid confidence value in file bubo_bubo_80p.wav"},

		// Invalid confidence value
		{"bubo_bubo_XXp_20210102T150405Z.wav", "invalid confidence value in file bubo_bubo_XXp_20210102T150405Z.wav"},

		// Invalid timestamp format
		{"bubo_bubo_80p_invalid.wav", "invalid timestamp format in file bubo_bubo_80p_invalid.wav"},
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
			assert.Error(t, err, "Should return an error for invalid file name")
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
	err := os.WriteFile(validFile, []byte("test content"), 0o644)
	assert.NoError(t, err, "Should be able to create valid file")

	err = os.WriteFile(invalidFile, []byte("test content"), 0o644)
	assert.NoError(t, err, "Should be able to create invalid file")

	// Create a mock DB
	mockDB := &MockDB{}

	// Call GetAudioFiles with debug enabled to see the error messages
	files, err := GetAudioFiles(tempDir, allowedFileTypes, mockDB, true)

	// Should not return an error as long as at least one file is valid
	assert.NoError(t, err, "Should not return an error when at least one file is valid")
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
		err := os.WriteFile(file, []byte("test content"), 0o644)
		assert.NoError(t, err, "Should be able to create file: %s", file)
	}

	// Create a mock DB
	mockDB := &MockDB{}

	// Call GetAudioFiles with debug enabled to see the error messages
	files, err := GetAudioFiles(tempDir, allowedFileTypes, mockDB, true)

	// Should not return an error as long as at least one file is valid
	assert.NoError(t, err, "Should not return an error when at least one file is valid")
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
		err := os.WriteFile(newPath, []byte("test content"), 0o644)
		assert.NoError(t, err, "Should be able to create file: %s", newPath)
	}

	// Call GetAudioFiles with all invalid files
	files, err = GetAudioFiles(invalidOnlyDir, allowedFileTypes, mockDB, true)

	// Should return an error when all files are invalid
	assert.Error(t, err, "Should return an error when all files are invalid")
	assert.Contains(t, err.Error(), "failed to parse any files", "Error should indicate no valid files were found")
	assert.Len(t, files, 0, "Should return no files when all are invalid")
}
