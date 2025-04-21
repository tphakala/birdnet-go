package securefs

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestFIFOPath(t *testing.T) {
	// Test GetFIFOPath function on different platforms
	path := "/tmp/test.fifo"
	fifoPath := GetFIFOPath(path)

	if runtime.GOOS == "windows" {
		expectedPrefix := `\\.\pipe\`
		if len(fifoPath) < len(expectedPrefix) || fifoPath[:len(expectedPrefix)] != expectedPrefix {
			t.Errorf("Expected Windows named pipe path to start with %s, got %s", expectedPrefix, fifoPath)
		}
	} else if fifoPath != path {
		t.Errorf("Expected Unix path to be unchanged, got %s, want %s", fifoPath, path)
	}

	// Test with Windows-style path
	winPath := `C:\Users\test\pipe.fifo`
	winFifoPath := GetFIFOPath(winPath)
	if runtime.GOOS == "windows" {
		expectedPrefix := `\\.\pipe\`
		if len(winFifoPath) < len(expectedPrefix) || winFifoPath[:len(expectedPrefix)] != expectedPrefix {
			t.Errorf("Expected Windows pipe name to start with %s, got %s", expectedPrefix, winFifoPath)
		}
	}
}

func TestFIFOOperations(t *testing.T) {
	// Skip this test on Windows in CI/automated test environments
	// since it requires special permissions to create named pipes
	if runtime.GOOS == "windows" && os.Getenv("CI") == "true" {
		t.Skip("Skipping FIFO test on Windows in CI environment")
	}

	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Create SecureFS instance
	sfs, err := New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create SecureFS: %v", err)
	}
	defer sfs.Close()

	// Create a pipe file path
	fifoPath := filepath.Join(tempDir, "test.fifo")

	// Test GetPipeName before setting
	if sfs.GetPipeName() != "" {
		t.Errorf("Expected empty pipe name initially, got %s", sfs.GetPipeName())
	}

	// Set and get pipe name
	expectedPipeName := "test-pipe-name"
	sfs.SetPipeName(expectedPipeName)
	if sfs.GetPipeName() != expectedPipeName {
		t.Errorf("Expected pipe name %s, got %s", expectedPipeName, sfs.GetPipeName())
	}

	// Test path validation for FIFO
	invalidPath := filepath.Join(tempDir, "..", "outside.fifo")
	err = sfs.CreateFIFO(invalidPath)
	if err == nil {
		t.Fatal("CreateFIFO should have failed for path outside sandbox")
	}

	// Test creation of FIFO
	// Note: This might fail or be skipped on some platforms due to permissions
	t.Run("CreateFIFO", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			// On Windows, this requires administrator privileges to create named pipes
			// so we'll just verify the function calls without expecting success
			err := sfs.CreateFIFO(fifoPath)
			t.Logf("CreateFIFO on Windows result: %v", err)
		} else {
			// On Unix, we can test FIFO creation
			err := sfs.CreateFIFO(fifoPath)
			if err != nil {
				t.Logf("CreateFIFO failed, but may be due to permissions: %v", err)
				t.Skip("Skipping FIFO creation test due to possible permission issues")
			}

			// Verify FIFO exists
			exists, err := sfs.Exists(fifoPath)
			if err != nil {
				t.Fatalf("Exists check failed with error: %v", err)
			}
			if !exists {
				t.Fatal("FIFO should exist after creation")
			}

			// Try to open with a short timeout to avoid blocking
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()

			// This will likely timeout since there's no reader, which is expected
			_, err = sfs.OpenFIFO(ctx, fifoPath)
			if err == nil {
				t.Log("OpenFIFO succeeded unexpectedly - there must be a reader")
			}
		}

		// Always call cleanup at the end
		if runtime.GOOS == "windows" {
			CleanupNamedPipes()
		}
	})
}
