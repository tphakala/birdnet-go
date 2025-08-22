package birdnet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTryLoadModelFromStandardPaths_RelativeDirectory(t *testing.T) {
	// Note: Cannot run in parallel due to os.Chdir() usage affecting global state
	
	const testModelName = "test_model.tflite"
	const testContent = "mock model data"
	
	tempDir := t.TempDir()
	originalWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}
	
	defer func() {
		if err := os.Chdir(originalWd); err != nil {
			t.Errorf("Failed to restore working directory: %v", err)
		}
	}()
	
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("Failed to change to temp directory: %v", err)
	}

	// Create test file in relative model directory
	modelPath := filepath.Join(DefaultModelDirectory, testModelName)
	if err := os.MkdirAll(DefaultModelDirectory, 0o755); err != nil {
		t.Fatalf("Failed to create model directory: %v", err)
	}
	if err := os.WriteFile(modelPath, []byte(testContent), 0o644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	result := tryLoadModelFromStandardPaths(testModelName)

	if !result.Found {
		t.Error("Expected Found=true")
	}
	if !strings.HasSuffix(result.Path, modelPath) {
		t.Errorf("Expected path to end with %s, got %s", modelPath, result.Path)
	}
	if string(result.Data) != testContent {
		t.Errorf("Expected data=%q, got %q", testContent, string(result.Data))
	}
	if len(result.AttemptedPaths) == 0 {
		t.Error("Expected AttemptedPaths to be populated")
	}
}

func TestTryLoadModelFromStandardPaths_LegacyDataDirectory(t *testing.T) {
	// Note: Cannot run in parallel due to os.Chdir() usage affecting global state
	
	const testModelName = "test_model.tflite"
	const testContent = "mock model data"
	
	tempDir := t.TempDir()
	originalWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}
	
	defer func() {
		if err := os.Chdir(originalWd); err != nil {
			t.Errorf("Failed to restore working directory: %v", err)
		}
	}()
	
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("Failed to change to temp directory: %v", err)
	}

	// Create test file in legacy data/model directory  
	modelPath := filepath.Join("data", DefaultModelDirectory, testModelName)
	if err := os.MkdirAll(filepath.Join("data", DefaultModelDirectory), 0o755); err != nil {
		t.Fatalf("Failed to create model directory: %v", err)
	}
	if err := os.WriteFile(modelPath, []byte(testContent), 0o644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	result := tryLoadModelFromStandardPaths(testModelName)

	if !result.Found {
		t.Error("Expected Found=true")
	}
	if !strings.HasSuffix(result.Path, modelPath) {
		t.Errorf("Expected path to end with %s, got %s", modelPath, result.Path)
	}
	if string(result.Data) != testContent {
		t.Errorf("Expected data=%q, got %q", testContent, string(result.Data))
	}
}

func TestTryLoadModelFromStandardPaths_FirstHitWins(t *testing.T) {
	// Note: Cannot run in parallel due to os.Chdir() usage affecting global state
	
	const testModelName = "test_model.tflite"
	
	tempDir := t.TempDir()
	originalWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}
	
	defer func() {
		if err := os.Chdir(originalWd); err != nil {
			t.Errorf("Failed to restore working directory: %v", err)
		}
	}()
	
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("Failed to change to temp directory: %v", err)
	}

	// Create test files in multiple locations
	firstPath := filepath.Join(DefaultModelDirectory, testModelName)
	secondPath := filepath.Join("data", DefaultModelDirectory, testModelName)

	// Create first priority file
	if err := os.MkdirAll(DefaultModelDirectory, 0o755); err != nil {
		t.Fatalf("Failed to create first directory: %v", err)
	}
	if err := os.WriteFile(firstPath, []byte("first_priority"), 0o644); err != nil {
		t.Fatalf("Failed to create first file: %v", err)
	}

	// Create second priority file
	if err := os.MkdirAll(filepath.Join("data", DefaultModelDirectory), 0o755); err != nil {
		t.Fatalf("Failed to create second directory: %v", err)
	}
	if err := os.WriteFile(secondPath, []byte("second_priority"), 0o644); err != nil {
		t.Fatalf("Failed to create second file: %v", err)
	}

	result := tryLoadModelFromStandardPaths(testModelName)

	if !result.Found {
		t.Error("Expected Found=true")
	}
	// Should find first priority
	if !strings.HasSuffix(result.Path, firstPath) {
		t.Errorf("Expected first priority path ending with %s, got %s", firstPath, result.Path)
	}
	if string(result.Data) != "first_priority" {
		t.Errorf("Expected first priority data, got %s", string(result.Data))
	}
}

