package logger

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Default buffer size for file writes (32KB provides good batching without excessive memory)
const DefaultBufferSize = 32 * 1024

// DefaultFlushInterval is the default interval for auto-flushing buffered writes
const DefaultFlushInterval = 5 * time.Second

// BufferedFileWriter wraps a file with buffered I/O for improved performance.
// It is thread-safe and supports periodic auto-flushing.
//
// Design note: The file handle is abstracted to support future log rotation.
// When rotation is implemented, the underlying file can be swapped via a
// dedicated method without changing the writer interface.
//
// TODO: Implement log rotation with the following features:
//   - Size-based rotation (rotate when file exceeds MaxSize MB)
//   - Age-based cleanup (delete files older than MaxAge days)
//   - Backup limit (keep only MaxBackups old files)
//   - Optional compression of rotated files
//   - Atomic file swap to prevent data loss during rotation
type BufferedFileWriter struct {
	mu          sync.Mutex
	file        *os.File
	writer      *bufio.Writer
	bufferSize  int
	filePath    string
	stopFlush   chan struct{}
	flushDone   chan struct{}
	flushTicker *time.Ticker
	closed      bool // tracks if Close has been called
}

// BufferedWriterOption configures a BufferedFileWriter
type BufferedWriterOption func(*BufferedFileWriter)

// WithBufferSize sets the buffer size for the writer
func WithBufferSize(size int) BufferedWriterOption {
	return func(w *BufferedFileWriter) {
		if size > 0 {
			w.bufferSize = size
		}
	}
}

// WithFlushInterval sets the auto-flush interval. Pass 0 to disable auto-flush.
func WithFlushInterval(interval time.Duration) BufferedWriterOption {
	return func(w *BufferedFileWriter) {
		if w.flushTicker != nil {
			w.flushTicker.Stop()
			w.flushTicker = nil
		}
		if interval > 0 {
			w.flushTicker = time.NewTicker(interval)
		}
	}
}

// NewBufferedFileWriter creates a new buffered file writer.
// The file is opened with append mode. Auto-flush runs every DefaultFlushInterval.
func NewBufferedFileWriter(filePath string, opts ...BufferedWriterOption) (*BufferedFileWriter, error) {
	w := &BufferedFileWriter{
		bufferSize: DefaultBufferSize,
		filePath:   filePath,
		stopFlush:  make(chan struct{}),
		flushDone:  make(chan struct{}),
	}

	// Apply options before opening file
	for _, opt := range opts {
		opt(w)
	}

	// Open the file
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, LogFilePermissions) //nolint:gosec // file path from user config is intentional
	if err != nil {
		return nil, fmt.Errorf("failed to open log file %s: %w", filePath, err)
	}

	w.file = file
	w.writer = bufio.NewWriterSize(file, w.bufferSize)

	// Start auto-flush goroutine if ticker wasn't disabled
	if w.flushTicker == nil {
		w.flushTicker = time.NewTicker(DefaultFlushInterval)
	}
	go w.autoFlushLoop()

	return w, nil
}

// NewBufferedFileWriterFromFile creates a buffered writer from an existing file handle.
// This is useful for testing or when the file is already open.
// The caller remains responsible for closing the underlying file after calling Close().
func NewBufferedFileWriterFromFile(file *os.File, opts ...BufferedWriterOption) *BufferedFileWriter {
	w := &BufferedFileWriter{
		bufferSize: DefaultBufferSize,
		file:       file,
		stopFlush:  make(chan struct{}),
		flushDone:  make(chan struct{}),
	}

	if file != nil {
		w.filePath = file.Name()
	}

	// Apply options
	for _, opt := range opts {
		opt(w)
	}

	if file != nil {
		w.writer = bufio.NewWriterSize(file, w.bufferSize)

		// Only start auto-flush goroutine if we have a valid file
		if w.flushTicker == nil {
			w.flushTicker = time.NewTicker(DefaultFlushInterval)
		}
		go w.autoFlushLoop()
	}

	return w
}

// autoFlushLoop periodically flushes the buffer to disk
func (w *BufferedFileWriter) autoFlushLoop() {
	defer close(w.flushDone)

	for {
		select {
		case <-w.stopFlush:
			return
		case <-w.flushTicker.C:
			// Ignore flush errors in background - they'll surface on next Write
			_ = w.Flush()
		}
	}
}

// Write writes data to the buffer. Thread-safe.
func (w *BufferedFileWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.writer == nil {
		return 0, fmt.Errorf("writer is closed")
	}

	return w.writer.Write(p)
}

// Flush flushes the buffer to the underlying file and syncs to disk. Thread-safe.
func (w *BufferedFileWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.flushLocked()
}

// flushLocked flushes buffer to file without acquiring the lock (caller must hold lock).
// Note: This does NOT sync to disk (fsync) since log files are not critical.
// Data is written to OS buffers which will be persisted eventually.
// For critical sync, use syncLocked().
func (w *BufferedFileWriter) flushLocked() error {
	if w.writer == nil {
		return nil
	}

	// Flush buffer to OS file buffers (not disk)
	if err := w.writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush buffer: %w", err)
	}

	return nil
}

// syncLocked flushes buffer and syncs to disk (caller must hold lock)
func (w *BufferedFileWriter) syncLocked() error {
	if err := w.flushLocked(); err != nil {
		return err
	}

	// Sync file to disk
	if w.file != nil {
		if err := w.file.Sync(); err != nil {
			return fmt.Errorf("failed to sync file: %w", err)
		}
	}

	return nil
}

// Sync flushes the buffer and syncs to disk. Thread-safe.
// Use this for critical data that must be persisted immediately.
func (w *BufferedFileWriter) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.syncLocked()
}

// Close flushes the buffer, syncs to disk, and closes the underlying file. Thread-safe.
// Close is idempotent - calling it multiple times is safe.
func (w *BufferedFileWriter) Close() error {
	w.mu.Lock()

	// Check if already closed
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	w.mu.Unlock()

	// Stop auto-flush goroutine if it was started
	if w.flushTicker != nil {
		w.flushTicker.Stop()
		close(w.stopFlush)
		<-w.flushDone // Wait for goroutine to exit
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	var errs []error

	// Sync any remaining data to disk before closing
	if err := w.syncLocked(); err != nil {
		errs = append(errs, err)
	}

	// Close the file
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close file: %w", err))
		}
		w.file = nil
	}

	w.writer = nil

	return errors.Join(errs...)
}

// FilePath returns the path of the underlying file
func (w *BufferedFileWriter) FilePath() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.filePath
}

// Buffered returns the number of bytes buffered but not yet written to disk
func (w *BufferedFileWriter) Buffered() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.writer == nil {
		return 0
	}
	return w.writer.Buffered()
}

// Ensure BufferedFileWriter implements io.WriteCloser
var (
	_ io.Writer = (*BufferedFileWriter)(nil)
	_ io.Closer = (*BufferedFileWriter)(nil)
)
