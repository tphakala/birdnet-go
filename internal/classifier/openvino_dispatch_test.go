//go:build !openvino

package classifier

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

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

	// Backend onnx is reported before a binary without OpenVINO.
	bn.Settings.BirdNET.Backend = conf.BackendPrefONNX
	_, ok, reason = bn.openVINOPlan()
	assert.False(t, ok)
	assert.Equal(t, ovReasonBackendONNX, reason)
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

// TestOpenVINOPlanForBat_NotBuilt verifies that in the default (no-tag) build the
// planner reports the not-built reason for the bat embedding model, so tryBatOpenVINO
// logs why OpenVINO was declined rather than falling back silently, and the
// backend-onnx reason when the backend is onnx.
func TestOpenVINOPlanForBat_NotBuilt(t *testing.T) {
	t.Parallel()
	// Output index 1 is the 2-output backbone's embedding port; its exact value is
	// immaterial here since the no-tag planner declines before using it.
	_, ok, reason := openVINOPlanFor(conf.BackendPrefOpenVINO, conf.OVDeviceGPU, RegistryIDBat, "", 1)
	assert.False(t, ok)
	assert.Equal(t, ovReasonNotBuilt, reason)

	// Backend onnx is reported before a binary without OpenVINO, for every model
	// that plans through openVINOPlanFor (bat, BirdNET v3, Perch).
	_, ok, reason = openVINOPlanFor(conf.BackendPrefONNX, conf.OVDeviceGPU, RegistryIDBat, "", 1)
	assert.False(t, ok)
	assert.Equal(t, ovReasonBackendONNX, reason)
}

// TestOpenVINONotUsed_StandardBuildLogging verifies the noise-control policy on a
// non-openvino build: the auto path (the common case) stays silent so it does not
// add a line to every standard-build startup, while an explicit selection warns
// that the model is not loaded so the user learns their request could not be
// honored. Not parallel: it swaps the process-global logger.
func TestOpenVINONotUsed_StandardBuildLogging(t *testing.T) {
	buf := newCaptureLogger(t)

	require.NoError(t, openVINONotUsed(DefaultModelVersion, conf.BackendPrefAuto, conf.OVDeviceAuto, conf.OVDeviceAuto, ovReasonNotBuilt, nil))
	assert.Empty(t, buf.String(), "auto-path decline must be silent on a non-openvino build")

	require.Error(t, openVINONotUsed(DefaultModelVersion, conf.BackendPrefOpenVINO, conf.OVDeviceAuto, conf.OVDeviceAuto, ovReasonNotBuilt, nil))
	out := buf.String()
	assert.Contains(t, out, "it is not loaded", "explicit selection must warn even on a non-openvino build")
	assert.Contains(t, out, ovReasonNotBuilt.Description())
	assert.Contains(t, out, "reason_code="+string(ovReasonNotBuilt))
	assert.Contains(t, out, "level=WARN")

	buf.Reset()
	require.Error(t, openVINONotUsed(DefaultModelVersion, conf.BackendPrefAuto, conf.OVDeviceGPU, conf.OVDeviceGPU, ovReasonNotBuilt, nil))
	assert.Contains(t, buf.String(), "level=WARN", "a concrete device is an explicit selection and warns on auto")
}

// TestOpenVINOSelectionExplicit pins which settings count as an explicit OpenVINO
// selection. An unrecognized device plans as auto, so it is not explicit.
func TestOpenVINOSelectionExplicit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		backend, device string
		want            bool
	}{
		{conf.BackendPrefAuto, conf.OVDeviceAuto, false},
		{conf.BackendPrefAuto, "", false},
		{"", conf.OVDeviceAuto, false},
		{conf.BackendPrefAuto, "GPU", false},
		{conf.BackendPrefONNX, conf.OVDeviceGPU, false},
		{conf.BackendPrefONNX, conf.OVDeviceCPU, false},
		{conf.BackendPrefOpenVINO, conf.OVDeviceAuto, true},
		{conf.BackendPrefAuto, conf.OVDeviceCPU, true},
		{conf.BackendPrefAuto, conf.OVDeviceGPU, true},
		{conf.BackendPrefAuto, "GPU.1", true},
	}
	for _, tt := range tests {
		assert.Equalf(t, tt.want, openVINOSelectionExplicit(tt.backend, tt.device), "backend %q device %q", tt.backend, tt.device)
	}
}

// TestOpenVINOReasonDescription verifies every reason code has a description and
// that the codes keep the stable openvino. prefix.
func TestOpenVINOReasonDescription(t *testing.T) {
	t.Parallel()
	all := []OpenVINOReason{
		ovReasonNotBuilt, ovReasonBackendONNX, ovReasonGPUUnavailable, ovReasonGPUDeviceMissing,
		ovReasonCPUUnsupported, ovReasonGPUFailed, ovReasonNoDevice, ovReasonORTPreferredARM64,
		ovReasonNotBirdNETv24, ovReasonNotPerchNoDFT, ovReasonUnverifiedWeights, ovReasonInitFailed, ovReasonLoadFailed,
	}
	assert.Len(t, ovReasonDescriptions, len(all))
	for _, code := range all {
		assert.Truef(t, strings.HasPrefix(string(code), "openvino."), "code %q", code)
		assert.NotEqualf(t, string(code), code.Description(), "code %q has no description", code)
	}
	assert.Equal(t, "openvino.unknown", OpenVINOReason("openvino.unknown").Description())
}
