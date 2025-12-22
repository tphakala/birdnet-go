package securefs

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Test constant for test file name.
const testFileName = "test.txt"

// setupSecureFS creates a temporary directory and SecureFS instance for testing
func setupSecureFS(t *testing.T) (sfs *SecureFS, tempDir string) {
	t.Helper()

	// Create temporary parent directory
	tempDir = t.TempDir()

	// For debugging
	t.Logf("Creating test directory: %s", tempDir)

	// Explicitly verify the temp directory exists
	if _, err := os.Stat(tempDir); err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}

	// Create SecureFS with the temp directory
	sfs, err := New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create SecureFS: %v", err)
	}

	return sfs, tempDir
}

func TestSecureFSWriteFile(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	testFile := filepath.Join(tempDir, "test.txt")
	if err := sfs.WriteFile(testFile, []byte("test data"), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
}

func TestSecureFSExists(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	testFile := filepath.Join(tempDir, "test.txt")
	_ = sfs.WriteFile(testFile, []byte("test"), 0o600)

	exists, err := sfs.Exists(testFile)
	if err != nil {
		t.Fatalf("Exists check failed: %v", err)
	}
	if !exists {
		t.Fatal("file should exist")
	}
}

func TestSecureFSReadFile(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	testFile := filepath.Join(tempDir, "test.txt")
	testData := []byte("test data")
	_ = sfs.WriteFile(testFile, testData, 0o600)

	data, err := sfs.ReadFile(testFile)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if !bytes.Equal(data, testData) {
		t.Fatalf("got %q, want %q", string(data), string(testData))
	}
}

// TestReadFileWithSizeLimit verifies that ReadFile respects the configured max file size
func TestReadFileWithSizeLimit(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	// Set a small max file size for testing
	sfs.SetMaxReadFileSize(100)

	// Test 1: File within limit should be read successfully
	smallFile := filepath.Join(tempDir, "small.txt")
	smallContent := []byte("small content")
	if err := sfs.WriteFile(smallFile, smallContent, 0o600); err != nil {
		t.Fatalf("Failed to write small file: %v", err)
	}

	data, err := sfs.ReadFile(smallFile)
	if err != nil {
		t.Errorf("ReadFile should succeed for file within limit: %v", err)
	}
	if !bytes.Equal(data, smallContent) {
		t.Errorf("Content mismatch: expected %q, got %q", smallContent, data)
	}

	// Test 2: File exceeding limit should return an error
	largeFile := filepath.Join(tempDir, "large.txt")
	largeContent := make([]byte, 200) // 200 bytes, exceeds 100 byte limit
	for i := range largeContent {
		largeContent[i] = 'x'
	}
	if err := sfs.WriteFile(largeFile, largeContent, 0o600); err != nil {
		t.Fatalf("Failed to write large file: %v", err)
	}

	_, err = sfs.ReadFile(largeFile)
	if err == nil {
		t.Error("ReadFile should have returned an error for file exceeding size limit")
	}
}

// TestReadFileSizeLimitZeroMeansUnlimited verifies that size limit of 0 means unlimited
func TestReadFileSizeLimitZeroMeansUnlimited(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	// Set limit to 0 (unlimited)
	sfs.SetMaxReadFileSize(0)

	// Create a reasonably sized file (not too large for testing)
	testFile := filepath.Join(tempDir, "test.txt")
	content := make([]byte, 1000)
	for i := range content {
		content[i] = byte('a' + i%26)
	}
	if err := sfs.WriteFile(testFile, content, 0o600); err != nil {
		t.Fatalf("Failed to write file: %v", err)
	}

	data, err := sfs.ReadFile(testFile)
	if err != nil {
		t.Errorf("ReadFile with 0 (unlimited) size limit should succeed: %v", err)
	}
	if len(data) != len(content) {
		t.Errorf("Content length mismatch: expected %d, got %d", len(content), len(data))
	}
}

func TestSecureFSReadFileNonExistent(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	_, err := sfs.ReadFile(filepath.Join(tempDir, "nonexistent.txt"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected os.ErrNotExist, got %v", err)
	}
}

func TestSecureFSStat(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	testFile := filepath.Join(tempDir, "test.txt")
	testData := []byte("test data")
	_ = sfs.WriteFile(testFile, testData, 0o600)

	info, err := sfs.Stat(testFile)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if info.Size() != int64(len(testData)) {
		t.Fatalf("got size %d, want %d", info.Size(), len(testData))
	}
}

func TestSecureFSOpenFile(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	testFile := filepath.Join(tempDir, "test.txt")
	_ = sfs.WriteFile(testFile, []byte("test"), 0o600)

	file, err := sfs.OpenFile(testFile, os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile failed: %v", err)
	}
	_ = file.Close()
}

func TestSecureFSRemove(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	testFile := filepath.Join(tempDir, "test.txt")
	_ = sfs.WriteFile(testFile, []byte("test"), 0o600)

	if err := sfs.Remove(testFile); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	exists, err := sfs.Exists(testFile)
	if err != nil {
		t.Fatalf("Exists check failed: %v", err)
	}
	if exists {
		t.Fatal("file should not exist after removal")
	}
}

func TestSecureFSDirectoryOperations(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() {
		if err := sfs.Close(); err != nil {
			t.Logf("error closing sfs: %v", err)
		}
	})

	// Test MkdirAll
	testDir := filepath.Join(tempDir, "subdir", "nested")
	err := sfs.MkdirAll(testDir, 0o750)
	if err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	exists, err := sfs.Exists(testDir)
	if err != nil {
		t.Fatalf("Exists check failed with error: %v", err)
	}
	if !exists {
		t.Fatal("MkdirAll failed: directory should exist")
	}

	// Test file in nested directory
	nestedFile := filepath.Join(testDir, "nested.txt")
	nestedData := []byte("nested file data")
	err = sfs.WriteFile(nestedFile, nestedData, 0o600)
	if err != nil {
		t.Fatalf("WriteFile in nested dir failed: %v", err)
	}

	// Test RemoveAll
	err = sfs.RemoveAll(filepath.Join(tempDir, "subdir"))
	if err != nil {
		t.Fatalf("RemoveAll failed: %v", err)
	}
	exists, err = sfs.Exists(testDir)
	if err != nil {
		t.Fatalf("Exists check failed with error: %v", err)
	}
	if exists {
		t.Fatal("RemoveAll failed: directory should not exist")
	}
}

func TestSecureFSPathTraversalPrevention(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() {
		if err := sfs.Close(); err != nil {
			t.Logf("error closing sfs: %v", err)
		}
	})

	// Test path traversal prevention
	traversalPath := filepath.Join(tempDir, "..", "outside.txt")
	_, err := sfs.RelativePath(traversalPath)
	if err == nil {
		t.Fatal("relativePath should have failed on traversal path")
	}
	if !strings.Contains(err.Error(), "security error") {
		t.Errorf("Expected security error message, got: %v", err)
	}

	// Attempt to write outside the sandbox
	err = sfs.WriteFile(traversalPath, []byte("should fail"), 0o600)
	if err == nil {
		t.Fatal("WriteFile should have failed for path outside sandbox")
	}
	if !strings.Contains(err.Error(), "security error") {
		t.Errorf("Expected security error message, got: %v", err)
	}

	// Attempt to read outside the sandbox
	_, err = sfs.ReadFile(traversalPath)
	if err == nil {
		t.Fatal("ReadFile should have failed for path outside sandbox")
	}
	if !strings.Contains(err.Error(), "security error") {
		t.Errorf("Expected security error message, got: %v", err)
	}
}

