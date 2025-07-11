package backup

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSecureFileOp_ValidatePath_CrossPlatform(t *testing.T) {
	t.Parallel()
	
	secureOp := NewSecureFileOp("test")
	
	tests := []struct {
		name     string
		path     string
		wantErr  bool
		platform string
	}{
		{
			name:     "valid relative path",
			path:     "test/file.txt",
			wantErr:  false,
			platform: "all",
		},
		{
			name:     "valid absolute path unix",
			path:     "/tmp/test.txt",
			wantErr:  false,
			platform: "unix",
		},
		{
			name:     "valid absolute path windows",
			path:     "C:\\temp\\test.txt",
			wantErr:  false,
			platform: "windows",
		},
		{
			name:     "path with dots",
			path:     "test/../file.txt",
			wantErr:  false,
			platform: "all",
		},
		{
			name:     "path with double dots",
			path:     "test/../../file.txt",
			wantErr:  false,
			platform: "all",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.platform == "unix" && runtime.GOOS == "windows" {
				t.Skip("Unix-specific test on Windows")
			}
			if tt.platform == "windows" && runtime.GOOS != "windows" {
				t.Skip("Windows-specific test on non-Windows")
			}
			
			cleanPath, err := secureOp.ValidatePath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			
			if !tt.wantErr {
				// Verify the path is cleaned
				if cleanPath != filepath.Clean(tt.path) {
					t.Errorf("ValidatePath() = %v, want %v", cleanPath, filepath.Clean(tt.path))
				}
			}
		})
	}
}

