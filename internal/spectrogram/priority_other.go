//go:build !linux

package spectrogram

// lowerThreadPriority does nothing off Linux. Sox was not niced on Windows
// either, and setpriority on macOS would change the whole process.
func lowerThreadPriority() error { return nil }
