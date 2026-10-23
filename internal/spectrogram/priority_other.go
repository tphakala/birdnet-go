//go:build !linux

package spectrogram

// lowerThreadPriority does nothing off Linux: setpriority on macOS would change
// the whole process.
func lowerThreadPriority() error { return nil }
