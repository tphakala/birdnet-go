package securefs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// CreateFIFO creates a named pipe with path validation
// It returns an error if the operation fails
func (sfs *SecureFS) CreateFIFO(path string) error {
	// Validate the path is within the base directory
	if err := IsPathValidWithinBase(sfs.baseDir, path); err != nil {
		return err
	}

	// Call platform-specific FIFO creation
	pipeName, err := createFIFOPlatform(path)
	if err != nil {
		return err
	}

	// Store the pipeName in the securefs instance
	sfs.pipeName = pipeName
	return nil
}

// GetFIFOPath returns the platform-specific path to use for the FIFO
// On Windows, this returns a named pipe path, on Unix it returns the original path
func GetFIFOPath(path string) string {
	if runtime.GOOS == "windows" {
		// Convert Unix-style path to Windows named pipe path
		// Format: \\.\pipe\[path]
		baseName := filepath.Base(path)
		ext := filepath.Ext(baseName)
		pipeName := strings.TrimSuffix(baseName, ext)
		return `\\.\pipe\` + pipeName
	}

	// For Unix systems, return the original path
	return path
}

// OpenFIFO opens the FIFO file safely with appropriate platform-specific flags
// It handles retries and returns the open file handle
func (sfs *SecureFS) OpenFIFO(ctx context.Context, path string) (*os.File, error) {
	// Validate the path is within the base directory
	if err := IsPathValidWithinBase(sfs.baseDir, path); err != nil {
		return nil, err
	}

	// Get platform-specific pipe name
	var pipePath string
	if sfs.pipeName != "" {
		// Use stored pipe name if available
		pipePath = sfs.pipeName
	} else {
		// Otherwise derive it from the path
		pipePath = GetFIFOPath(path)
	}

	// Set platform-specific flags for opening FIFO
	openFlags := getPlatformOpenFlags()

	// Try to open the FIFO with retries
	return openFIFOWithRetries(ctx, path, pipePath, openFlags, sfs)
}

// getPlatformOpenFlags returns OS-specific open flags for the FIFO
func getPlatformOpenFlags() int {
	if runtime.GOOS == "windows" {
		return os.O_WRONLY // Windows uses writeable flag without O_NONBLOCK
	}
	// Unix systems use non-blocking flag to prevent indefinite blocking if reader crashes
	return os.O_WRONLY | syscall.O_NONBLOCK
}

// openFIFOWithRetries attempts to open the FIFO with multiple retries
func openFIFOWithRetries(ctx context.Context, fifoPath, pipePath string, openFlags int, sfs *SecureFS) (*os.File, error) {
	maxRetries := 30
	retryInterval := 200 * time.Millisecond

	for i := 0; i < maxRetries; i++ {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context canceled while opening FIFO")
		default:
			// Attempt to open the FIFO with platform-specific approach
			fifo, openErr := openPlatformSpecificFIFO(pipePath, fifoPath, openFlags, sfs)
			if openErr == nil {
				return fifo, nil
			}

			if i == 0 || (i+1)%5 == 0 {
				// Log less frequently to avoid flooding logs
				fmt.Printf("Waiting for reader to open FIFO (attempt %d): %v\n", i+1, openErr)
			}

			// Sleep before retrying
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("context canceled during retry delay")
			case <-time.After(retryInterval):
				// Continue to next attempt
			}
		}
	}

	return nil, fmt.Errorf("failed to open FIFO after %d attempts", maxRetries)
}

// openPlatformSpecificFIFO opens the FIFO using OS-specific approach
func openPlatformSpecificFIFO(pipePath, fifoPath string, openFlags int, sfs *SecureFS) (*os.File, error) {
	if runtime.GOOS == "windows" {
		// For Windows, open the named pipe directly
		return os.OpenFile(pipePath, openFlags, 0o666)
	}

	// For Unix systems, use SecureFS to maintain security
	return sfs.OpenFile(fifoPath, openFlags, 0o666)
}

// Platform-specific FIFO creation is implemented in:
// - fifo_unix.go  (for Linux, macOS, etc.)
// - fifo_windows.go (for Windows)
// - fifo_other.go (fallback for other platforms)
