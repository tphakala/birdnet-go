package logger

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBufferedFileWriter_Write(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "test.log")

	// Create writer with short flush interval for testing
	writer, err := NewBufferedFileWriter(logPath, WithFlushInterval(100*time.Millisecond))
	require.NoError(t, err)
	defer func() { _ = writer.Close() }()

	// Write some data
	testData := "Hello, buffered world!\n"
	n, err := writer.Write([]byte(testData))
	require.NoError(t, err)
	assert.Equal(t, len(testData), n)

	// Data should be buffered, not on disk yet
	assert.Positive(t, writer.Buffered())

	// Flush and verify
	err = writer.Flush()
	require.NoError(t, err)
	assert.Equal(t, 0, writer.Buffered())

	// Read file and verify content
	content, err := os.ReadFile(logPath) //nolint:gosec // test file path from t.TempDir()
	require.NoError(t, err)
	assert.Equal(t, testData, string(content))
}

func TestBufferedFileWriter_AutoFlush(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "autoflush.log")

	// Create writer with very short flush interval
	writer, err := NewBufferedFileWriter(logPath, WithFlushInterval(50*time.Millisecond))
	require.NoError(t, err)
	defer func() { _ = writer.Close() }()

	// Write data
	testData := "Auto-flush test data\n"
	_, err = writer.Write([]byte(testData))
	require.NoError(t, err)

	// Data should be buffered initially
	assert.Positive(t, writer.Buffered())

	// Wait for auto-flush
	time.Sleep(100 * time.Millisecond)

	// Buffer should be flushed
	assert.Equal(t, 0, writer.Buffered())

	// Verify content on disk
	content, err := os.ReadFile(logPath) //nolint:gosec // test file path from t.TempDir()
	require.NoError(t, err)
	assert.Equal(t, testData, string(content))
}

func TestBufferedFileWriter_Close(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "close.log")

	writer, err := NewBufferedFileWriter(logPath)
	require.NoError(t, err)

	// Write data
	testData := "Data before close\n"
	_, err = writer.Write([]byte(testData))
	require.NoError(t, err)

	// Close should flush and sync
	err = writer.Close()
	require.NoError(t, err)

	// Verify content on disk
	content, err := os.ReadFile(logPath) //nolint:gosec // test file path from t.TempDir()
	require.NoError(t, err)
	assert.Equal(t, testData, string(content))

	// Write after close should fail
	_, err = writer.Write([]byte("should fail"))
	assert.Error(t, err)
}

func TestBufferedFileWriter_ConcurrentWrites(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "concurrent.log")

	writer, err := NewBufferedFileWriter(logPath, WithFlushInterval(100*time.Millisecond))
	require.NoError(t, err)
	defer func() { _ = writer.Close() }()

	// Concurrent writes from multiple goroutines
	const numGoroutines = 10
	const writesPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for range numGoroutines {
		go func() {
			defer wg.Done()
			for range writesPerGoroutine {
				data := []byte("goroutine write\n")
				_, err := writer.Write(data)
				assert.NoError(t, err)
			}
		}()
	}

	wg.Wait()

	// Flush and close
	err = writer.Close()
	require.NoError(t, err)

	// Verify all writes made it to disk
	content, err := os.ReadFile(logPath) //nolint:gosec // test file path from t.TempDir()
	require.NoError(t, err)

	expectedLines := numGoroutines * writesPerGoroutine
	actualLines := strings.Count(string(content), "\n")
	assert.Equal(t, expectedLines, actualLines)
}

func TestBufferedFileWriter_CustomBufferSize(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "custom_buffer.log")

	// Create with small buffer
	customSize := 1024
	writer, err := NewBufferedFileWriter(logPath, WithBufferSize(customSize))
	require.NoError(t, err)
	defer func() { _ = writer.Close() }()

	// Verify it was created successfully
	assert.NotNil(t, writer)
	assert.Equal(t, logPath, writer.FilePath())
}

