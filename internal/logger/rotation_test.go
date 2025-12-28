package logger

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRotationConfig_IsEnabled(t *testing.T) {
	tests := []struct {
		name     string
		config   RotationConfig
		expected bool
	}{
		{
			name:     "enabled when MaxSize > 0",
			config:   RotationConfig{MaxSize: 1024},
			expected: true,
		},
		{
			name:     "disabled when MaxSize = 0",
			config:   RotationConfig{MaxSize: 0},
			expected: false,
		},
		{
			name:     "disabled for empty config",
			config:   RotationConfig{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.config.IsEnabled())
		})
	}
}

func TestRotationConfigFromFileOutput(t *testing.T) {
	tests := []struct {
		name     string
		input    *FileOutput
		expected RotationConfig
	}{
		{
			name:     "nil FileOutput returns empty config",
			input:    nil,
			expected: RotationConfig{},
		},
		{
			name: "converts MB to bytes",
			input: &FileOutput{
				MaxSize:         100,
				MaxAge:          30,
				MaxRotatedFiles: 10,
				Compress:        true,
			},
			expected: RotationConfig{
				MaxSize:         100 * bytesPerMB,
				MaxAge:          30,
				MaxRotatedFiles: 10,
				Compress:        true,
			},
		},
		{
			name: "handles zero values",
			input: &FileOutput{
				MaxSize:         0,
				MaxAge:          0,
				MaxRotatedFiles: 0,
				Compress:        false,
			},
			expected: RotationConfig{
				MaxSize:         0,
				MaxAge:          0,
				MaxRotatedFiles: 0,
				Compress:        false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RotationConfigFromFileOutput(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRotationConfigFromModuleOutput(t *testing.T) {
	defaultFo := &FileOutput{
		MaxSize:         100,
		MaxAge:          30,
		MaxRotatedFiles: 10,
		Compress:        true,
	}

	tests := []struct {
		name      string
		module    *ModuleOutput
		defaultFo *FileOutput
		expected  RotationConfig
	}{
		{
			name:      "nil module uses FileOutput defaults",
			module:    nil,
			defaultFo: defaultFo,
			expected: RotationConfig{
				MaxSize:         100 * bytesPerMB,
				MaxAge:          30,
				MaxRotatedFiles: 10,
				Compress:        true,
			},
		},
		{
			name: "module overrides defaults",
			module: &ModuleOutput{
				MaxSize:         50,
				MaxAge:          15,
				MaxRotatedFiles: 5,
				Compress:        false,
			},
			defaultFo: defaultFo,
			expected: RotationConfig{
				MaxSize:         50 * bytesPerMB,
				MaxAge:          15,
				MaxRotatedFiles: 5,
				Compress:        false,
			},
		},
		{
			name: "zero module values fall back to defaults",
			module: &ModuleOutput{
				MaxSize:         0, // Should use default
				MaxAge:          0, // Should use default
				MaxRotatedFiles: 0, // Should use default
				Compress:        false,
			},
			defaultFo: defaultFo,
			expected: RotationConfig{
				MaxSize:         100 * bytesPerMB,
				MaxAge:          30,
				MaxRotatedFiles: 10,
				Compress:        false, // Module's explicit false
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RotationConfigFromModuleOutput(tt.module, tt.defaultFo)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRotationManager_rotatedFilePath(t *testing.T) {
	rm := &RotationManager{
		filePath: "/logs/application.log",
	}

	timestamp := "2025-01-15T14-30-05Z"
	result := rm.rotatedFilePath(timestamp)

	assert.Equal(t, "/logs/application-2025-01-15T14-30-05Z.log", result)
}

func TestRotationManager_rotatedFilePattern(t *testing.T) {
	rm := &RotationManager{
		filePath: "/logs/application.log",
	}

	result := rm.rotatedFilePattern()

	assert.Equal(t, "/logs/application-*Z.log", result)
}

func TestRotationManager_CheckAndRotate_SizeBasedRotation(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "test.log")

	// Create a small MaxSize for testing (1KB)
	config := RotationConfig{
		MaxSize:         1024, // 1KB
		MaxAge:          30,
		MaxRotatedFiles: 5,
		Compress:        false,
	}

	// Create writer with rotation
	writer, err := NewBufferedFileWriter(logPath, WithRotation(config))
	require.NoError(t, err)
	defer func() { _ = writer.Close() }()

	// Write data exceeding MaxSize
	data := strings.Repeat("x", 1500) // 1.5KB
	_, err = writer.Write([]byte(data))
	require.NoError(t, err)
	require.NoError(t, writer.Flush())

	// Manually trigger rotation check (normally happens on flush interval)
	if writer.rotation != nil {
		writer.rotation.CheckAndRotate()
	}

	// Wait a bit for async operations
	time.Sleep(100 * time.Millisecond)

	// Check that rotated file exists
	files, err := filepath.Glob(filepath.Join(tempDir, "test-*Z.log"))
	require.NoError(t, err)
	assert.Len(t, files, 1, "should have one rotated file")

	// Original file should be empty or small (new file after rotation)
	info, err := os.Stat(logPath)
	require.NoError(t, err)
	assert.Less(t, info.Size(), int64(100), "new log file should be small")
}

func TestRotationManager_Compression(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "test.log")

	config := RotationConfig{
		MaxSize:         512, // 512 bytes for quick rotation
		MaxAge:          30,
		MaxRotatedFiles: 5,
		Compress:        true,
	}

	writer, err := NewBufferedFileWriter(logPath, WithRotation(config))
	require.NoError(t, err)
	defer func() { _ = writer.Close() }()

	// Write data exceeding MaxSize
	data := strings.Repeat("test data for compression ", 50) // ~1.4KB
	_, err = writer.Write([]byte(data))
	require.NoError(t, err)
	require.NoError(t, writer.Flush())

	// Trigger rotation
	if writer.rotation != nil {
		writer.rotation.CheckAndRotate()
	}

	// Wait for async compression
	time.Sleep(500 * time.Millisecond)

	// Check for compressed file
	gzFiles, err := filepath.Glob(filepath.Join(tempDir, "test-*Z.log.gz"))
	require.NoError(t, err)
	assert.Len(t, gzFiles, 1, "should have one compressed file")

	// Verify it's a valid gzip file
	if len(gzFiles) > 0 {
		f, err := os.Open(gzFiles[0])
		require.NoError(t, err)
		defer func() { _ = f.Close() }()

		gz, err := gzip.NewReader(f)
		require.NoError(t, err)
		defer func() { _ = gz.Close() }()

		content, err := io.ReadAll(gz)
		require.NoError(t, err)
		assert.Contains(t, string(content), "test data for compression")
	}
}

func TestRotationManager_Cleanup_MaxRotatedFiles(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "test.log")

	// Create some old rotated files
	for i := 1; i <= 5; i++ {
		oldFile := filepath.Join(tempDir, "test-2025-01-0"+string(rune('0'+i))+"T10-00-00Z.log")
		require.NoError(t, os.WriteFile(oldFile, []byte("old"), 0o600))
		// Set modification time to make them old
		oldTime := time.Now().Add(-time.Duration(i) * 24 * time.Hour)
		require.NoError(t, os.Chtimes(oldFile, oldTime, oldTime))
	}

	config := RotationConfig{
		MaxSize:         512,
		MaxAge:          0, // No age limit
		MaxRotatedFiles: 3, // Keep only 3
		Compress:        false,
	}

	writer, err := NewBufferedFileWriter(logPath, WithRotation(config))
	require.NoError(t, err)
	defer func() { _ = writer.Close() }()

	// Write data to trigger rotation
	_, err = writer.Write([]byte(strings.Repeat("x", 600)))
	require.NoError(t, err)
	require.NoError(t, writer.Flush())

	// Trigger rotation and cleanup
	if writer.rotation != nil {
		writer.rotation.CheckAndRotate()
	}

	time.Sleep(100 * time.Millisecond)

	// Count remaining rotated files
	files, err := filepath.Glob(filepath.Join(tempDir, "test-*Z.log"))
	require.NoError(t, err)

	// Should have at most MaxRotatedFiles
	assert.LessOrEqual(t, len(files), 3, "should have at most 3 rotated files")
}

func TestRotationManager_Cleanup_MaxAge(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "test.log")

	// Create an old file (older than MaxAge)
	oldFile := filepath.Join(tempDir, "test-2025-01-01T10-00-00Z.log")
	require.NoError(t, os.WriteFile(oldFile, []byte("old"), 0o600))
	oldTime := time.Now().Add(-40 * 24 * time.Hour) // 40 days old
	require.NoError(t, os.Chtimes(oldFile, oldTime, oldTime))

	// Create a recent file
	recentFile := filepath.Join(tempDir, "test-2025-01-20T10-00-00Z.log")
	require.NoError(t, os.WriteFile(recentFile, []byte("recent"), 0o600))

	config := RotationConfig{
		MaxSize:         512,
		MaxAge:          30, // 30 days
		MaxRotatedFiles: 0,  // No count limit
		Compress:        false,
	}

	writer, err := NewBufferedFileWriter(logPath, WithRotation(config))
	require.NoError(t, err)
	defer func() { _ = writer.Close() }()

	// Write data to trigger rotation
	_, err = writer.Write([]byte(strings.Repeat("x", 600)))
	require.NoError(t, err)
	require.NoError(t, writer.Flush())

	// Trigger rotation and cleanup
	if writer.rotation != nil {
		writer.rotation.CheckAndRotate()
	}

	time.Sleep(100 * time.Millisecond)

	// Old file should be deleted
	_, err = os.Stat(oldFile)
	assert.True(t, os.IsNotExist(err), "old file should be deleted")

	// Recent file should still exist
	_, err = os.Stat(recentFile)
	assert.NoError(t, err, "recent file should still exist")
}

func TestRotationManager_ConcurrentWrites(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "test.log")

	config := RotationConfig{
		MaxSize:         2048, // 2KB
		MaxAge:          30,
		MaxRotatedFiles: 10,
		Compress:        false,
	}

	writer, err := NewBufferedFileWriter(logPath, WithRotation(config))
	require.NoError(t, err)
	defer func() { _ = writer.Close() }()

	// Write concurrently from multiple goroutines
	var wg sync.WaitGroup
	numWriters := 10
	writesPerWriter := 50

	for i := range numWriters {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range writesPerWriter {
				data := strings.Repeat("x", 50) // 50 bytes per write
				_, err := writer.Write([]byte(data))
				if err != nil {
					t.Logf("writer %d, write %d error: %v", id, j, err)
				}
			}
		}(i)
	}

	// Also trigger rotation checks periodically
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if writer.rotation != nil {
					writer.rotation.CheckAndRotate()
				}
			}
		}
	}()

	wg.Wait()
	close(done)

	// Flush and close
	require.NoError(t, writer.Flush())

	// Verify no panics occurred and files exist
	files, err := filepath.Glob(filepath.Join(tempDir, "test*.log"))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(files), 1, "should have at least the main log file")
}

