package handlers

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// createFIFOWrapper is a convenience wrapper for creating FIFOs
// that uses the appropriate platform-specific implementation
func createFIFOWrapper(path string) error {
	// Validate the path exists
	if _, err := os.Stat(filepath.Dir(path)); os.IsNotExist(err) {
		return fmt.Errorf("directory for FIFO does not exist: %w", err)
	}

	// Call platform-specific implementation
	log.Printf("Creating FIFO using platform-specific implementation: %s", path)
	return createFIFOImpl(path)
}
