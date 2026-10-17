package inference

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
	ort "github.com/tphakala/birdnet-go/internal/inference/onnx"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
)

// yalueAPIBaseTooOldText is the literal text yalue/onnxruntime_go v1.36.0
// returns when the loaded library cannot serve its C API level. It is spelled
// out here so a wrong production marker cannot pass.
const yalueAPIBaseTooOldText = "Error setting ORT API base: 2"

// apiBaseFailure builds the error InitORT returns when the loaded library is
// older than the binding's required C API level.
func apiBaseFailure(t *testing.T) (wrapped, cause error) {
	t.Helper()
	cause = errors.NewStd(yalueAPIBaseTooOldText)
	return fmt.Errorf("Platform-specific initialization failed: %w", cause), cause
}

func TestDescribeORTInitError_TranslatesAPIBaseFailure(t *testing.T) {
	t.Parallel()

	t.Run("without version hint", func(t *testing.T) {
		t.Parallel()
		wrapped, cause := apiBaseFailure(t)
		got := describeORTInitError("/opt/ort/libonnxruntime.so", wrapped)
		require.Error(t, got)
		msg := got.Error()
		assert.Contains(t, msg, "/opt/ort/libonnxruntime.so")
		assert.Contains(t, msg, "older than the supported minimum")
		assert.Contains(t, msg, "1.29 or newer")
		assert.Contains(t, msg, ORTInstallGuideURL)
		assert.NotContains(t, msg, "suggests version")
		require.ErrorIs(t, got, cause)
	})

	t.Run("known file name version is added as a hint", func(t *testing.T) {
		t.Parallel()
		path := writeLibFile(t, t.TempDir(), "libonnxruntime.so.1.25.1")
		wrapped, cause := apiBaseFailure(t)
		got := describeORTInitError(path, wrapped)
		require.ErrorIs(t, got, cause)
		assert.Contains(t, got.Error(), "file name suggests version 1.25.1")
	})

	t.Run("unversioned so.1 gives no hint", func(t *testing.T) {
		t.Parallel()
		path := writeLibFile(t, t.TempDir(), "libonnxruntime.so.1")
		wrapped, _ := apiBaseFailure(t)
		got := describeORTInitError(path, wrapped)
		assert.NotContains(t, got.Error(), "suggests version")
	})
}

func TestDescribeORTInitError_PassesOtherFailuresThrough(t *testing.T) {
	t.Parallel()

	for _, msg := range []string{
		"Error loading ONNX shared library \"/x/libonnxruntime.so\": no such file",
		"Error setting ORT API base: 1",
	} {
		t.Run(msg, func(t *testing.T) {
			t.Parallel()
			got := describeORTInitError("/x/libonnxruntime.so", fmt.Errorf("Platform-specific initialization failed: %w", errors.NewStd(msg)))
			require.Error(t, got)
			assert.Contains(t, got.Error(), "failed to initialize ONNX Runtime")
			assert.NotContains(t, got.Error(), "1.29")
		})
	}
}

func TestInitONNXRuntime_FailedLoadStaysRetryable(t *testing.T) {
	// Not parallel: touches the package-level init state.
	if IsORTInitialized() {
		t.Skip("ORT already initialized in this process")
	}

	missing := filepath.Join(t.TempDir(), "libonnxruntime.so")
	for range 2 {
		err := InitONNXRuntime(missing)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to initialize ONNX Runtime")
		assert.False(t, IsORTInitialized(), "a failed load must leave init retryable")
	}
	_, statErr := os.Stat(missing)
	require.Error(t, statErr)
}

func TestLogORTInitialized_ReportsVersionAndTelemetry(t *testing.T) {
	// Not parallel: captures the global logger.
	buf := logtest.CaptureBufferAt(t, slog.LevelInfo)
	logORTInitialized("1.31.0", "/usr/lib/libonnxruntime.so", ort.TelemetryOptOut{
		Value:       "1",
		Disabled:    true,
		APIDisabled: true,
	})
	out := buf.String()
	assert.Contains(t, out, "ONNX Runtime initialized")
	assert.Contains(t, out, "version=1.31.0")
	assert.Contains(t, out, "ort_disable_telemetry=1")
	assert.Contains(t, out, "telemetry_disabled=true")
	assert.Contains(t, out, "telemetry_api_disabled=true")
}
