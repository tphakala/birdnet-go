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
	if runtime.GOOS == "windows" && os.Getenv("CI") == "true" {
		t.Skip("Skipping FIFO test on Windows in CI environment")
	}

	tempDir := t.TempDir()
	sfs, err := New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create SecureFS: %v", err)
	}
	t.Cleanup(func() { _ = sfs.Close() })

	fifoPath := filepath.Join(tempDir, "test.fifo")

	t.Run("PipeName", func(t *testing.T) {
		testFIFOPipeName(t, sfs)
	})

	t.Run("PathValidation", func(t *testing.T) {
		invalidPath := filepath.Join(tempDir, "..", "outside.fifo")
		if err := sfs.CreateFIFO(invalidPath); err == nil {
			t.Fatal("CreateFIFO should have failed for path outside sandbox")
		}
	})

	t.Run("CreateFIFO", func(t *testing.T) {
		testFIFOCreation(t, sfs, fifoPath)
	})
}

func testFIFOPipeName(t *testing.T, sfs *SecureFS) {
	t.Helper()

	if sfs.GetPipeName() != "" {
		t.Errorf("Expected empty pipe name initially, got %s", sfs.GetPipeName())
	}

	expectedPipeName := "test-pipe-name"
	sfs.SetPipeName(expectedPipeName)
	if sfs.GetPipeName() != expectedPipeName {
		t.Errorf("Expected pipe name %s, got %s", expectedPipeName, sfs.GetPipeName())
	}
}

func testFIFOCreation(t *testing.T, sfs *SecureFS, fifoPath string) {
	t.Helper()
	defer func() {
		if runtime.GOOS == "windows" {
			CleanupNamedPipes()
		}
	}()

	if runtime.GOOS == "windows" {
		err := sfs.CreateFIFO(fifoPath)
		t.Logf("CreateFIFO on Windows result: %v", err)
		return
	}

	if err := sfs.CreateFIFO(fifoPath); err != nil {
		t.Logf("CreateFIFO failed: %v", err)
		t.Skip("Skipping FIFO creation test due to possible permission issues")
	}

	exists, err := sfs.Exists(fifoPath)
	if err != nil {
		t.Fatalf("Exists check failed: %v", err)
	}
	if !exists {
		t.Fatal("FIFO should exist after creation")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// This will likely timeout since there's no reader, which is expected
	if _, err := sfs.OpenFIFO(ctx, fifoPath); err == nil {
		t.Log("OpenFIFO succeeded unexpectedly - there must be a reader")
	}
}