func TestBufferedFileWriter_SwapFile(t *testing.T) {
	tempDir := t.TempDir()
	origPath := filepath.Join(tempDir, "original.log")
	newPath := filepath.Join(tempDir, "new.log")

	// Create original writer
	writer, err := NewBufferedFileWriter(origPath)
	require.NoError(t, err)
	defer func() { _ = writer.Close() }()

	// Write some data
	_, err = writer.Write([]byte("original data"))
	require.NoError(t, err)

	// Create new file for swap
	newFile, err := os.OpenFile(newPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // test file
	require.NoError(t, err)

	// Swap
	oldFile, err := writer.SwapFile(newFile)
	require.NoError(t, err)
	require.NotNil(t, oldFile)
	_ = oldFile.Close()

	// Write to new file
	_, err = writer.Write([]byte("new data"))
	require.NoError(t, err)
	require.NoError(t, writer.Flush())

	// Verify original file has original data (was flushed before swap)
	origContent, err := os.ReadFile(origPath) //nolint:gosec // test file
	require.NoError(t, err)
	assert.Equal(t, "original data", string(origContent))

	// Verify new file has new data
	newContent, err := os.ReadFile(newPath) //nolint:gosec // test file
	require.NoError(t, err)
	assert.Equal(t, "new data", string(newContent))
}

func TestBufferedFileWriter_SwapFile_Errors(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "test.log")

	writer, err := NewBufferedFileWriter(logPath)
	require.NoError(t, err)

	// Test nil file
	_, err = writer.SwapFile(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil")

	// Close writer
	_ = writer.Close()

	// Test swap on closed writer
	newFile, _ := os.CreateTemp(tempDir, "new*.log")
	defer func() { _ = newFile.Close() }()
	_, err = writer.SwapFile(newFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "closed")
}

func TestRotationManager_Close(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "test.log")

	config := RotationConfig{
		MaxSize:         1024,
		MaxAge:          30,
		MaxRotatedFiles: 5,
		Compress:        false,
	}

	writer, err := NewBufferedFileWriter(logPath, WithRotation(config))
	require.NoError(t, err)

	// Get rotation manager
	rm := writer.rotation
	require.NotNil(t, rm)

	// Close writer (which closes rotation manager)
	require.NoError(t, writer.Close())

	// CheckAndRotate should be a no-op after close
	rm.CheckAndRotate() // Should not panic

	// Closing again should be safe
	require.NoError(t, writer.Close())
}

func TestWithRotation_DisabledWhenMaxSizeZero(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "test.log")

	config := RotationConfig{
		MaxSize: 0, // Disabled
	}

	writer, err := NewBufferedFileWriter(logPath, WithRotation(config))
	require.NoError(t, err)
	defer func() { _ = writer.Close() }()

	// Rotation should not be set when disabled
	assert.Nil(t, writer.rotation)
}