func TestIsPathWithinBaseValid(t *testing.T) {
	t.Parallel()
	_, tempDir := setupSecureFS(t)

	validPath := filepath.Join(tempDir, "file.txt")
	isWithin, err := IsPathWithinBase(tempDir, validPath)
	if err != nil {
		t.Fatalf("IsPathWithinBase failed: %v", err)
	}
	if !isWithin {
		t.Fatal("valid path should be within base")
	}
}

func TestIsPathWithinBaseInvalid(t *testing.T) {
	t.Parallel()
	_, tempDir := setupSecureFS(t)

	invalidPath := filepath.Join(tempDir, "..", "outside.txt")
	isWithin, err := IsPathWithinBase(tempDir, invalidPath)
	if err != nil {
		t.Fatalf("IsPathWithinBase failed: %v", err)
	}
	if isWithin {
		t.Fatal("traversal path should not be within base")
	}
}

func TestIsPathWithinBaseSamePath(t *testing.T) {
	t.Parallel()
	_, tempDir := setupSecureFS(t)

	isWithin, err := IsPathWithinBase(tempDir, tempDir)
	if err != nil {
		t.Fatalf("IsPathWithinBase failed: %v", err)
	}
	if !isWithin {
		t.Fatal("same path should be within base")
	}
}

func TestIsPathWithinBaseSymlinkEscape(t *testing.T) {
	t.Parallel()
	_, tempDir := setupSecureFS(t)

	if os.Getuid() == 0 {
		t.Skip("Skipping symlink escape test when running as root")
	}

	_, symlinkPath := setupSymlinkTest(t, tempDir)
	if symlinkPath == "" {
		return // setup skipped
	}

	escapePath := filepath.Join(symlinkPath, "secret.txt")
	isWithin, _ := IsPathWithinBase(tempDir, escapePath)
	if isWithin {
		t.Error("failed to detect symlink escape")
	}
}

