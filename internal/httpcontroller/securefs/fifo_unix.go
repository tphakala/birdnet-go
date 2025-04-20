//go:build !windows
// +build !windows

package securefs

import (
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

		log.Printf("Retry %d: Failed to create FIFO pipe: %v", retry+1, fifoErr)
		// If error is "file exists", try to remove again
		if os.IsExist(fifoErr) {
			removeFIFO()
			time.Sleep(100 * time.Millisecond)
		}
	}

	return "", fmt.Errorf("failed to create FIFO after retries: %w", fifoErr)
}

// CleanupNamedPipes is a no-op on non-Windows platforms
func CleanupNamedPipes() {
	// This function does nothing on non-Windows platforms
}
