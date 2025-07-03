package notification

import (
	"log/slog"
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

	// Test 1: Verify singleton behavior
	logger1 := getFileLogger(false)
	logger2 := getFileLogger(true) // Should return same logger instance
	if logger1 != logger2 {
		t.Error("Expected getFileLogger to return same instance (singleton)")
	}

	// Test 2: Verify debug level changes
	// Start with debug disabled
	SetDebugLevel(false)
	if levelVar != nil && levelVar.Level() != slog.LevelInfo {
		t.Errorf("Expected log level to be Info after SetDebugLevel(false), got %v", levelVar.Level())
	}

	// Enable debug
	SetDebugLevel(true)
	if levelVar != nil && levelVar.Level() != slog.LevelDebug {
		t.Errorf("Expected log level to be Debug after SetDebugLevel(true), got %v", levelVar.Level())
	}

	// Disable debug again
	SetDebugLevel(false)
	if levelVar != nil && levelVar.Level() != slog.LevelInfo {
		t.Errorf("Expected log level to be Info after second SetDebugLevel(false), got %v", levelVar.Level())
	}

	// Test 3: Verify SetDebugLevel handles nil levelVar gracefully
	// This would happen if logger initialization failed
	originalLevelVar := levelVar
	levelVar = nil
	SetDebugLevel(true) // Should not panic
	SetDebugLevel(false) // Should not panic
	levelVar = originalLevelVar
}