package notification

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileLoggerInitialization(t *testing.T) {
	t.Parallel()

	t.Run("DebugMode", func(t *testing.T) {
		t.Parallel()
		logger := getFileLogger(true)
		require.NotNil(t, logger, "logger should be initialized")
	})

	t.Run("NonDebugMode", func(t *testing.T) {
		t.Parallel()
		logger := getFileLogger(false)
		require.NotNil(t, logger, "logger should be initialized")
	})
}

func TestSetDebugLevel(t *testing.T) {
	t.Parallel()

	// Test 1: Verify singleton behavior
	logger1 := getFileLogger(false)
	logger2 := getFileLogger(true)
	assert.Same(t, logger1, logger2, "getFileLogger should return same instance (singleton)")

	// Test 2: Verify debug level changes
	SetDebugLevel(false)
	if levelVar != nil {
		assert.Equal(t, slog.LevelInfo, levelVar.Level(), "level should be Info after SetDebugLevel(false)")
	}

	SetDebugLevel(true)
	if levelVar != nil {
		assert.Equal(t, slog.LevelDebug, levelVar.Level(), "level should be Debug after SetDebugLevel(true)")
	}

	SetDebugLevel(false)
	if levelVar != nil {
		assert.Equal(t, slog.LevelInfo, levelVar.Level(), "level should be Info after second SetDebugLevel(false)")
	}

	// Test 3: Verify SetDebugLevel handles nil levelVar gracefully
	originalLevelVar := levelVar
	levelVar = nil
	assert.NotPanics(t, func() { SetDebugLevel(true) }, "SetDebugLevel(true) should not panic with nil levelVar")
	assert.NotPanics(t, func() { SetDebugLevel(false) }, "SetDebugLevel(false) should not panic with nil levelVar")
	levelVar = originalLevelVar
}