func TestBufferedFileWriter_Sync(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "sync.log")

	writer, err := NewBufferedFileWriter(logPath)
	require.NoError(t, err)
	defer func() { _ = writer.Close() }()

	// Write data
	testData := "Sync test data\n"
	_, err = writer.Write([]byte(testData))
	require.NoError(t, err)

	// Sync should flush buffer AND fsync to disk
	err = writer.Sync()
	require.NoError(t, err)

	// Verify content
	content, err := os.ReadFile(logPath) //nolint:gosec // test file path from t.TempDir()
	require.NoError(t, err)
	assert.Equal(t, testData, string(content))
}

func TestBufferedFileWriter_FilePath(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "filepath.log")

	writer, err := NewBufferedFileWriter(logPath)
	require.NoError(t, err)
	defer func() { _ = writer.Close() }()

	assert.Equal(t, logPath, writer.FilePath())
}

func TestBufferedFileWriter_AppendMode(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "append.log")

	// First write
	writer1, err := NewBufferedFileWriter(logPath)
	require.NoError(t, err)
	_, err = writer1.Write([]byte("first\n"))
	require.NoError(t, err)
	err = writer1.Close()
	require.NoError(t, err)

	// Second write (should append)
	writer2, err := NewBufferedFileWriter(logPath)
	require.NoError(t, err)
	_, err = writer2.Write([]byte("second\n"))
	require.NoError(t, err)
	err = writer2.Close()
	require.NoError(t, err)

	// Verify both lines exist
	content, err := os.ReadFile(logPath) //nolint:gosec // test file path from t.TempDir()
	require.NoError(t, err)
	assert.Equal(t, "first\nsecond\n", string(content))
}

func TestNewBufferedFileWriter_InvalidPath(t *testing.T) {
	t.Parallel()

	// Try to create writer in non-existent directory
	_, err := NewBufferedFileWriter("/nonexistent/path/test.log")
	assert.Error(t, err)
}

func TestBufferedFileWriter_FromFile(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "fromfile.log")

	// Create file manually
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // test file path from t.TempDir()
	require.NoError(t, err)

	// Create buffered writer from existing file
	writer := NewBufferedFileWriterFromFile(file, WithFlushInterval(100*time.Millisecond))
	require.NotNil(t, writer)

	// Write and close
	_, err = writer.Write([]byte("from file test\n"))
	require.NoError(t, err)
	err = writer.Close()
	require.NoError(t, err)

	// Verify content
	content, err := os.ReadFile(logPath) //nolint:gosec // test file path from t.TempDir()
	require.NoError(t, err)
	assert.Equal(t, "from file test\n", string(content))
}

func BenchmarkBufferedFileWriter_Write(b *testing.B) {
	tempDir := b.TempDir()
	logPath := filepath.Join(tempDir, "bench.log")

	writer, err := NewBufferedFileWriter(logPath)
	require.NoError(b, err)
	defer func() { _ = writer.Close() }()

	data := []byte(`{"time":"2025-01-12T10:30:00Z","level":"INFO","msg":"Benchmark log message","module":"test","count":42}` + "\n")

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		_, err := writer.Write(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBufferedFileWriter_WriteParallel(b *testing.B) {
	tempDir := b.TempDir()
	logPath := filepath.Join(tempDir, "bench_parallel.log")

	writer, err := NewBufferedFileWriter(logPath)
	require.NoError(b, err)
	defer func() { _ = writer.Close() }()

	data := []byte(`{"time":"2025-01-12T10:30:00Z","level":"INFO","msg":"Benchmark log message","module":"test","count":42}` + "\n")

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := writer.Write(data)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkUnbufferedWrite compares against direct file writes
func BenchmarkUnbufferedWrite(b *testing.B) {
	tempDir := b.TempDir()
	logPath := filepath.Join(tempDir, "unbuffered.log")

	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // test file path from b.TempDir()
	require.NoError(b, err)
	defer func() { _ = file.Close() }()

	data := []byte(`{"time":"2025-01-12T10:30:00Z","level":"INFO","msg":"Benchmark log message","module":"test","count":42}` + "\n")

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		_, err := file.Write(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}