// setupSymlinkTest creates the directory structure for symlink escape testing
func setupSymlinkTest(t *testing.T, tempDir string) (outsideDir, symlinkPath string) {
	t.Helper()

	insideDir := filepath.Join(tempDir, "inside")
	if err := os.Mkdir(insideDir, 0o750); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	outsideDir = filepath.Join(os.TempDir(), "securefs_test_outside")
	if err := os.MkdirAll(outsideDir, 0o750); err != nil {
		t.Fatalf("Failed to create outside test directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outsideDir) })

	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret data"), 0o600); err != nil {
		t.Fatalf("Failed to create outside test file: %v", err)
	}

	symlinkPath = filepath.Join(insideDir, "symlink_escape")
	if err := os.Symlink(outsideDir, symlinkPath); err != nil {
		t.Logf("Symlink creation failed (permissions?): %v", err)
		t.Skip("Skipping symlink test due to permission issues")
		return "", ""
	}

	return outsideDir, symlinkPath
}

func TestIsPathValidWithinBase(t *testing.T) {
	t.Parallel()
	_, tempDir := setupSecureFS(t)

	// Test with valid path
	validPath := filepath.Join(tempDir, "valid.txt")
	err := IsPathValidWithinBase(tempDir, validPath)
	if err != nil {
		t.Fatalf("IsPathValidWithinBase failed for valid path: %v", err)
	}

	// Test with invalid path
	invalidPath := filepath.Join(tempDir, "..", "outside.txt")
	err = IsPathValidWithinBase(tempDir, invalidPath)
	if err == nil {
		t.Fatal("IsPathValidWithinBase should have failed for invalid path")
	}
	// Verify the error type or message
	if !strings.Contains(err.Error(), "security error") {
		t.Errorf("Expected security error message, got: %v", err)
	}
}

// TestReadlinkWithinSandbox verifies that Readlink works for symlinks within the sandbox
func TestReadlinkWithinSandbox(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	// Create a target file within the sandbox
	targetFile := filepath.Join(tempDir, "target.txt")
	if err := os.WriteFile(targetFile, []byte("target content"), 0o600); err != nil {
		t.Fatalf("Failed to create target file: %v", err)
	}

	// Create a symlink pointing to the target (using relative path)
	symlinkPath := filepath.Join(tempDir, "link.txt")
	if err := os.Symlink("target.txt", symlinkPath); err != nil {
		t.Fatalf("Failed to create symlink: %v", err)
	}

	// Test Readlink - should return the relative target
	target, err := sfs.Readlink(symlinkPath)
	if err != nil {
		t.Fatalf("Readlink failed for valid symlink within sandbox: %v", err)
	}

	if target != "target.txt" {
		t.Errorf("Expected target 'target.txt', got '%s'", target)
	}
}

// TestReadlinkReturnsTargetString verifies that Readlink returns the symlink target
// as a string, regardless of whether the target is safe to follow.
// Note: Readlink is an informational operation - security validation happens
// when you try to FOLLOW the symlink (via Open, Stat, etc.)
func TestReadlinkReturnsTargetString(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	// Test 1: Symlink with absolute target - Readlink should return the target string
	absSymlink := filepath.Join(tempDir, "abs_link.txt")
	if err := os.Symlink("/etc/passwd", absSymlink); err != nil {
		t.Fatalf("Failed to create absolute symlink: %v", err)
	}

	target, err := sfs.Readlink(absSymlink)
	if err != nil {
		t.Errorf("Readlink should return target string, got error: %v", err)
	}
	if target != "/etc/passwd" {
		t.Errorf("Expected target '/etc/passwd', got '%s'", target)
	}

	// Test 2: Symlink with relative escaping target - Readlink should return the target string
	relEscapeSymlink := filepath.Join(tempDir, "rel_escape.txt")
	if err := os.Symlink("../../etc/passwd", relEscapeSymlink); err != nil {
		t.Fatalf("Failed to create relative escaping symlink: %v", err)
	}

	target, err = sfs.Readlink(relEscapeSymlink)
	if err != nil {
		t.Errorf("Readlink should return target string, got error: %v", err)
	}
	if target != "../../etc/passwd" {
		t.Errorf("Expected target '../../etc/passwd', got '%s'", target)
	}
}

// TestReadDir verifies the ReadDir function
func TestReadDir(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	// Create some files and directories
	_ = sfs.WriteFile(filepath.Join(tempDir, "file1.txt"), []byte("content1"), 0o600)
	_ = sfs.WriteFile(filepath.Join(tempDir, "file2.txt"), []byte("content2"), 0o600)
	_ = sfs.MkdirAll(filepath.Join(tempDir, "subdir"), 0o750)

	// Read directory
	entries, err := sfs.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}

	if len(entries) != 3 {
		t.Errorf("Expected 3 entries, got %d", len(entries))
	}

	// Verify entry names
	names := make(map[string]bool)
	for _, entry := range entries {
		names[entry.Name()] = true
	}

	if !names["file1.txt"] || !names["file2.txt"] || !names["subdir"] {
		t.Errorf("Missing expected entries: %v", names)
	}
}

