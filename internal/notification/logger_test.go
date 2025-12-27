package notification

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetLogger(t *testing.T) {
	t.Parallel()

	t.Run("ReturnsValidLogger", func(t *testing.T) {
		t.Parallel()
		logger := GetLogger()
		require.NotNil(t, logger, "logger should be initialized")
	})

	t.Run("MultipleCalls", func(t *testing.T) {
		t.Parallel()
		logger1 := GetLogger()
		logger2 := GetLogger()
		require.NotNil(t, logger1, "first logger should be initialized")
		require.NotNil(t, logger2, "second logger should be initialized")
	})
}

func TestCloseLogger(t *testing.T) {
	t.Parallel()

	err := CloseLogger()
	require.NoError(t, err, "CloseLogger should not return an error")
}
