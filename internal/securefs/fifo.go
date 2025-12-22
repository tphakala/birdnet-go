package securefs

import (
	"context"
	"fmt"
	"hash/crc32"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// CreateFIFO creates a FIFO (named pipe) at the specified path with platform-specific implementation
func (sfs *SecureFS) CreateFIFO(path string) error {
	// Validate the path is within the base directory
	if err := IsPathValidWithinBase(sfs.baseDir, path); err != nil {
		return fmt.Errorf("security error creating FIFO: %w", err)
	}

	// First try to create the FIFO using platform-specific functions
	pipeName, err := createFIFOPlatform(path)
	if err != nil {
		return fmt.Errorf("error creating FIFO: %w", err)
	}

	// Store the pipe name for later reference
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
		// Use a hash suffix to avoid name collisions
		pipeName := fmt.Sprintf("%s_%x", strings.TrimSuffix(baseName, ext), crc32.ChecksumIEEE([]byte(path)))
		return `\\.\pipe\` + pipeName
	}

	// For Unix systems, return the original path
	return path
}

// OpenFIFO opens the FIFO at the given path. It works in a platform-independent manner.
func (sfs *SecureFS) OpenFIFO(ctx context.Context, path string) (*os.File, error) {
	// Validate the path is within the base directory
	if err := IsPathValidWithinBase(sfs.baseDir, path); err != nil {
		return nil, fmt.Errorf("security error opening FIFO: %w", err)
	}

	// For non-Windows platforms, this is just a regular file open
	// Windows support is implemented in the platform-specific file
	var fifo *os.File
	var err error

	// Perform platform-specific fifo opening
	if runtime.GOOS == "windows" && sfs.pipeName != "" {
		// Use a pipe path from CreateFIFO
		fifo, err = sfs.OpenNamedPipe(sfs.pipeName)
	} else {
		// For Unix platforms, we can just open the file
		relPath, err := sfs.RelativePath(path)
		if err != nil {
			return nil, fmt.Errorf("failed to get relative path: %w", err)
		}

		// On Unix, open the FIFO through os.Root for sandbox security
		fifo, err = sfs.root.OpenFile(relPath, getPlatformOpenFlags(), 0)
		if err != nil {
			return nil, fmt.Errorf("failed to open FIFO: %w", err)
		}
	}

	return fifo, err
}

// OpenNamedPipe opens a named pipe with platform-specific implementation
// This is a cross-platform facade that delegates to the platform-specific implementation
func (sfs *SecureFS) OpenNamedPipe(pipePath string) (*os.File, error) {
	// This implementation is in the platform-specific file (fifo_windows.go or fifo_unix.go)
	return openNamedPipePlatform(sfs, pipePath)
}

// getPlatformOpenFlags returns OS-specific open flags for the FIFO
func getPlatformOpenFlags() int {
	if runtime.GOOS == "windows" {
		return os.O_WRONLY // Windows uses writeable flag without O_NONBLOCK
	}
	// Unix systems use non-blocking flag to prevent indefinite blocking if reader crashes
	return os.O_WRONLY | syscall.O_NONBLOCK
}

// waitWithContext waits for the specified duration or until context is canceled
func waitWithContext(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// openFIFOWithRetries attempts to open the FIFO with multiple retries
func openFIFOWithRetries(ctx context.Context, fifoPath, pipePath string, openFlags int, sfs *SecureFS) (*os.File, error) {
	const maxRetries = 30
	const retryInterval = 200 * time.Millisecond

	for i := range maxRetries {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("context canceled while opening FIFO")
		}

		fifo, openErr := openPlatformSpecificFIFO(pipePath, fifoPath, openFlags, sfs)
		if openErr == nil {
			return fifo, nil
		}

		if i == 0 || (i+1)%5 == 0 {
			log.Printf("FIFO %s: writer waiting (attempt %d): %v", fifoPath, i+1, openErr)
		}

		if err := waitWithContext(ctx, retryInterval); err != nil {
			return nil, fmt.Errorf("context canceled during retry delay")
		}
	}

	return nil, fmt.Errorf("failed to open FIFO after %d attempts", maxRetries)
}

// openPlatformSpecificFIFO opens the FIFO using OS-specific approach
func openPlatformSpecificFIFO(pipePath, fifoPath string, openFlags int, sfs *SecureFS) (*os.File, error) {
	if runtime.GOOS == "windows" {
		// Validate Windows pipe path to ensure it's a valid named pipe path
		if !strings.HasPrefix(pipePath, `\\.\pipe\`) {
			return nil, fmt.Errorf("security error: Windows pipe path must start with \\\\.\\pipe\\")
		}
		// For Windows, open the named pipe directly
		// Named pipes on Windows have their own security model independent of file permissions
		return os.OpenFile(pipePath, openFlags, 0o600) //nolint:gosec // G304: pipePath validated to start with \\.\pipe\
	}

	// For Unix systems, use SecureFS to maintain security
	// FIFOs need write permission for IPC, but restrict to owner+group
	return sfs.OpenFile(fifoPath, openFlags, 0o660)
}

// Platform-specific FIFO creation is implemented in:
// - fifo_unix.go  (for Linux, macOS, etc.)
// - fifo_windows.go (for Windows)
// - fifo_other.go (fallback for other platforms)
