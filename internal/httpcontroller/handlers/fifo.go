package handlers

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// createFIFOWrapper is a convenience wrapper for creating FIFOs
// that uses the appropriate platform-specific implementation.
// It returns the platform-specific pipe name that should be used
// for writing to the pipe, which may differ from the input path
// on Windows systems.
func createFIFOWrapper(path string) (string, error) {
	// Validate the path exists
	if _, err := os.Stat(filepath.Dir(path)); os.IsNotExist(err) {
		return "", fmt.Errorf("directory for FIFO does not exist: %w", err)
	}

	// Call platform-specific implementation
	log.Printf("Creating FIFO using platform-specific implementation: %s", path)

	// Create the FIFO
	if err := createFIFOImpl(path); err != nil {
		return "", err
	}

	// Return the appropriate path for the platform
	if runtime.GOOS == "windows" {
		// For Windows, return the Windows named pipe path
		pipeName := formatWindowsPipeName(path)
		return pipeName, nil
	}

	// For Unix systems, return the original path
	return path, nil
}

// formatWindowsPipeName converts a path to a Windows named pipe format
// This function should match the logic in fifo_windows.go
func formatWindowsPipeName(path string) string {
	// Convert Unix-style path to Windows named pipe path
	// Format: \\.\pipe\[path]
	pipeName := strings.ReplaceAll(path, "/", "\\")
	pipeName = strings.ReplaceAll(pipeName, ":", "")
	pipeName = strings.TrimPrefix(pipeName, "\\")
	pipeName = fmt.Sprintf("\\\\.\\pipe\\%s", pipeName)

	return pipeName
}
