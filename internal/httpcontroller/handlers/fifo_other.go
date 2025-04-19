//go:build !windows
// +build !windows

package handlers

// CleanupNamedPipes is a no-op on non-Windows platforms
func CleanupNamedPipes() {
	// This function does nothing on non-Windows platforms
}
