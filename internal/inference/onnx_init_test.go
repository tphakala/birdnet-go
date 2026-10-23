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

// apiBaseTooOldText is the literal text the official binding returns when the
// loaded library cannot serve even its lowest C API level (ort.go:67-69 in
// github.com/microsoft/onnxruntime/go at the pinned commit). It is spelled out
// here so a wrong production marker cannot pass.
const apiBaseTooOldText = "ort: init api: GetApi failed; ORT library may be too old (need API >= 17)"

// apiBaseFailure builds the error InitORT returns when the loaded library is
// older than the binding's lowest C API level.
func apiBaseFailure(t *testing.T) (wrapped, cause error) {
	t.Helper()
	cause = errors.NewStd(apiBaseTooOldText)
	return fmt.Errorf("ort: init api: %w", cause), cause
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
		"ort: load library: dlopen /x/libonnxruntime.so: no such file or directory",
		"ort: create env: invalid argument",
	} {
		t.Run(msg, func(t *testing.T) {
			t.Parallel()
			got := describeORTInitError("/x/libonnxruntime.so", fmt.Errorf("ort: init: %w", errors.NewStd(msg)))
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

// TestInitONNXRuntime_LibraryBelowMinimumStaysRejected loads a library that is
// older than the supported minimum. The official binding accepts any library
// from ONNX Runtime 1.17 on and ignores later library paths once a load has
// succeeded, so InitONNXRuntime must keep rejecting it itself. It loads a
// library for the life of the process: run it alone, with ORT_TEST_OLD_LIB set
// to a libonnxruntime older than the minimum, for example
// ORT_TEST_OLD_LIB=/usr/lib/libonnxruntime.so.1.25.1 go test -run LibraryBelowMinimum.
func TestInitONNXRuntime_LibraryBelowMinimumStaysRejected(t *testing.T) {
	// Not parallel: touches the package-level init state.
	oldLib := os.Getenv("ORT_TEST_OLD_LIB")
	if oldLib == "" {
		t.Skip("set ORT_TEST_OLD_LIB to a libonnxruntime older than the supported minimum to run")
	}
	if IsORTInitialized() {
		t.Skip("ORT already initialized in this process")
	}

	// The binding keeps this library for the life of the process; clear the
	// rejection record so a later test does not read it. Run this test alone.
	t.Cleanup(func() { setRejectedORT(nil) })

	for range 2 {
		err := InitONNXRuntime(oldLib)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "older than the supported minimum")
		assert.Contains(t, err.Error(), "restart")
		assert.False(t, IsORTInitialized(), "a library below the minimum must not count as initialized")
	}

	status := CheckORTAvailability("")
	assert.False(t, status.Available)
	assert.NotEmpty(t, status.Version)
	assert.Contains(t, status.Error, "older than the minimum supported version")
}

// setRejectedORT replaces the recorded rejection under the init lock.
func setRejectedORT(rej *ortRejection) {
	ortInitMu.Lock()
	defer ortInitMu.Unlock()
	ortRejected = rej
}

// TestRejectedLibrary_StaysRejectedAndReportsRestart records a rejection
// directly, so it runs without an old native library: every later init returns
// the stored error without reading its path, and the availability status names
// the loaded version and says a restart is needed.
func TestRejectedLibrary_StaysRejectedAndReportsRestart(t *testing.T) {
	// Not parallel: touches the package-level init state.
	if IsORTInitialized() {
		t.Skip("ORT already initialized in this process")
	}
	const loadedVersion = "1.25.1"
	rej := &ortRejection{
		version: loadedVersion,
		path:    "/opt/ort/libonnxruntime.so." + loadedVersion,
		err:     rejectedORTError("/opt/ort/libonnxruntime.so."+loadedVersion, loadedVersion),
	}
	setRejectedORT(rej)
	t.Cleanup(func() { setRejectedORT(nil) })

	otherPath := filepath.Join(t.TempDir(), "libonnxruntime.so")
	err := InitONNXRuntime(otherPath)
	require.Error(t, err)
	assert.Same(t, rej.err, err, "a later init must return the stored error, not try the new path")
	assert.False(t, IsORTInitialized())

	status := CheckORTAvailability("")
	assert.False(t, status.Available)
	assert.Equal(t, loadedVersion, status.Version)
	assert.Equal(t, rej.path, status.LibraryPath)
	assert.Contains(t, status.Error, "restart")
}