// TestParentPath verifies the ParentPath function
func TestParentPath(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	// Create a nested directory
	nested := filepath.Join(tempDir, "a", "b", "c")
	if err := sfs.MkdirAll(nested, 0o750); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	// Test ParentPath
	parent, err := sfs.ParentPath(nested)
	if err != nil {
		t.Fatalf("ParentPath failed: %v", err)
	}

	expected := filepath.Join(tempDir, "a", "b")
	if parent != expected {
		t.Errorf("Expected '%s', got '%s'", expected, parent)
	}

	// Test root returns empty
	rootParent, err := sfs.ParentPath(tempDir)
	if err != nil {
		t.Fatalf("ParentPath for root failed: %v", err)
	}
	if rootParent != "" {
		t.Errorf("Expected empty string for root parent, got '%s'", rootParent)
	}
}

// TestLstat verifies the Lstat function
func TestLstat(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	// Create a file
	testFile := filepath.Join(tempDir, "test.txt")
	if err := sfs.WriteFile(testFile, []byte("test content"), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Lstat should work for regular files
	info, err := sfs.Lstat(testFile)
	if err != nil {
		t.Fatalf("Lstat failed: %v", err)
	}

	if info.Name() != testFileName {
		t.Errorf("Expected name '%s', got '%s'", testFileName, info.Name())
	}

	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("Regular file should not have symlink mode")
	}
}

