//go:build !windows
// +build !windows

package handlers

import (
	"fmt"
	"log"
	"os"
	"syscall"
	"time"
)

// createFIFOImpl creates a named pipe (FIFO) on Unix systems
func createFIFOImpl(path string) error {
	// Remove if exists
	if _, err := os.Stat(path); err == nil {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			log.Printf("Warning: Error removing existing FIFO: %v", err)
		}
	}

	// Create FIFO with retry mechanism
	var fifoErr error
	for retry := 0; retry < 3; retry++ {
		fifoErr = syscall.Mkfifo(path, 0o666)
		if fifoErr == nil {
			log.Printf("Successfully created FIFO pipe: %s", path)
			return nil
		}

		log.Printf("Retry %d: Failed to create FIFO pipe: %v", retry+1, fifoErr)
		// If error is "file exists", try to remove again
		if os.IsExist(fifoErr) {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				log.Printf("Warning: Error removing existing FIFO: %v", err)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	return fmt.Errorf("failed to create FIFO after retries: %w", fifoErr)
}
