package securefs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	_, err := os.Stat(tempDir)
	require.NoError(t, err, "Failed to create temp directory")

	// Create SecureFS with the temp directory
	sfs, err = New(tempDir)
	require.NoError(t, err, "Failed to create SecureFS")

	return sfs, tempDir
}

func TestSecureFSWriteFile(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	testFile := filepath.Join(tempDir, "test.txt")
	err := sfs.WriteFile(testFile, []byte("test data"), 0o600)
	require.NoError(t, err, "WriteFile failed")
}

func TestSecureFSExists(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	testFile := filepath.Join(tempDir, "test.txt")
	_ = sfs.WriteFile(testFile, []byte("test"), 0o600)

	exists, err := sfs.Exists(testFile)
	require.NoError(t, err, "Exists check failed")
	assert.True(t, exists, "file should exist")
}

func TestSecureFSReadFile(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	testFile := filepath.Join(tempDir, "test.txt")
	testData := []byte("test data")
	_ = sfs.WriteFile(testFile, testData, 0o600)

	data, err := sfs.ReadFile(testFile)
	require.NoError(t, err, "ReadFile failed")
	assert.Equal(t, testData, data)
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
	err := sfs.WriteFile(smallFile, smallContent, 0o600)
	require.NoError(t, err, "Failed to write small file")

	data, err := sfs.ReadFile(smallFile)
	require.NoError(t, err, "ReadFile should succeed for file within limit")
	assert.Equal(t, smallContent, data, "Content mismatch")

	// Test 2: File exceeding limit should return an error
	largeFile := filepath.Join(tempDir, "large.txt")
	largeContent := make([]byte, 200) // 200 bytes, exceeds 100 byte limit
	for i := range largeContent {
		largeContent[i] = 'x'
	}
	err = sfs.WriteFile(largeFile, largeContent, 0o600)
	require.NoError(t, err, "Failed to write large file")

	_, err = sfs.ReadFile(largeFile)
	assert.Error(t, err, "ReadFile should have returned an error for file exceeding size limit")
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
	err := sfs.WriteFile(testFile, content, 0o600)
	require.NoError(t, err, "Failed to write file")

	data, err := sfs.ReadFile(testFile)
	require.NoError(t, err, "ReadFile with 0 (unlimited) size limit should succeed")
	assert.Len(t, data, len(content), "Content length mismatch")
}

