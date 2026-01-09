//go:build windows

package securefs

import (
	"crypto/sha1"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/tphakala/birdnet-go/internal/logger"
	"golang.org/x/sys/windows"
)

// openPipes tracks all created named pipe handles for later cleanup
// IMPORTANT: Must call CleanupNamedPipes() during application shutdown
// to prevent handle leaks, especially for long-running applications
var (
	openPipes   map[windows.Handle]struct{}
	openPipesMu sync.Mutex
	cleaned     bool // Tracks if cleanup has already occurred
)

func init() {
	openPipes = make(map[windows.Handle]struct{})
}

// createFIFOPlatform creates a Windows named pipe
// Windows doesn't support Unix-style FIFOs, so we create a named pipe
// using Windows API and emulate FIFO functionality
// It returns the named pipe path and any error encountered
// Note: The caller is responsible for ensuring CleanupNamedPipes() is called
// during shutdown to release all pipe handles
func createFIFOPlatform(path string) (string, error) {
	// Convert Unix-style path to Windows named pipe path
	// Format: \\.\pipe\[path]
	// Use a hash of the full path to avoid collisions and increase security
	pipeName := fmt.Sprintf("bn_%x", sha1.Sum([]byte(path)))[:16]
	fullPipeName := fmt.Sprintf("\\\\.\\pipe\\%s", pipeName)

	// Remove any existing file at the path location
	if _, err := os.Stat(path); err == nil {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			GetLogger().Warn("Error removing existing file at FIFO path",
				logger.String("path", path),
				logger.Error(err))
		}
	}

	// Create a named pipe with proper security attributes
	// This provides a simple implementation that should work for the HLS streaming use case
	sa := &windows.SecurityAttributes{
		Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		// Default security descriptor
		SecurityDescriptor: nil,
		// Allow the pipe to be inherited
		InheritHandle: 1,
	}

	// Set pipe parameters
	openMode := uint32(windows.PIPE_ACCESS_DUPLEX)
	pipeMode := uint32(windows.PIPE_TYPE_BYTE | windows.PIPE_READMODE_BYTE | windows.PIPE_WAIT)
	maxInstances := uint32(windows.PIPE_UNLIMITED_INSTANCES)
	outBufSize := uint32(4096)
	inBufSize := uint32(4096)
	defaultTimeout := uint32(5000) // 5 seconds

	// Windows named pipes can fail if created too quickly after cleanup
	var pipeHandle windows.Handle
	var createErr error

	for retry := 0; retry < 3; retry++ {
		pipeHandle, createErr = windows.CreateNamedPipe(
			windows.StringToUTF16Ptr(fullPipeName),
			openMode,
			pipeMode,
			maxInstances,
			outBufSize,
			inBufSize,
			defaultTimeout,
			sa,
		)

		if createErr == nil && pipeHandle != windows.InvalidHandle {
			GetLogger().Debug("Successfully created Windows named pipe", logger.String("pipe", fullPipeName))

			// Create a placeholder file at the original path location with metadata
			// This helps us track the named pipe location
			placeholderInfo := fmt.Sprintf("Windows named pipe: %s", fullPipeName)

			// Ensure the parent directory exists
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				GetLogger().Warn("Failed to create parent directory for named pipe placeholder",
					logger.String("path", filepath.Dir(path)),
					logger.Error(err))
			}

			if err := os.WriteFile(path, []byte(placeholderInfo), 0o666); err != nil {
				GetLogger().Warn("Failed to create named pipe placeholder file",
					logger.String("path", path),
					logger.Error(err))
			}

			// Track the handle for later cleanup with proper synchronization
			openPipesMu.Lock()
			openPipes[pipeHandle] = struct{}{}
			openPipesMu.Unlock()

			return fullPipeName, nil
		}

		GetLogger().Debug("Failed to create Windows named pipe, retrying",
			logger.Int("retry", retry+1),
			logger.Error(createErr))
		if pipeHandle != windows.InvalidHandle {
			_ = windows.CloseHandle(pipeHandle)
		}
		time.Sleep(100 * time.Millisecond)
	}

	return "", fmt.Errorf("failed to create Windows named pipe after retries: %v", createErr)
}

// CleanupNamedPipes closes all open named pipe handles
// Call this function during application shutdown to properly release resources
// This must be called to prevent handle leaks, especially for long-running applications
func CleanupNamedPipes() {
	openPipesMu.Lock()
	defer openPipesMu.Unlock()

	// If already cleaned, do nothing (idempotency guard)
	if cleaned || len(openPipes) == 0 {
		return
	}

	for h := range openPipes {
		_ = windows.CloseHandle(h)
	}

	// Clear the map and mark as cleaned
	openPipes = make(map[windows.Handle]struct{})
	cleaned = true
}

// openNamedPipePlatform is the Windows-specific implementation of opening a named pipe
// This function is called by the cross-platform OpenNamedPipe method
func openNamedPipePlatform(sfs *SecureFS, pipePath string) (*os.File, error) {
	// Validate the path is a named pipe path
	if len(pipePath) < 10 || !strings.HasPrefix(pipePath, "\\\\.\\pipe\\") {
		return nil, fmt.Errorf("invalid Windows named pipe path: %s", pipePath)
	}

	// Open the named pipe with proper flags
	h, err := windows.CreateFile(
		windows.StringToUTF16Ptr(pipePath),
		windows.GENERIC_WRITE,
		0, // No sharing
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OVERLAPPED, // Use overlapped I/O for better performance
		0,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to open Windows named pipe: %w", err)
	}

	// Convert Windows handle to os.File for compatibility with existing code
	// This uses an os.NewFile pattern similar to os/exec on Windows
	fd := uintptr(h)
	file := os.NewFile(fd, pipePath)
	if file == nil {
		windows.CloseHandle(h)
		return nil, fmt.Errorf("failed to create file from pipe handle")
	}

	// Use runtime.SetFinalizer to cleanup the handle when the file is garbage collected
	// This isn't perfect (doesn't clean up immediately when file is closed) but prevents leaks
	runtime.SetFinalizer(file, func(f *os.File) {
		f.Close()
		openPipesMu.Lock()
		delete(openPipes, h)
		openPipesMu.Unlock()
	})

	return file, nil
}