func TestTryLoadModelFromStandardPaths_ModelNotFound(t *testing.T) {
	// Note: Cannot run in parallel due to os.Chdir() usage affecting global state
	
	tempDir := t.TempDir()
	originalWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}
	
	defer func() {
		if err := os.Chdir(originalWd); err != nil {
			t.Errorf("Failed to restore working directory: %v", err)
		}
	}()
	
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("Failed to change to temp directory: %v", err)
	}

	result := tryLoadModelFromStandardPaths("nonexistent.tflite")

	if result.Found {
		t.Error("Expected Found=false")
	}
	if result.Path != "" {
		t.Error("Expected empty path when not found")
	}
	if result.Data != nil {
		t.Error("Expected nil data when not found")
	}
	if len(result.AttemptedPaths) == 0 {
		t.Error("Expected AttemptedPaths to be populated even when not found")
	}

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
		if !found {
			t.Errorf("Expected attempted paths to include pattern %s, got %v", pattern, result.AttemptedPaths)
		}
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
			if err != nil {
				t.Fatalf("Failed to get working directory: %v", err)
			}
			
			defer func() {
				if err := os.Chdir(originalWd); err != nil {
					t.Errorf("Failed to restore working directory: %v", err)
				}
			}()
			
			if err := os.Chdir(tempDir); err != nil {
				t.Fatalf("Failed to change to temp directory: %v", err)
			}

			// Create test file
			modelPath := filepath.Join(DefaultModelDirectory, tc.modelName)
			if err := os.MkdirAll(DefaultModelDirectory, 0o755); err != nil {
				t.Fatalf("Failed to create model directory: %v", err)
			}
			if err := os.WriteFile(modelPath, []byte(testContent), 0o644); err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			result := tryLoadModelFromStandardPaths(tc.modelName)

			if !result.Found {
				t.Error("Expected Found=true")
			}
			if !strings.HasSuffix(result.Path, modelPath) {
				t.Errorf("Expected path to end with %s, got %s", modelPath, result.Path)
			}
			if string(result.Data) != testContent {
				t.Errorf("Expected data=%q, got %q", testContent, string(result.Data))
			}
		})
	}
}

func TestTryLoadModelFromStandardPaths_AttemptedPaths(t *testing.T) {
	// Note: Cannot run in parallel due to os.Chdir() usage affecting global state

	const testModelName = "nonexistent.tflite"
	
	tempDir := t.TempDir()
	originalWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}
	
	defer func() {
		if err := os.Chdir(originalWd); err != nil {
			t.Errorf("Failed to restore working directory: %v", err)
		}
	}()
	
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("Failed to change to temp directory: %v", err)
	}

	result := tryLoadModelFromStandardPaths(testModelName)

	if result.Found {
		t.Error("Expected not to find model, but found=true")
	}

	// Verify that AttemptedPaths is populated and contains expected patterns
	if len(result.AttemptedPaths) == 0 {
		t.Fatal("Expected AttemptedPaths to be populated")
	}

	// All attempted paths should end with the model name
	for _, attemptedPath := range result.AttemptedPaths {
		if filepath.Base(attemptedPath) != testModelName {
			t.Errorf("Expected all attempted paths to end with %s, but %s does not", testModelName, attemptedPath)
		}
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

	if !hasRelativePath {
		t.Error("Expected some relative paths in AttemptedPaths")
	}
	if !hasAbsolutePath {
		t.Error("Expected some absolute paths in AttemptedPaths")
	}
}