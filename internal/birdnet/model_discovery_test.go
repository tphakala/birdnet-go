package birdnet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withTempWorkDir creates a temporary directory, changes to it, runs the function,
// and ensures we return to the original directory afterwards.
func withTempWorkDir(t *testing.T, fn func(tempDir string)) {
	t.Helper()
	
	tempDir := t.TempDir()
	originalWd, err := os.Getwd()
	require.NoError(t, err, "Failed to get working directory")
	
	// Register cleanup to restore working directory
	t.Cleanup(func() {
		assert.NoError(t, os.Chdir(originalWd), "Failed to restore working directory")
	})
	
	require.NoError(t, os.Chdir(tempDir), "Failed to change to temp directory")
	
	// Run the test function
	fn(tempDir)
}

func TestTryLoadModelFromStandardPaths_RelativeDirectory(t *testing.T) {
	// Note: Cannot run in parallel due to os.Chdir() usage affecting global state
	
	const testModelName = "test_model.tflite"
	const testContent = "mock model data"
	
	withTempWorkDir(t, func(tempDir string) {
		// Create test file in relative model directory
		modelPath := filepath.Join(DefaultModelDirectory, testModelName)
		require.NoError(t, os.MkdirAll(DefaultModelDirectory, 0o750), "Failed to create model directory")
		require.NoError(t, os.WriteFile(modelPath, []byte(testContent), 0o600), "Failed to create test file")

		data, path, err := tryLoadModelFromStandardPaths(testModelName, "test")

		require.NoError(t, err, "Expected model to be found")
		require.True(t, strings.HasSuffix(path, modelPath), 
			"Expected path to end with %s, got %s", modelPath, path)
		require.Equal(t, testContent, string(data), "Expected correct file content")
	})
}

func TestTryLoadModelFromStandardPaths_LegacyDataDirectory(t *testing.T) {
	// Note: Cannot run in parallel due to os.Chdir() usage affecting global state
	
	const testModelName = "test_model.tflite"
	const testContent = "mock model data"
	
	withTempWorkDir(t, func(tempDir string) {
		// Create test file in legacy data/model directory  
		modelPath := filepath.Join("data", DefaultModelDirectory, testModelName)
		require.NoError(t, os.MkdirAll(filepath.Join("data", DefaultModelDirectory), 0o750), 
			"Failed to create model directory")
		require.NoError(t, os.WriteFile(modelPath, []byte(testContent), 0o600), 
			"Failed to create test file")

		data, path, err := tryLoadModelFromStandardPaths(testModelName, "test")

		require.NoError(t, err, "Expected model to be found")
		require.True(t, strings.HasSuffix(path, modelPath),
			"Expected path to end with %s, got %s", modelPath, path)
		require.Equal(t, testContent, string(data), "Expected correct file content")
	})
}

func TestTryLoadModelFromStandardPaths_FirstHitWins(t *testing.T) {
	// Note: Cannot run in parallel due to os.Chdir() usage affecting global state
	
	const testModelName = "test_model.tflite"
	
	withTempWorkDir(t, func(tempDir string) {
		// Create test files in multiple locations
		firstPath := filepath.Join(DefaultModelDirectory, testModelName)
		secondPath := filepath.Join("data", DefaultModelDirectory, testModelName)

		// Create first priority file
		require.NoError(t, os.MkdirAll(DefaultModelDirectory, 0o750), "Failed to create first directory")
		require.NoError(t, os.WriteFile(firstPath, []byte("first_priority"), 0o600), "Failed to create first file")

		// Create second priority file
		require.NoError(t, os.MkdirAll(filepath.Join("data", DefaultModelDirectory), 0o750), 
			"Failed to create second directory")
		require.NoError(t, os.WriteFile(secondPath, []byte("second_priority"), 0o600), 
			"Failed to create second file")

		data, path, err := tryLoadModelFromStandardPaths(testModelName, "test")

		require.NoError(t, err, "Expected model to be found")
		// Should find first priority
		require.True(t, strings.HasSuffix(path, firstPath),
			"Expected first priority path ending with %s, got %s", firstPath, path)
		require.Equal(t, "first_priority", string(data), "Expected first priority data")
	})
}

func TestTryLoadModelFromStandardPaths_ModelNotFound(t *testing.T) {
	// Note: Cannot run in parallel due to os.Chdir() usage affecting global state
	
	withTempWorkDir(t, func(tempDir string) {
		data, path, err := tryLoadModelFromStandardPaths("nonexistent.tflite", "test")

		require.Error(t, err, "Expected error when model not found")
		require.Nil(t, data, "Expected nil data when not found")
		require.Empty(t, path, "Expected empty path when not found")
		
		// Verify error contains attempted paths context
		require.Contains(t, err.Error(), "not found in standard paths", "Error should mention standard paths")
		require.Contains(t, err.Error(), "nonexistent.tflite", "Error should mention the file name")
	})
}

func TestTryLoadModelFromStandardPaths_RangeFilterModels(t *testing.T) {
	// Note: Cannot run in parallel due to os.Chdir() usage affecting global state

	const testContent = "range filter model data"

	// Test both range filter model types
	testCases := []struct {
		name      string
		modelName string
	}{
		{"range filter v1", DefaultRangeFilterV1ModelName},
		{"range filter v2", DefaultRangeFilterV2ModelName},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			withTempWorkDir(t, func(tempDir string) {
				// Create test file
				modelPath := filepath.Join(DefaultModelDirectory, tc.modelName)
				require.NoError(t, os.MkdirAll(DefaultModelDirectory, 0o750), "Failed to create model directory")
				require.NoError(t, os.WriteFile(modelPath, []byte(testContent), 0o600), "Failed to create test file")

				data, path, err := tryLoadModelFromStandardPaths(tc.modelName, "test")

				require.NoError(t, err, "Expected model to be found")
				require.True(t, strings.HasSuffix(path, modelPath),
					"Expected path to end with %s, got %s", modelPath, path)
				require.Equal(t, testContent, string(data), "Expected correct file content")
			})
		})
	}
}

func TestTryLoadModelFromStandardPaths_AttemptedPaths(t *testing.T) {
	// Note: Cannot run in parallel due to os.Chdir() usage affecting global state

	const testModelName = "nonexistent.tflite"
	
	withTempWorkDir(t, func(tempDir string) {
		data, path, err := tryLoadModelFromStandardPaths(testModelName, "test")

		require.Error(t, err, "Expected error when model not found")
		require.Nil(t, data, "Expected nil data")
		require.Empty(t, path, "Expected empty path")

		// Error should contain information about attempted paths
		errStr := err.Error()
		require.Contains(t, errStr, "not found in standard paths", "Error should mention standard paths")
		require.Contains(t, errStr, testModelName, "Error should mention the model name")
		
		// The error context would include attempted paths, but we'd need to parse the error
		// to verify. For now, we can at least verify the error mentions the failure.
		require.Contains(t, errStr, "test model", "Error should mention model type")
	})
}