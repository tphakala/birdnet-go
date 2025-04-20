package securefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSecureFS(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir() // Use t.TempDir() for automatic cleanup

	// Create a SecureFS instance
	sfs, err := New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create SecureFS: %v", err)
	}
	defer sfs.Close()

	t.Run("FileOperations", func(t *testing.T) {
		t.Parallel()

		// Test file operations
		testFile := filepath.Join(tempDir, "test.txt")
		testData := []byte("test data")

		// Test WriteFile
		err := sfs.WriteFile(testFile, testData, 0o644)
		if err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		// Test Exists
		if !sfs.Exists(testFile) {
			t.Fatal("Exists failed: file should exist")
		}

		// Test ReadFile
		data, err := sfs.ReadFile(testFile)
		if err != nil {
			t.Fatalf("ReadFile failed: %v", err)
		}
		if string(data) != string(testData) {
			t.Fatalf("ReadFile returned wrong data: got %q, want %q", string(data), string(testData))
		}

		// Test Stat
		info, err := sfs.Stat(testFile)
		if err != nil {
			t.Fatalf("Stat failed: %v", err)
		}
		if info.Size() != int64(len(testData)) {
			t.Fatalf("Stat returned wrong size: got %d, want %d", info.Size(), len(testData))
		}

		// Test OpenFile (read)
		file, err := sfs.OpenFile(testFile, os.O_RDONLY, 0)
		if err != nil {
			t.Fatalf("OpenFile failed: %v", err)
		}
		file.Close()

		// Test Remove
		err = sfs.Remove(testFile)
		if err != nil {
			t.Fatalf("Remove failed: %v", err)
		}
		if sfs.Exists(testFile) {
			t.Fatal("Remove failed: file should not exist")
		}
	})

	t.Run("DirectoryOperations", func(t *testing.T) {
		t.Parallel()

		// Test MkdirAll
		testDir := filepath.Join(tempDir, "subdir", "nested")
		err := sfs.MkdirAll(testDir, 0o755)
		if err != nil {
			t.Fatalf("MkdirAll failed: %v", err)
		}
		if !sfs.Exists(testDir) {
			t.Fatal("MkdirAll failed: directory should exist")
		}

		// Test file in nested directory
		nestedFile := filepath.Join(testDir, "nested.txt")
		nestedData := []byte("nested file data")
		err = sfs.WriteFile(nestedFile, nestedData, 0o644)
		if err != nil {
			t.Fatalf("WriteFile in nested dir failed: %v", err)
		}

		// Test RemoveAll
		err = sfs.RemoveAll(filepath.Join(tempDir, "subdir"))
		if err != nil {
			t.Fatalf("RemoveAll failed: %v", err)
		}
		if sfs.Exists(testDir) {
			t.Fatal("RemoveAll failed: directory should not exist")
		}
	})

	t.Run("PathTraversalPrevention", func(t *testing.T) {
		t.Parallel()

		// Test path traversal prevention
		traversalPath := filepath.Join(tempDir, "..", "outside.txt")
		_, err := sfs.relativePath(traversalPath)
		if err == nil {
			t.Fatal("relativePath should have failed on traversal path")
		}

		// Attempt to write outside the sandbox
		err = sfs.WriteFile(traversalPath, []byte("should fail"), 0o644)
		if err == nil {
			t.Fatal("WriteFile should have failed for path outside sandbox")
		}

		// Attempt to read outside the sandbox
		_, err = sfs.ReadFile(traversalPath)
		if err == nil {
			t.Fatal("ReadFile should have failed for path outside sandbox")
		}
	})

	t.Run("PathWithinBaseCheck", func(t *testing.T) {
		t.Parallel()

		// Test IsPathWithinBase
		validPath := filepath.Join(tempDir, "file.txt")
		isWithin, err := IsPathWithinBase(tempDir, validPath)
		if err != nil {
			t.Fatalf("IsPathWithinBase failed: %v", err)
		}
		if !isWithin {
			t.Fatal("IsPathWithinBase failed: valid path should be within base")
		}

		invalidPath := filepath.Join(tempDir, "..", "outside.txt")
		isWithin, err = IsPathWithinBase(tempDir, invalidPath)
		if err != nil {
			t.Fatalf("IsPathWithinBase failed: %v", err)
		}
		if isWithin {
			t.Fatal("IsPathWithinBase failed: invalid path should not be within base")
		}

		// Test with same path
		isWithin, err = IsPathWithinBase(tempDir, tempDir)
		if err != nil {
			t.Fatalf("IsPathWithinBase failed with same path: %v", err)
		}
		if !isWithin {
			t.Fatal("IsPathWithinBase failed: same path should be within base")
		}
	})

	t.Run("IsPathValidWithinBase", func(t *testing.T) {
		t.Parallel()

		// Test IsPathValidWithinBase
		validPath := filepath.Join(tempDir, "valid.txt")
		err := IsPathValidWithinBase(tempDir, validPath)
		if err != nil {
			t.Fatalf("IsPathValidWithinBase failed for valid path: %v", err)
		}

		invalidPath := filepath.Join(tempDir, "..", "outside.txt")
		err = IsPathValidWithinBase(tempDir, invalidPath)
		if err == nil {
			t.Fatal("IsPathValidWithinBase should have failed for invalid path")
		}
	})
}