func TestSecureFSReadFileNonExistent(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	_, err := sfs.ReadFile(filepath.Join(tempDir, "nonexistent.txt"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestSecureFSStat(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	testFile := filepath.Join(tempDir, "test.txt")
	testData := []byte("test data")
	_ = sfs.WriteFile(testFile, testData, 0o600)

	info, err := sfs.Stat(testFile)
	require.NoError(t, err, "Stat failed")
	assert.Equal(t, int64(len(testData)), info.Size())
}

func TestSecureFSOpenFile(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	testFile := filepath.Join(tempDir, "test.txt")
	_ = sfs.WriteFile(testFile, []byte("test"), 0o600)

	file, err := sfs.OpenFile(testFile, os.O_RDONLY, 0)
	require.NoError(t, err, "OpenFile failed")
	_ = file.Close()
}

func TestSecureFSRemove(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	testFile := filepath.Join(tempDir, "test.txt")
	_ = sfs.WriteFile(testFile, []byte("test"), 0o600)

	err := sfs.Remove(testFile)
	require.NoError(t, err, "Remove failed")

	exists, err := sfs.Exists(testFile)
	require.NoError(t, err, "Exists check failed")
	assert.False(t, exists, "file should not exist after removal")
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
	require.NoError(t, err, "MkdirAll failed")

	exists, err := sfs.Exists(testDir)
	require.NoError(t, err, "Exists check failed")
	assert.True(t, exists, "directory should exist after MkdirAll")

	// Test file in nested directory
	nestedFile := filepath.Join(testDir, "nested.txt")
	nestedData := []byte("nested file data")
	err = sfs.WriteFile(nestedFile, nestedData, 0o600)
	require.NoError(t, err, "WriteFile in nested dir failed")

	// Test RemoveAll
	err = sfs.RemoveAll(filepath.Join(tempDir, "subdir"))
	require.NoError(t, err, "RemoveAll failed")

	exists, err = sfs.Exists(testDir)
	require.NoError(t, err, "Exists check failed")
	assert.False(t, exists, "directory should not exist after RemoveAll")

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
	require.Error(t, err, "relativePath should have failed on traversal path")
	assert.Contains(t, err.Error(), "security error")

	// Attempt to write outside the sandbox
	err = sfs.WriteFile(traversalPath, []byte("should fail"), 0o600)
	require.Error(t, err, "WriteFile should have failed for path outside sandbox")
	assert.Contains(t, err.Error(), "security error")

	// Attempt to read outside the sandbox
	_, err = sfs.ReadFile(traversalPath)
	require.Error(t, err, "ReadFile should have failed for path outside sandbox")
	assert.Contains(t, err.Error(), "security error")
}

func TestIsPathWithinBaseValid(t *testing.T) {
	t.Parallel()
	_, tempDir := setupSecureFS(t)

	validPath := filepath.Join(tempDir, "file.txt")
	isWithin, err := IsPathWithinBase(tempDir, validPath)
	require.NoError(t, err, "IsPathWithinBase failed")
	assert.True(t, isWithin, "valid path should be within base")
}

func TestIsPathWithinBaseInvalid(t *testing.T) {
	t.Parallel()
	_, tempDir := setupSecureFS(t)

	invalidPath := filepath.Join(tempDir, "..", "outside.txt")
	isWithin, err := IsPathWithinBase(tempDir, invalidPath)
	require.NoError(t, err, "IsPathWithinBase failed")
	assert.False(t, isWithin, "traversal path should not be within base")
}

func TestIsPathWithinBaseSamePath(t *testing.T) {
	t.Parallel()
	_, tempDir := setupSecureFS(t)

	isWithin, err := IsPathWithinBase(tempDir, tempDir)
	require.NoError(t, err, "IsPathWithinBase failed")
	assert.True(t, isWithin, "same path should be within base")
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
	assert.False(t, isWithin, "should detect symlink escape")
}

// setupSymlinkTest creates the directory structure for symlink escape testing
func setupSymlinkTest(t *testing.T, tempDir string) (outsideDir, symlinkPath string) {
	t.Helper()

	insideDir := filepath.Join(tempDir, "inside")
	err := os.Mkdir(insideDir, 0o750)
	require.NoError(t, err, "Failed to create test directory")

	outsideDir = filepath.Join(os.TempDir(), "securefs_test_outside")
	err = os.MkdirAll(outsideDir, 0o750)
	require.NoError(t, err, "Failed to create outside test directory")
	t.Cleanup(func() { _ = os.RemoveAll(outsideDir) })

	outsideFile := filepath.Join(outsideDir, "secret.txt")
	err = os.WriteFile(outsideFile, []byte("secret data"), 0o600)
	require.NoError(t, err, "Failed to create outside test file")

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
	require.NoError(t, err, "IsPathValidWithinBase failed for valid path")

	// Test with invalid path
	invalidPath := filepath.Join(tempDir, "..", "outside.txt")
	err = IsPathValidWithinBase(tempDir, invalidPath)
	require.Error(t, err, "IsPathValidWithinBase should have failed for invalid path")
	assert.Contains(t, err.Error(), "security error")
}

// TestReadlinkWithinSandbox verifies that Readlink works for symlinks within the sandbox
func TestReadlinkWithinSandbox(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	// Create a target file within the sandbox
	targetFile := filepath.Join(tempDir, "target.txt")
	err := os.WriteFile(targetFile, []byte("target content"), 0o600)
	require.NoError(t, err, "Failed to create target file")

	// Create a symlink pointing to the target (using relative path)
	symlinkPath := filepath.Join(tempDir, "link.txt")
	err = os.Symlink("target.txt", symlinkPath)
	require.NoError(t, err, "Failed to create symlink")

	// Test Readlink - should return the relative target
	target, err := sfs.Readlink(symlinkPath)
	require.NoError(t, err, "Readlink failed for valid symlink within sandbox")
	assert.Equal(t, "target.txt", target)
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
	err := os.Symlink("/etc/passwd", absSymlink)
	require.NoError(t, err, "Failed to create absolute symlink")

	target, err := sfs.Readlink(absSymlink)
	require.NoError(t, err, "Readlink should return target string")
	assert.Equal(t, "/etc/passwd", target)

	// Test 2: Symlink with relative escaping target - Readlink should return the target string
	relEscapeSymlink := filepath.Join(tempDir, "rel_escape.txt")
	err = os.Symlink("../../etc/passwd", relEscapeSymlink)
	require.NoError(t, err, "Failed to create relative escaping symlink")

	target, err = sfs.Readlink(relEscapeSymlink)
	require.NoError(t, err, "Readlink should return target string")
	assert.Equal(t, "../../etc/passwd", target)
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
	require.NoError(t, err, "ReadDir failed")
	assert.Len(t, entries, 3, "Expected 3 entries")

	// Verify entry names
	names := make(map[string]bool)
	for _, entry := range entries {
		names[entry.Name()] = true
	}

	assert.True(t, names["file1.txt"], "Missing file1.txt")
	assert.True(t, names["file2.txt"], "Missing file2.txt")
	assert.True(t, names["subdir"], "Missing subdir")
}

// TestParentPath verifies the ParentPath function
func TestParentPath(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	// Create a nested directory
	nested := filepath.Join(tempDir, "a", "b", "c")
	err := sfs.MkdirAll(nested, 0o750)
	require.NoError(t, err, "MkdirAll failed")

	// Test ParentPath
	parent, err := sfs.ParentPath(nested)
	require.NoError(t, err, "ParentPath failed")

	expected := filepath.Join(tempDir, "a", "b")
	assert.Equal(t, expected, parent)

	// Test root returns empty
	rootParent, err := sfs.ParentPath(tempDir)
	require.NoError(t, err, "ParentPath for root failed")
	assert.Empty(t, rootParent, "Expected empty string for root parent")
}

// TestLstat verifies the Lstat function
func TestLstat(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	// Create a file
	testFile := filepath.Join(tempDir, "test.txt")
	err := sfs.WriteFile(testFile, []byte("test content"), 0o600)
	require.NoError(t, err, "WriteFile failed")

	// Lstat should work for regular files
	info, err := sfs.Lstat(testFile)
	require.NoError(t, err, "Lstat failed")
	assert.Equal(t, testFileName, info.Name())
	assert.Equal(t, os.FileMode(0), info.Mode()&os.ModeSymlink, "Regular file should not have symlink mode")
}

// TestStatRel verifies the StatRel function
func TestStatRel(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	// Create a file
	err := sfs.WriteFile(filepath.Join(tempDir, "test.txt"), []byte("content"), 0o600)
	require.NoError(t, err, "WriteFile failed")

	// StatRel with relative path
	info, err := sfs.StatRel("test.txt")
	require.NoError(t, err, "StatRel failed")
	assert.Equal(t, testFileName, info.Name())

	// StatRel with traversal should fail
	_, err = sfs.StatRel("../outside.txt")
	assert.Error(t, err, "StatRel should fail for path traversal attempt")
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
	assert.GreaterOrEqual(t, stats.AbsPathTotal, 0, "Invalid cache stats")

	// Test ClearExpiredCache doesn't panic
	sfs.ClearExpiredCache()

	// Test BaseDir
	assert.Equal(t, tempDir, sfs.BaseDir())
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
	assert.True(t, sfs.ExistsNoErr(testFile), "ExistsNoErr should return true for existing file")

	// Should return false for non-existent file
	assert.False(t, sfs.ExistsNoErr(filepath.Join(tempDir, "nonexistent.txt")), "ExistsNoErr should return false for non-existent file")

	// Should return false for invalid path (traversal)
	assert.False(t, sfs.ExistsNoErr(filepath.Join(tempDir, "..", "outside.txt")), "ExistsNoErr should return false for path traversal")
}

// TestGetMaxReadFileSize verifies the getter method
func TestGetMaxReadFileSize(t *testing.T) {
	t.Parallel()
	sfs, _ := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	// Default should be 0
	assert.Equal(t, int64(0), sfs.GetMaxReadFileSize(), "Expected default 0")

	// Set and verify
	sfs.SetMaxReadFileSize(1024)
	assert.Equal(t, int64(1024), sfs.GetMaxReadFileSize())
}

// TestFollowingEscapingSymlinkFails verifies that while Readlink returns the target,
// actually trying to FOLLOW an escaping symlink via Open/Stat will fail
func TestFollowingEscapingSymlinkFails(t *testing.T) {
	t.Parallel()
	sfs, tempDir := setupSecureFS(t)
	t.Cleanup(func() { _ = sfs.Close() })

	// Create a symlink with a relative path that would escape the sandbox
	symlinkPath := filepath.Join(tempDir, "escape.txt")
	err := os.Symlink("../../etc/passwd", symlinkPath)
	require.NoError(t, err, "Failed to create symlink")

	// Readlink should succeed (returns the target string)
	target, err := sfs.Readlink(symlinkPath)
	require.NoError(t, err, "Readlink failed")
	assert.Equal(t, "../../etc/passwd", target)

	// But trying to Open the symlink should fail because it escapes the sandbox
	// The security is enforced when following the link, not when reading its target
	_, err = sfs.Open(symlinkPath)
	assert.Error(t, err, "Open should have failed for symlink escaping sandbox")
}
