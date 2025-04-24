//go:build !windows
// +build !windows

package securefs

import (
	"errors"
	"fmt"
	"log"
	"os"
	"syscall"
	"time"
)

// createFIFOPlatform creates a named pipe (FIFO) on Unix systems
// It returns the path to the FIFO and any error encountered
func createFIFOPlatform(path string) (string, error) {
	// Helper function for removing existing FIFO
	removeFIFO := func() {
		if _, err := os.Stat(path); err == nil {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				log.Printf("Warning: Error removing existing FIFO: %v", err)
			}
		}
	}

	// Remove if exists initially
	removeFIFO()

	// Create FIFO with retry mechanism
	var fifoErr error
	for retry := 0; retry < 3; retry++ {
		fifoErr = syscall.Mkfifo(path, 0o600)
		if fifoErr == nil {
			log.Printf("Successfully created FIFO pipe: %s", path)
			return path, nil
		}

		if !errors.Is(fifoErr, syscall.EEXIST) {
			break // fatal – no point in retrying
		}

		log.Printf("Retry %d: FIFO already exists, removing and retrying", retry+1)
		removeFIFO()
		time.Sleep(100 * time.Millisecond)
	}

	return "", fmt.Errorf("failed to create FIFO after retries: %w", fifoErr)
}

// CleanupNamedPipes is a no-op on non-Windows platforms
func CleanupNamedPipes() {
	// This function does nothing on non-Windows platforms
}

// openNamedPipePlatform is the Unix implementation of opening a named pipe
// This is a fallback that just uses a regular file open since on Unix
// platforms we directly use the fifo path
func openNamedPipePlatform(sfs *SecureFS, pipePath string) (*os.File, error) {
	// On Unix, we just use the same path that was used to create the FIFO
	// and open it using the standard file access functions through os.Root

	// Validate that the provided path is the same as what was created
	if pipePath != sfs.pipeName {
		return nil, fmt.Errorf("pipe path mismatch: expected %s but got %s",
			sfs.pipeName, pipePath)
	}

	// Make the path relative to the base directory
	relPath, err := sfs.RelativePath(pipePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get relative path: %w", err)
	}

	// Open the FIFO through os.Root for security
	fifo, err := sfs.root.OpenFile(relPath, os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to open Unix FIFO: %w", err)
	}

	return fifo, nil
}
