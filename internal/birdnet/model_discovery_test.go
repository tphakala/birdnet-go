package birdnet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTryLoadModelFromStandardPaths_RelativeDirectory(t *testing.T) {
	// Note: Cannot run in parallel due to os.Chdir() usage affecting global state
	
	const testModelName = "test_model.tflite"
	const testContent = "mock model data"
	
	tempDir := t.TempDir()
	originalWd, err := os.Getwd()
	require.NoError(t, err, "Failed to get working directory")
	
	defer func() {
		if err := os.Chdir(originalWd); err != nil {
			t.Errorf("Failed to restore working directory: %v", err)
		}
	}()
	
	require.NoError(t, os.Chdir(tempDir), "Failed to change to temp directory")

	// Create test file in relative model directory
	modelPath := filepath.Join(DefaultModelDirectory, testModelName)
	require.NoError(t, os.MkdirAll(DefaultModelDirectory, 0o755), "Failed to create model directory")
	require.NoError(t, os.WriteFile(modelPath, []byte(testContent), 0o644), "Failed to create test file")

	result := tryLoadModelFromStandardPaths(testModelName)

	require.True(t, result.Found, "Expected model to be found")
	require.True(t, strings.HasSuffix(result.Path, modelPath), 
		"Expected path to end with %s, got %s", modelPath, result.Path)
	require.Equal(t, testContent, string(result.Data), "Expected correct file content")
	require.NotEmpty(t, result.AttemptedPaths, "Expected AttemptedPaths to be populated")
}

func TestTryLoadModelFromStandardPaths_LegacyDataDirectory(t *testing.T) {
	// Note: Cannot run in parallel due to os.Chdir() usage affecting global state
	
	const testModelName = "test_model.tflite"
	const testContent = "mock model data"
	
	tempDir := t.TempDir()
	originalWd, err := os.Getwd()
	require.NoError(t, err, "Failed to get working directory")
	
	defer func() {
		if err := os.Chdir(originalWd); err != nil {
			t.Errorf("Failed to restore working directory: %v", err)
		}
	}()
	
	require.NoError(t, os.Chdir(tempDir), "Failed to change to temp directory")

	// Create test file in legacy data/model directory  
	modelPath := filepath.Join("data", DefaultModelDirectory, testModelName)
	require.NoError(t, os.MkdirAll(filepath.Join("data", DefaultModelDirectory), 0o755), 
		"Failed to create model directory")
	require.NoError(t, os.WriteFile(modelPath, []byte(testContent), 0o644), 
		"Failed to create test file")

	result := tryLoadModelFromStandardPaths(testModelName)

	require.True(t, result.Found, "Expected model to be found")
	require.True(t, strings.HasSuffix(result.Path, modelPath),
		"Expected path to end with %s, got %s", modelPath, result.Path)
	require.Equal(t, testContent, string(result.Data), "Expected correct file content")
}

func TestTryLoadModelFromStandardPaths_FirstHitWins(t *testing.T) {
	// Note: Cannot run in parallel due to os.Chdir() usage affecting global state
	
	const testModelName = "test_model.tflite"
	
	tempDir := t.TempDir()
	originalWd, err := os.Getwd()
	require.NoError(t, err, "Failed to get working directory")
	
	defer func() {
		if err := os.Chdir(originalWd); err != nil {
			t.Errorf("Failed to restore working directory: %v", err)
		}
	}()
	
	require.NoError(t, os.Chdir(tempDir), "Failed to change to temp directory")

	// Create test files in multiple locations
	firstPath := filepath.Join(DefaultModelDirectory, testModelName)
	secondPath := filepath.Join("data", DefaultModelDirectory, testModelName)

	// Create first priority file
	require.NoError(t, os.MkdirAll(DefaultModelDirectory, 0o755), "Failed to create first directory")
	require.NoError(t, os.WriteFile(firstPath, []byte("first_priority"), 0o644), "Failed to create first file")

	// Create second priority file
	require.NoError(t, os.MkdirAll(filepath.Join("data", DefaultModelDirectory), 0o755), 
		"Failed to create second directory")
	require.NoError(t, os.WriteFile(secondPath, []byte("second_priority"), 0o644), 
		"Failed to create second file")

	result := tryLoadModelFromStandardPaths(testModelName)

	require.True(t, result.Found, "Expected model to be found")
	// Should find first priority
	require.True(t, strings.HasSuffix(result.Path, firstPath),
		"Expected first priority path ending with %s, got %s", firstPath, result.Path)
	require.Equal(t, "first_priority", string(result.Data), "Expected first priority data")
}