// TestStatRel verifies the StatRel function
func TestStatRel(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	// Create a file
	if err := sfs.WriteFile(filepath.Join(tempDir, "test.txt"), []byte("content"), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// StatRel with relative path
	info, err := sfs.StatRel("test.txt")
	if err != nil {
		t.Fatalf("StatRel failed: %v", err)
	}

	if info.Name() != testFileName {
		t.Errorf("Expected name '%s', got '%s'", testFileName, info.Name())
	}

	// StatRel with traversal should fail
	_, err = sfs.StatRel("../outside.txt")
	if err == nil {
		t.Error("StatRel should fail for path traversal attempt")
	}
}

// TestCacheUtilities verifies cache utility functions
func TestCacheUtilities(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	// Create a file to trigger some cache entries
	testFile := filepath.Join(tempDir, "test.txt")
	_ = sfs.WriteFile(testFile, []byte("content"), 0o600)
	_, _ = sfs.Exists(testFile)

	// Test GetCacheStats
	stats := sfs.GetCacheStats()
	// Just verify it doesn't panic and returns something
	if stats.AbsPathTotal < 0 {
		t.Error("Invalid cache stats")
	}

	// Test ClearExpiredCache doesn't panic
	sfs.ClearExpiredCache()

	// Test BaseDir
	if sfs.BaseDir() != tempDir {
		t.Errorf("Expected BaseDir '%s', got '%s'", tempDir, sfs.BaseDir())
	}
}

// TestExistsNoErr verifies ExistsNoErr convenience function
func TestExistsNoErr(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	// Create a file
	testFile := filepath.Join(tempDir, "test.txt")
	_ = sfs.WriteFile(testFile, []byte("content"), 0o600)

	// Should return true for existing file
	if !sfs.ExistsNoErr(testFile) {
		t.Error("ExistsNoErr should return true for existing file")
	}

	// Should return false for non-existent file
	if sfs.ExistsNoErr(filepath.Join(tempDir, "nonexistent.txt")) {
		t.Error("ExistsNoErr should return false for non-existent file")
	}

	// Should return false for invalid path (traversal)
	if sfs.ExistsNoErr(filepath.Join(tempDir, "..", "outside.txt")) {
		t.Error("ExistsNoErr should return false for path traversal")
	}
}

// TestGetMaxReadFileSize verifies the getter method
func TestGetMaxReadFileSize(t *testing.T) {
	t.Parallel()
	sfs, _ := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	// Default should be 0
	if sfs.GetMaxReadFileSize() != 0 {
		t.Errorf("Expected default 0, got %d", sfs.GetMaxReadFileSize())
	}

	// Set and verify
	sfs.SetMaxReadFileSize(1024)
	if sfs.GetMaxReadFileSize() != 1024 {
		t.Errorf("Expected 1024, got %d", sfs.GetMaxReadFileSize())
	}
}

// TestFollowingEscapingSymlinkFails verifies that while Readlink returns the target,
// actually trying to FOLLOW an escaping symlink via Open/Stat will fail
func TestFollowingEscapingSymlinkFails(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	// Create a symlink with a relative path that would escape the sandbox
	symlinkPath := filepath.Join(tempDir, "escape.txt")
	if err := os.Symlink("../../etc/passwd", symlinkPath); err != nil {
		t.Fatalf("Failed to create symlink: %v", err)
	}

	// Readlink should succeed (returns the target string)
	target, err := sfs.Readlink(symlinkPath)
	if err != nil {
		t.Fatalf("Readlink failed: %v", err)
	}
	if target != "../../etc/passwd" {
		t.Errorf("Expected target '../../etc/passwd', got '%s'", target)
	}

	// But trying to Open the symlink should fail because it escapes the sandbox
	// The security is enforced when following the link, not when reading its target
	_, err = sfs.Open(symlinkPath)
	if err == nil {
		t.Error("Open should have failed for symlink escaping sandbox")
	}
}
