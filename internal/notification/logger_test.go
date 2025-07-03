package notification

import (
	"testing"
)

func TestFileLoggerInitialization(t *testing.T) {
	t.Parallel()

	// Test debug mode initialization
	t.Run("DebugMode", func(t *testing.T) {
		t.Parallel()
		logger := getFileLogger(true)
		if logger == nil {
			t.Error("Expected logger to be initialized, got nil")
		}
	})

	// Test non-debug mode initialization
	t.Run("NonDebugMode", func(t *testing.T) {
		t.Parallel()
		logger := getFileLogger(false)
		if logger == nil {
			t.Error("Expected logger to be initialized, got nil")
		}
	})
}

func TestSetDebugLevel(t *testing.T) {
	t.Parallel()

	// Initialize logger first
	_ = getFileLogger(false)

	// Test enabling debug
	SetDebugLevel(true)

	// Test disabling debug
	SetDebugLevel(false)

	// No error checking needed as SetDebugLevel handles nil levelVar gracefully
}