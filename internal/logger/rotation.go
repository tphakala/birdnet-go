package logger

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Rotation constants
const (
	// rotationTimestampFormat is the UTC timestamp format for rotated files.
	// Uses dashes instead of colons for Windows compatibility.
	rotationTimestampFormat = "2006-01-02T15-04-05Z"

	// retryInterval is how often to retry file logging after disk-full fallback.
	retryInterval = 30 * time.Second

	// bytesPerMB converts megabytes to bytes.
	bytesPerMB = 1024 * 1024
)

// RotationConfig holds rotation settings for a log file.
type RotationConfig struct {
	MaxSize         int64 // Max size in bytes before rotation (0 = disabled)
	MaxAge          int   // Days to keep rotated files (0 = no limit)
	MaxRotatedFiles int   // Max number of rotated files (0 = no limit)
	Compress        bool  // Gzip rotated files
}

// RotationConfigFromFileOutput creates a RotationConfig from FileOutput settings.
func RotationConfigFromFileOutput(fo *FileOutput) RotationConfig {
	if fo == nil {
		return RotationConfig{}
	}
	return RotationConfig{
		MaxSize:         int64(fo.MaxSize) * bytesPerMB,
		MaxAge:          fo.MaxAge,
		MaxRotatedFiles: fo.MaxRotatedFiles,
		Compress:        fo.Compress,
	}
}

// RotationConfigFromModuleOutput creates a RotationConfig from ModuleOutput settings.
// If module settings are zero, it falls back to the provided FileOutput defaults.
func RotationConfigFromModuleOutput(mo *ModuleOutput, defaultFo *FileOutput) RotationConfig {
	if mo == nil {
		return RotationConfigFromFileOutput(defaultFo)
	}

	// Use module values if set, otherwise fall back to FileOutput defaults
	maxSize := mo.MaxSize
	if maxSize == 0 && defaultFo != nil {
		maxSize = defaultFo.MaxSize
	}

	maxAge := mo.MaxAge
	if maxAge == 0 && defaultFo != nil {
		maxAge = defaultFo.MaxAge
	}

	maxRotatedFiles := mo.MaxRotatedFiles
	if maxRotatedFiles == 0 && defaultFo != nil {
		maxRotatedFiles = defaultFo.MaxRotatedFiles
	}

	// Compress uses module value (no zero-value fallback since false is meaningful)
	compress := mo.Compress

	return RotationConfig{
		MaxSize:         int64(maxSize) * bytesPerMB,
		MaxAge:          maxAge,
		MaxRotatedFiles: maxRotatedFiles,
		Compress:        compress,
	}
}

// IsEnabled returns true if rotation is enabled (MaxSize > 0).
func (c RotationConfig) IsEnabled() bool {
	return c.MaxSize > 0
}

// RotationManager handles log file rotation for a BufferedFileWriter.
type RotationManager struct {
	config   RotationConfig
	filePath string // Base path (e.g., "logs/application.log")
	mu       sync.Mutex

	// Reference to the writer for file swapping
	writer *BufferedFileWriter

	// Disk-full recovery state
	consoleFallback bool
	retryTimer      *time.Timer
	retryMu         sync.Mutex

	// For clean shutdown
	closed   bool
	closedMu sync.RWMutex
}

// NewRotationManager creates a new rotation manager for the given file path.
func NewRotationManager(filePath string, config RotationConfig, writer *BufferedFileWriter) *RotationManager {
	return &RotationManager{
		config:   config,
		filePath: filePath,
		writer:   writer,
	}
}

// CheckAndRotate checks if the log file exceeds MaxSize and rotates if needed.
// This should be called periodically (e.g., during the flush interval).
func (rm *RotationManager) CheckAndRotate() {
	if !rm.config.IsEnabled() {
		return
	}

	rm.closedMu.RLock()
	if rm.closed {
		rm.closedMu.RUnlock()
		return
	}
	rm.closedMu.RUnlock()

	rm.mu.Lock()
	defer rm.mu.Unlock()

	// Check current file size
	info, err := os.Stat(rm.filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "rotation: failed to stat log file: %v\n", err)
		}
		return
	}

	if info.Size() < rm.config.MaxSize {
		return
	}

	// File exceeds size limit, rotate
	rm.rotateLocked()
}