func TestTryLoadModelFromStandardPaths_ModelNotFound(t *testing.T) {
	// Note: Cannot run in parallel due to os.Chdir() usage affecting global state
	
	tempDir := t.TempDir()
	originalWd, err := os.Getwd()
	require.NoError(t, err, "Failed to get working directory")
	
	defer func() {
		if err := os.Chdir(originalWd); err != nil {
			t.Errorf("Failed to restore working directory: %v", err)
		}
	}()
	
	require.NoError(t, os.Chdir(tempDir), "Failed to change to temp directory")

	result := tryLoadModelFromStandardPaths("nonexistent.tflite")

	require.False(t, result.Found, "Expected model not to be found")
	require.Empty(t, result.Path, "Expected empty path when not found")
	require.Nil(t, result.Data, "Expected nil data when not found")
	require.NotEmpty(t, result.AttemptedPaths, "Expected AttemptedPaths to be populated even when not found")

	// Verify AttemptedPaths contains expected relative paths
	expectedPatterns := []string{DefaultModelDirectory, "data"}
	for _, pattern := range expectedPatterns {
		found := false
		for _, attemptedPath := range result.AttemptedPaths {
			if strings.Contains(attemptedPath, pattern) {
				found = true
				break
			}
		}
		require.True(t, found, "Expected attempted paths to include pattern %s, got %v", 
			pattern, result.AttemptedPaths)
	}
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
			
			tempDir := t.TempDir()
			originalWd, err := os.Getwd()
			require.NoError(t, err, "Failed to get working directory")
			
			defer func() {
				if err := os.Chdir(originalWd); err != nil {
					t.Errorf("Failed to restore working directory: %v", err)
				}
			}()
			
			require.NoError(t, os.Chdir(tempDir), "Failed to change to temp directory")

			// Create test file
			modelPath := filepath.Join(DefaultModelDirectory, tc.modelName)
			require.NoError(t, os.MkdirAll(DefaultModelDirectory, 0o755), "Failed to create model directory")
			require.NoError(t, os.WriteFile(modelPath, []byte(testContent), 0o644), "Failed to create test file")

			result := tryLoadModelFromStandardPaths(tc.modelName)

			require.True(t, result.Found, "Expected model to be found")
			require.True(t, strings.HasSuffix(result.Path, modelPath),
				"Expected path to end with %s, got %s", modelPath, result.Path)
			require.Equal(t, testContent, string(result.Data), "Expected correct file content")
		})
	}
}

func TestTryLoadModelFromStandardPaths_AttemptedPaths(t *testing.T) {
	// Note: Cannot run in parallel due to os.Chdir() usage affecting global state

	const testModelName = "nonexistent.tflite"
	
	tempDir := t.TempDir()
	originalWd, err := os.Getwd()
	require.NoError(t, err, "Failed to get working directory")
	
	defer func() {
		if err := os.Chdir(originalWd); err != nil {
			t.Errorf("Failed to restore working directory: %v", err)
		}
	}()
	
	require.NoError(t, os.Chdir(tempDir), "Failed to change to temp directory")

	result := tryLoadModelFromStandardPaths(testModelName)

	require.False(t, result.Found, "Expected not to find model")

	// Verify that AttemptedPaths is populated and contains expected patterns
	require.NotEmpty(t, result.AttemptedPaths, "Expected AttemptedPaths to be populated")

	// All attempted paths should end with the model name
	for _, attemptedPath := range result.AttemptedPaths {
		require.Equal(t, testModelName, filepath.Base(attemptedPath),
			"Expected all attempted paths to end with model name")
	}

	// Should contain both relative and absolute paths
	hasRelativePath := false
	hasAbsolutePath := false
	for _, path := range result.AttemptedPaths {
		if filepath.IsAbs(path) {
			hasAbsolutePath = true
		} else {
			hasRelativePath = true
		}
	}

	require.True(t, hasRelativePath, "Expected some relative paths in AttemptedPaths")
	require.True(t, hasAbsolutePath, "Expected some absolute paths in AttemptedPaths")
}