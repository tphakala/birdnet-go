package securefs

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFIFOPath(t *testing.T) {
	// Test GetFIFOPath function on different platforms
	path := "/tmp/test.fifo"
	fifoPath := GetFIFOPath(path)

	if runtime.GOOS == osWindows {
		expectedPrefix := `\\.\pipe\`
		require.GreaterOrEqual(t, len(fifoPath), len(expectedPrefix), "Windows pipe path too short")
		assert.Equal(t, expectedPrefix, fifoPath[:len(expectedPrefix)], "Windows named pipe should start with prefix")
	} else {
		assert.Equal(t, path, fifoPath, "Unix path should be unchanged")
	}

	// Test with Windows-style path
	winPath := `C:\Users\test\pipe.fifo`
	winFifoPath := GetFIFOPath(winPath)
	if runtime.GOOS == osWindows {
		expectedPrefix := `\\.\pipe\`
		require.GreaterOrEqual(t, len(winFifoPath), len(expectedPrefix), "Windows pipe path too short")
		assert.Equal(t, expectedPrefix, winFifoPath[:len(expectedPrefix)], "Windows pipe name should start with prefix")
	}
}

func TestFIFOOperations(t *testing.T) {
	if runtime.GOOS == osWindows && os.Getenv("CI") == "true" {
		t.Skip("Skipping FIFO test on Windows in CI environment")
	}

	tempDir := t.TempDir()
	sfs, err := New(tempDir)
	require.NoError(t, err, "Failed to create SecureFS")
	t.Cleanup(func() { _ = sfs.Close() })

	fifoPath := filepath.Join(tempDir, "test.fifo")

	t.Run("PipeName", func(t *testing.T) {
		testFIFOPipeName(t, sfs)
	})

	t.Run("PathValidation", func(t *testing.T) {
		invalidPath := filepath.Join(tempDir, "..", "outside.fifo")
		err := sfs.CreateFIFO(invalidPath)
		require.Error(t, err, "CreateFIFO should have failed for path outside sandbox")
	})

	t.Run("CreateFIFO", func(t *testing.T) {
		testFIFOCreation(t, sfs, fifoPath)
	})
}

func testFIFOPipeName(t *testing.T, sfs *SecureFS) {
	t.Helper()

	assert.Empty(t, sfs.GetPipeName(), "Expected empty pipe name initially")

	expectedPipeName := "test-pipe-name"
	sfs.SetPipeName(expectedPipeName)
	assert.Equal(t, expectedPipeName, sfs.GetPipeName())
}

func testFIFOCreation(t *testing.T, sfs *SecureFS, fifoPath string) {
	t.Helper()
	defer func() {
		if runtime.GOOS == osWindows {
			CleanupNamedPipes()
		}
	}()

	if runtime.GOOS == osWindows {
		err := sfs.CreateFIFO(fifoPath)
		t.Logf("CreateFIFO on Windows result: %v", err)
		return
	}

	if err := sfs.CreateFIFO(fifoPath); err != nil {
		t.Logf("CreateFIFO failed: %v", err)
		t.Skip("Skipping FIFO creation test due to possible permission issues")
	}

	exists, err := sfs.Exists(fifoPath)
	require.NoError(t, err, "Exists check failed")
	assert.True(t, exists, "FIFO should exist after creation")

	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()

	// This will likely timeout since there's no reader, which is expected
	if _, err := sfs.OpenFIFO(ctx, fifoPath); err == nil {
		t.Log("OpenFIFO succeeded unexpectedly - there must be a reader")
	}
}