func TestSecureFileOp_ValidatePath_WithBaseDir(t *testing.T) {
	t.Parallel()
	
	tempDir := t.TempDir()
	secureOp := NewSecureFileOp("test", tempDir)
	
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name:    "path within base dir",
			path:    filepath.Join(tempDir, "subdir", "file.txt"),
			wantErr: false,
		},
		{
			name:    "path outside base dir with dots",
			path:    filepath.Join(tempDir, "..", "outside.txt"),
			wantErr: true,
		},
		{
			name:    "path outside base dir absolute",
			path:    "/tmp/outside.txt",
			wantErr: true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := secureOp.ValidatePath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePath() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSecureFileOp_SecureCreate_CrossPlatform(t *testing.T) {
	t.Parallel()
	
	tempDir := t.TempDir()
	secureOp := NewSecureFileOp("test")
	
	testFile := filepath.Join(tempDir, "test.txt")
	
	file, cleanPath, err := secureOp.SecureCreate(testFile)
	if err != nil {
		t.Fatalf("SecureCreate() error = %v", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("Failed to close file: %v", err)
		}
	}()
	
	// Verify the file was created
	if _, err := os.Stat(cleanPath); os.IsNotExist(err) {
		t.Errorf("File was not created at %s", cleanPath)
	}
	
	// Verify the clean path
	if cleanPath != filepath.Clean(testFile) {
		t.Errorf("SecureCreate() cleanPath = %v, want %v", cleanPath, filepath.Clean(testFile))
	}
}

func TestSecureFileOp_SecureReadFile_CrossPlatform(t *testing.T) {
	t.Parallel()
	
	tempDir := t.TempDir()
	secureOp := NewSecureFileOp("test")
	
	testContent := "Hello, World!"
	testFile := filepath.Join(tempDir, "test.txt")
	
	// Create test file
	if err := os.WriteFile(testFile, []byte(testContent), 0o600); err != nil { // #nosec G306 - test file with secure permissions
		t.Fatalf("Failed to create test file: %v", err)
	}
	
	// Read file using SecureReadFile
	data, cleanPath, err := secureOp.SecureReadFile(testFile)
	if err != nil {
		t.Fatalf("SecureReadFile() error = %v", err)
	}
	
	// Verify content
	if string(data) != testContent {
		t.Errorf("SecureReadFile() data = %v, want %v", string(data), testContent)
	}
	
	// Verify clean path
	if cleanPath != filepath.Clean(testFile) {
		t.Errorf("SecureReadFile() cleanPath = %v, want %v", cleanPath, filepath.Clean(testFile))
	}
}

func TestSecureFileOp_SecureMkdirAll_CrossPlatform(t *testing.T) {
	t.Parallel()
	
	tempDir := t.TempDir()
	secureOp := NewSecureFileOp("test")
	
	testDir := filepath.Join(tempDir, "subdir", "nested")
	
	cleanPath, err := secureOp.SecureMkdirAll(testDir, DefaultDirectoryPermissions())
	if err != nil {
		t.Fatalf("SecureMkdirAll() error = %v", err)
	}
	
	// Verify directory was created
	if _, err := os.Stat(cleanPath); os.IsNotExist(err) {
		t.Errorf("Directory was not created at %s", cleanPath)
	}
	
	// Verify it's a directory
	if info, err := os.Stat(cleanPath); err != nil || !info.IsDir() {
		t.Errorf("Created path is not a directory")
	}
}

func TestDefaultPermissions(t *testing.T) {
	t.Parallel()
	
	dirPerm := DefaultDirectoryPermissions()
	filePerm := DefaultFilePermissions()
	
	// Verify directory permissions are restrictive
	if dirPerm != 0o750 {
		t.Errorf("DefaultDirectoryPermissions() = %o, want %o", dirPerm, 0o750)
	}
	
	// Verify file permissions are restrictive
	if filePerm != 0o640 {
		t.Errorf("DefaultFilePermissions() = %o, want %o", filePerm, 0o640)
	}
}

func TestSecureFileOpBatch_PathCaching(t *testing.T) {
	t.Parallel()
	
	batch := NewSecureFileOpBatch("test")
	
	testPath := "test/file.txt"
	
	// First call should validate and cache
	cleanPath1, err := batch.GetValidatedPath(testPath)
	if err != nil {
		t.Fatalf("GetValidatedPath() error = %v", err)
	}
	
	// Second call should use cache
	cleanPath2, err := batch.GetValidatedPath(testPath)
	if err != nil {
		t.Fatalf("GetValidatedPath() error = %v", err)
	}
	
	// Should return same result
	if cleanPath1 != cleanPath2 {
		t.Errorf("Cached result differs: %v != %v", cleanPath1, cleanPath2)
	}
	
	// Clear cache and verify it's cleared
	batch.ClearCache()
	cleanPath3, err := batch.GetValidatedPath(testPath)
	if err != nil {
		t.Fatalf("GetValidatedPath() error = %v", err)
	}
	
	// Should still return same result
	if cleanPath1 != cleanPath3 {
		t.Errorf("Result after cache clear differs: %v != %v", cleanPath1, cleanPath3)
	}
}

// TestCrossPlatformPathSeparators tests that path validation works correctly
// with different path separators on different platforms
func TestCrossPlatformPathSeparators(t *testing.T) {
	t.Parallel()
	
	secureOp := NewSecureFileOp("test")
	
	// Test that paths are cleaned properly on each platform
	testPath := filepath.Join("test", "subdir", "file.txt")
	
	cleanPath, err := secureOp.ValidatePath(testPath)
	if err != nil {
		t.Fatalf("ValidatePath() error = %v", err)
	}
	
	// Should be equal to filepath.Clean result
	expectedCleanPath := filepath.Clean(testPath)
	if cleanPath != expectedCleanPath {
		t.Errorf("ValidatePath() = %s, want %s", cleanPath, expectedCleanPath)
	}
	
	// Test with dots in path
	testPathWithDots := filepath.Join("test", "..", "other", "file.txt")
	cleanPathWithDots, err := secureOp.ValidatePath(testPathWithDots)
	if err != nil {
		t.Fatalf("ValidatePath() error = %v", err)
	}
	
	expectedCleanPathWithDots := filepath.Clean(testPathWithDots)
	if cleanPathWithDots != expectedCleanPathWithDots {
		t.Errorf("ValidatePath() with dots = %s, want %s", cleanPathWithDots, expectedCleanPathWithDots)
	}
}