//go:build !openvino

package classifier

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// newCaptureLogger swaps the process-global logger for one that writes to the
// returned buffer at debug level, restoring the previous logger on cleanup. It is
// used to assert which lines logOpenVINODeclined emits. Callers must NOT run in
// parallel: it mutates global state.
func newCaptureLogger(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	capture := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	cl, err := logger.NewCentralLogger(
		&logger.LoggingConfig{
			Console:      &logger.ConsoleOutput{Enabled: false},
			FileOutput:   &logger.FileOutput{Enabled: false},
			DefaultLevel: "debug",
		},
		capture,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cl.Close() })
	prev := logger.Global()
	logger.SetGlobal(cl)
	t.Cleanup(func() { logger.SetGlobal(prev) })
	return &buf
}

// TestShouldTryOpenVINO_FalseWithoutTag verifies that without the openvino build
// tag, shouldTryOpenVINO returns false regardless of config or CPU state.
func TestShouldTryOpenVINO_FalseWithoutTag(t *testing.T) {
	t.Parallel()
	// In the default build openvinoBackendAvailable is false, so the gate must
	// be false regardless of config/CPU.
	bn := &BirdNET{Settings: &conf.Settings{}}
	bn.Settings.BirdNET.Backend = conf.BackendPrefOpenVINO
	bn.ModelInfo = ModelInfo{ID: DefaultModelVersion, Backend: BackendONNX}
	assert.False(t, bn.shouldTryOpenVINO(),
		"without the openvino tag, shouldTryOpenVINO must be false")
}

// TestShouldTryOpenVINO_OptOut verifies that Backend="onnx" forces shouldTryOpenVINO
// to false even if everything else would allow it.
func TestShouldTryOpenVINO_OptOut(t *testing.T) {
	t.Parallel()
	// Backend="onnx" forces OFF even if everything else allows it.
	bn := &BirdNET{Settings: &conf.Settings{}}
	bn.Settings.BirdNET.Backend = conf.BackendPrefONNX
	bn.ModelInfo = ModelInfo{ID: DefaultModelVersion, Backend: BackendONNX}
	assert.False(t, bn.shouldTryOpenVINO())
}

// TestShouldTryOpenVINO_FalseForAutoWithoutTag pins the no-tag gate predicate
// for the default/auto backend: without the openvino build tag, shouldTryOpenVINO
// is false even when the model ID and CPU would otherwise qualify.
func TestShouldTryOpenVINO_FalseForAutoWithoutTag(t *testing.T) {
	t.Parallel()
	// Without the openvino tag, shouldTryOpenVINO is false, so initializeModel
	// on an ONNX-backed model must go straight to the ONNX path. We assert the
	// gate, which is the observable contract in a CI (no-tag) build.
	bn := &BirdNET{Settings: &conf.Settings{}}
	bn.ModelInfo = ModelInfo{ID: DefaultModelVersion, Backend: BackendONNX}
	assert.False(t, bn.shouldTryOpenVINO())
}

// TestOpenVINOPlanReason_NotBuilt verifies that in the default (no-tag) build the
// planner reports the not-built reason for an otherwise-eligible BirdNET v2.4
// model, so initializeModel can log why OpenVINO was declined rather than falling
// back silently.
func TestOpenVINOPlanReason_NotBuilt(t *testing.T) {
	t.Parallel()
	bn := &BirdNET{Settings: &conf.Settings{}}
	bn.ModelInfo = ModelInfo{ID: DefaultModelVersion, Backend: BackendONNX}
	_, ok, reason := bn.openVINOPlan()
	assert.False(t, ok)
	assert.Equal(t, ovReasonNotBuilt, reason)
}

// TestOpenVINOPlanReason_WrongModel verifies the wrong-model reason short-circuits
// before the build-tag check, so a non-v2.4 model is reported as such in any build.
func TestOpenVINOPlanReason_WrongModel(t *testing.T) {
	t.Parallel()
	bn := &BirdNET{Settings: &conf.Settings{}}
	bn.ModelInfo = ModelInfo{ID: "BirdNET_V2.3", Backend: BackendONNX}
	_, ok, reason := bn.openVINOPlan()
	assert.False(t, ok)
	assert.Equal(t, ovReasonNotBirdNETv24, reason)
}

// TestTryBatOpenVINO_FallsBackWithoutTag verifies that without the openvino build
// tag, tryBatOpenVINO declines (returns a nil extractor and ok=false) so NewBat uses
// the ORT embedding path. This pins the "OpenVINO must never make the bat model fail
// to load" contract at the gate level, and the explicit nil return guards against the
// typed-nil interface trap. It is the observable behavior in a CI (no-tag) build.
func TestTryBatOpenVINO_FallsBackWithoutTag(t *testing.T) {
	t.Parallel()
	cfg := &BatModelConfig{
		Backend:        conf.BackendPrefOpenVINO,
		OpenVINODevice: conf.OVDeviceGPU,
	}
	ext, device, ok := tryBatOpenVINO(cfg, 1024)
	assert.False(t, ok, "without the openvino tag, the bat OV path must decline")
	assert.Nil(t, ext, "a declined OV path must return a nil extractor (no typed-nil trap)")
	assert.Empty(t, device)
}

// TestOpenVINOPlanForBat_NotBuilt verifies that in the default (no-tag) build the
// planner reports the not-built reason for the bat embedding model, so tryBatOpenVINO
// logs why OpenVINO was declined rather than falling back silently.
func TestOpenVINOPlanForBat_NotBuilt(t *testing.T) {
	t.Parallel()
	// Output index 1 is the 2-output backbone's embedding port; its exact value is
	// immaterial here since the no-tag planner declines before using it.
	_, ok, reason := openVINOPlanFor(conf.BackendPrefOpenVINO, conf.OVDeviceGPU, RegistryIDBat, "", 1)
	assert.False(t, ok)
	assert.Equal(t, ovReasonNotBuilt, reason)
}

// TestLogOpenVINODeclined_StandardBuild verifies the noise-control policy on a
// non-openvino build: the auto path (the common case) stays silent so it does not
// add a line to every standard-build startup, while an explicit backend=openvino
// opt-in still warns at WARN so the user learns their request could not be honored.
// Not parallel: it swaps the process-global logger.
func TestLogOpenVINODeclined_StandardBuild(t *testing.T) {
	buf := newCaptureLogger(t)

	logOpenVINODeclined(DefaultModelVersion, conf.BackendPrefAuto, ovReasonNotBuilt)
	assert.Empty(t, buf.String(), "auto-path decline must be silent on a non-openvino build")

	logOpenVINODeclined(DefaultModelVersion, conf.BackendPrefOpenVINO, ovReasonNotBuilt)
	out := buf.String()
	assert.Contains(t, out, "OpenVINO requested but not used", "explicit opt-in must warn even on a non-openvino build")
	assert.Contains(t, out, ovReasonNotBuilt)
	assert.Contains(t, out, "level=WARN")
}