// rotateLocked performs the actual rotation. Caller must hold rm.mu.
func (rm *RotationManager) rotateLocked() {
	// Generate timestamp for rotated file name
	timestamp := time.Now().UTC().Format(rotationTimestampFormat)
	rotatedPath := rm.rotatedFilePath(timestamp)

	// Step 1: Create new file first (double-buffer approach)
	newFile, err := rm.createNewFile()
	if err != nil {
		// Disk might be full - attempt recovery
		if rm.recoverDiskSpace() {
			newFile, err = rm.createNewFile()
		}
		if err != nil {
			rm.enableConsoleFallback()
			return
		}
	}

	// Step 2: Swap the file in the writer (holds writer lock briefly)
	oldFile, err := rm.writer.SwapFile(newFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rotation: failed to swap file: %v\n", err)
		newFile.Close()
		return
	}

	// Step 3: Close old file handle
	if oldFile != nil {
		if err := oldFile.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "rotation: failed to close old file: %v\n", err)
		}
	}

	// Step 4: Rename old log file to timestamped name
	if err := os.Rename(rm.filePath, rotatedPath); err != nil {
		fmt.Fprintf(os.Stderr, "rotation: failed to rename old file: %v\n", err)
		// Continue anyway - the new file is already active
	}

	// Rename the new file to the original path
	// The new file was created with a .new suffix, now move it to the main path
	newFilePath := rm.filePath + ".new"
	if err := os.Rename(newFilePath, rm.filePath); err != nil {
		fmt.Fprintf(os.Stderr, "rotation: failed to rename new file: %v\n", err)
	}

	// Step 5: Compress if enabled (async)
	if rm.config.Compress {
		go rm.compressFile(rotatedPath)
	}

	// Step 6: Cleanup old files
	rm.cleanup()

	// If we were in fallback mode, we successfully rotated, so disable fallback
	rm.disableConsoleFallback()
}

// createNewFile creates a new log file with .new suffix for atomic swap.
func (rm *RotationManager) createNewFile() (*os.File, error) {
	newPath := rm.filePath + ".new"
	return os.OpenFile(newPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, LogFilePermissions)
}

// rotatedFilePath generates the path for a rotated file with timestamp.
// Example: "logs/application.log" -> "logs/application-2025-01-15T14-30-05Z.log"
func (rm *RotationManager) rotatedFilePath(timestamp string) string {
	ext := filepath.Ext(rm.filePath)
	base := strings.TrimSuffix(rm.filePath, ext)
	return fmt.Sprintf("%s-%s%s", base, timestamp, ext)
}

// rotatedFilePattern returns a glob pattern matching rotated files.
// Example: "logs/application-*Z.log" and "logs/application-*Z.log.gz"
func (rm *RotationManager) rotatedFilePattern() string {
	ext := filepath.Ext(rm.filePath)
	base := strings.TrimSuffix(rm.filePath, ext)
	return fmt.Sprintf("%s-*Z%s", base, ext)
}

// compressFile compresses a rotated log file with gzip.
// Runs asynchronously - errors are logged to stderr.
func (rm *RotationManager) compressFile(srcPath string) {
	dstPath := srcPath + ".gz"

	// Open source file
	src, err := os.Open(srcPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rotation: failed to open file for compression: %v\n", err)
		return
	}
	defer src.Close()

	// Create destination .gz file
	dst, err := os.Create(dstPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rotation: failed to create compressed file: %v\n", err)
		return
	}

	// Compress with gzip
	gz := gzip.NewWriter(dst)
	if _, err := io.Copy(gz, src); err != nil {
		gz.Close()
		dst.Close()
		os.Remove(dstPath) // Clean up partial file
		fmt.Fprintf(os.Stderr, "rotation: compression failed: %v\n", err)
		return
	}

	if err := gz.Close(); err != nil {
		dst.Close()
		os.Remove(dstPath)
		fmt.Fprintf(os.Stderr, "rotation: failed to finalize compression: %v\n", err)
		return
	}

	if err := dst.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "rotation: failed to close compressed file: %v\n", err)
		// Don't remove - the file might still be usable
	}

	// Remove original after successful compression
	if err := os.Remove(srcPath); err != nil {
		fmt.Fprintf(os.Stderr, "rotation: failed to remove original after compression: %v\n", err)
	}
}

