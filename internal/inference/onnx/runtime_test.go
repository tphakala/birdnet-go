package onnx

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ort "github.com/yalue/onnxruntime_go"
)

// TestInitORT_AppliesTelemetryOptOutBeforeLoading checks InitORT itself, not
// the package init() that normally runs first: the variable is blanked in the
// test, so only the call inside InitORT can set it. The library path does not
// exist, so no native ONNX Runtime is needed; the opt-out must already be in
// place when the load fails.
func TestInitORT_AppliesTelemetryOptOutBeforeLoading(t *testing.T) {
	// Not parallel: the test changes the process environment and the shared
	// library path held by the binding.
	if ort.IsInitialized() {
		t.Skip("ONNX Runtime already initialized in this process")
	}
	t.Cleanup(func() { ort.SetSharedLibraryPath("") })
	missing := filepath.Join(t.TempDir(), "libonnxruntime.so")

	t.Run("blank variable is set and reported disabled", func(t *testing.T) {
		t.Setenv(ortDisableTelemetryEnv, "")

		optOut, err := InitORT(missing)

		require.Error(t, err)
		assert.Equal(t, ortDisableTelemetryValue, os.Getenv(ortDisableTelemetryEnv))
		assert.True(t, optOut.Disabled)
		assert.False(t, optOut.APIDisabled, "the API call needs an initialized environment")
	})

	t.Run("user value that keeps telemetry on is left alone", func(t *testing.T) {
		t.Setenv(ortDisableTelemetryEnv, "0")

		optOut, err := InitORT(missing)

		require.Error(t, err)
		assert.Equal(t, "0", os.Getenv(ortDisableTelemetryEnv))
		assert.False(t, optOut.Disabled)
		assert.False(t, optOut.APIDisabled)
	})
}
