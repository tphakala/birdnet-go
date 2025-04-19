//go:build windows
// +build windows

package handlers

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// createFIFOImpl creates a Windows named pipe
// Windows doesn't support Unix-style FIFOs, so we create a named pipe
// using Windows API and emulate FIFO functionality
func createFIFOImpl(path string) error {
	// Convert Unix-style path to Windows named pipe path
	// Format: \\.\pipe\[path]
	pipeName := strings.ReplaceAll(path, "/", "\\")
	pipeName = strings.ReplaceAll(pipeName, ":", "")
	pipeName = strings.TrimPrefix(pipeName, "\\")
	pipeName = fmt.Sprintf("\\\\.\\pipe\\%s", pipeName)

	// Remove any existing file at the path location
	if _, err := os.Stat(path); err == nil {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			log.Printf("Warning: Error removing existing file at FIFO path: %v", err)
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
			windows.StringToUTF16Ptr(pipeName),
			openMode,
			pipeMode,
			maxInstances,
			outBufSize,
			inBufSize,
			defaultTimeout,
			sa,
		)

		if createErr == nil && pipeHandle != windows.InvalidHandle {
			log.Printf("Successfully created Windows named pipe: %s", pipeName)

			// Create a placeholder file at the original path location with metadata
			// This helps us track the named pipe location
			placeholderInfo := fmt.Sprintf("Windows named pipe: %s", pipeName)
			if err := os.WriteFile(path, []byte(placeholderInfo), 0o666); err != nil {
				log.Printf("Warning: Failed to create named pipe placeholder file: %v", err)
			}

			// We deliberately leak the handle here as we want the pipe to remain
			// available throughout the application lifetime, similar to a FIFO
			// The OS will clean it up when the process terminates
			return nil
		}

		log.Printf("Retry %d: Failed to create Windows named pipe: %v", retry+1, createErr)
		if pipeHandle != windows.InvalidHandle {
			_ = windows.CloseHandle(pipeHandle)
		}
		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("failed to create Windows named pipe after retries: %v", createErr)
}