// cleanup removes old rotated files based on MaxAge and MaxRotatedFiles limits.
func (rm *RotationManager) cleanup() {
	pattern := rm.rotatedFilePattern()

	// Find all rotated files (both compressed and uncompressed)
	files, err := filepath.Glob(pattern)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rotation: failed to list rotated files: %v\n", err)
		return
	}

	// Also find compressed files
	compressedFiles, _ := filepath.Glob(pattern + ".gz")
	files = append(files, compressedFiles...)

	if len(files) == 0 {
		return
	}

	// Get file info for sorting and filtering
	type fileInfo struct {
		path    string
		modTime time.Time
	}
	fileInfos := make([]fileInfo, 0, len(files))
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		fileInfos = append(fileInfos, fileInfo{path: f, modTime: info.ModTime()})
	}

	// Sort by modification time (oldest first)
	sort.Slice(fileInfos, func(i, j int) bool {
		return fileInfos[i].modTime.Before(fileInfos[j].modTime)
	})

	now := time.Now()
	maxAge := time.Duration(rm.config.MaxAge) * 24 * time.Hour
	kept := 0

	for _, f := range fileInfos {
		shouldDelete := false

		// Check MaxAge limit
		if rm.config.MaxAge > 0 {
			age := now.Sub(f.modTime)
			if age > maxAge {
				shouldDelete = true
			}
		}

		// Check MaxRotatedFiles limit (count from newest, delete oldest excess)
		if !shouldDelete && rm.config.MaxRotatedFiles > 0 {
			// We iterate oldest first, so we need to count how many we'd keep
			remaining := len(fileInfos) - kept
			if remaining > rm.config.MaxRotatedFiles {
				shouldDelete = true
			}
		}

		if shouldDelete {
			if err := os.Remove(f.path); err != nil {
				fmt.Fprintf(os.Stderr, "rotation: failed to remove old file %s: %v\n", f.path, err)
			}
		} else {
			kept++
		}
	}
}

// recoverDiskSpace attempts to free disk space by deleting oldest rotated files.
// Returns true if space was potentially freed.
func (rm *RotationManager) recoverDiskSpace() bool {
	pattern := rm.rotatedFilePattern()

	// Find all rotated files
	files, _ := filepath.Glob(pattern)
	compressedFiles, _ := filepath.Glob(pattern + ".gz")
	files = append(files, compressedFiles...)

	if len(files) == 0 {
		return false
	}

	// Get file info for sorting
	type fileInfo struct {
		path    string
		modTime time.Time
	}
	fileInfos := make([]fileInfo, 0, len(files))
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		fileInfos = append(fileInfos, fileInfo{path: f, modTime: info.ModTime()})
	}

	// Sort oldest first
	sort.Slice(fileInfos, func(i, j int) bool {
		return fileInfos[i].modTime.Before(fileInfos[j].modTime)
	})

	// Delete oldest files until we free some space (or delete half)
	deleted := 0
	maxDelete := len(fileInfos) / 2
	if maxDelete < 1 {
		maxDelete = 1
	}

	for _, f := range fileInfos {
		if deleted >= maxDelete {
			break
		}
		if err := os.Remove(f.path); err == nil {
			deleted++
			// Check if we can create a test file now
			if rm.testDiskSpace() {
				return true
			}
		}
	}

	return rm.testDiskSpace()
}

// testDiskSpace checks if we can create a file (disk has space).
func (rm *RotationManager) testDiskSpace() bool {
	testPath := rm.filePath + ".test"
	f, err := os.Create(testPath)
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(testPath)
	return true
}

// enableConsoleFallback switches to console-only logging when disk is full.
func (rm *RotationManager) enableConsoleFallback() {
	rm.retryMu.Lock()
	defer rm.retryMu.Unlock()

	if rm.consoleFallback {
		return
	}

	rm.consoleFallback = true
	fmt.Fprintf(os.Stderr, "rotation: disk full, falling back to console logging\n")

	// Start retry timer
	rm.retryTimer = time.AfterFunc(retryInterval, rm.retryFileLogging)
}

// disableConsoleFallback restores normal file logging.
func (rm *RotationManager) disableConsoleFallback() {
	rm.retryMu.Lock()
	defer rm.retryMu.Unlock()

	if !rm.consoleFallback {
		return
	}

	rm.consoleFallback = false
	if rm.retryTimer != nil {
		rm.retryTimer.Stop()
		rm.retryTimer = nil
	}
	fmt.Fprintf(os.Stderr, "rotation: disk space available, resuming file logging\n")
}

// retryFileLogging attempts to resume file logging after disk-full fallback.
func (rm *RotationManager) retryFileLogging() {
	rm.closedMu.RLock()
	if rm.closed {
		rm.closedMu.RUnlock()
		return
	}
	rm.closedMu.RUnlock()

	if rm.testDiskSpace() {
		rm.disableConsoleFallback()
	} else {
		rm.retryMu.Lock()
		if rm.consoleFallback && rm.retryTimer != nil {
			rm.retryTimer.Reset(retryInterval)
		}
		rm.retryMu.Unlock()
	}
}

// IsConsoleFallback returns true if currently in console fallback mode.
func (rm *RotationManager) IsConsoleFallback() bool {
	rm.retryMu.Lock()
	defer rm.retryMu.Unlock()
	return rm.consoleFallback
}

// Close stops the rotation manager and cleans up resources.
func (rm *RotationManager) Close() {
	rm.closedMu.Lock()
	rm.closed = true
	rm.closedMu.Unlock()

	rm.retryMu.Lock()
	if rm.retryTimer != nil {
		rm.retryTimer.Stop()
		rm.retryTimer = nil
	}
	rm.retryMu.Unlock()
}
