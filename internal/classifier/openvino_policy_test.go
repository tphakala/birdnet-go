package classifier

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference"
)

// TestOpenVINONotUsed_Policy pins the fallback rule: auto selection falls back to
// ONNX Runtime (nil), an explicit selection never does (a typed refusal), whatever
// the reason and whether the planner declined or a build failed after the plan.
func TestOpenVINONotUsed_Policy(t *testing.T) {
	t.Parallel()
	cause := errors.NewStd("compile failed")
	tests := []struct {
		name            string
		backend, device string
		reason          OpenVINOReason
		cause           error
		wantRefusal     bool
	}{
		{"auto/auto no_device", conf.BackendPrefAuto, conf.OVDeviceAuto, ovReasonNoDevice, nil, false},
		{"auto/auto gpu_unavailable", conf.BackendPrefAuto, conf.OVDeviceAuto, ovReasonGPUUnavailable, nil, false},
		{"auto/auto gpu_failed", conf.BackendPrefAuto, conf.OVDeviceAuto, ovReasonGPUFailed, nil, false},
		{"auto/auto not_perch_no_dft", conf.BackendPrefAuto, conf.OVDeviceAuto, ovReasonNotPerchNoDFT, nil, false},
		{"auto/auto unverified_weights", conf.BackendPrefAuto, conf.OVDeviceAuto, ovReasonUnverifiedWeights, nil, false},
		{"auto/auto load_failed after the plan", conf.BackendPrefAuto, conf.OVDeviceAuto, ovReasonLoadFailed, cause, false},
		{"empty/empty is auto", "", "", ovReasonNoDevice, nil, false},
		{"onnx/gpu never consults OpenVINO", conf.BackendPrefONNX, conf.OVDeviceGPU, ovReasonBackendONNX, nil, false},
		{"auto/gpu gpu_unavailable", conf.BackendPrefAuto, conf.OVDeviceGPU, ovReasonGPUUnavailable, nil, true},
		{"auto/gpu gpu_failed", conf.BackendPrefAuto, conf.OVDeviceGPU, ovReasonGPUFailed, nil, true},
		{"auto/gpu not_built", conf.BackendPrefAuto, conf.OVDeviceGPU, ovReasonNotBuilt, nil, true},
		{"auto/GPU.1 not_perch_no_dft", conf.BackendPrefAuto, "GPU.1", ovReasonNotPerchNoDFT, nil, true},
		{"auto/cpu cpu_unsupported", conf.BackendPrefAuto, conf.OVDeviceCPU, ovReasonCPUUnsupported, nil, true},
		{"openvino/auto no_device", conf.BackendPrefOpenVINO, conf.OVDeviceAuto, ovReasonNoDevice, nil, true},
		{"openvino/auto init_failed after the plan", conf.BackendPrefOpenVINO, conf.OVDeviceAuto, ovReasonInitFailed, cause, true},
		{"auto/gpu load_failed after the plan", conf.BackendPrefAuto, conf.OVDeviceGPU, ovReasonLoadFailed, cause, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := openVINONotUsed(RegistryIDPerchV2, tt.backend, tt.device, "GPU.0", tt.reason, tt.cause)
			if !tt.wantRefusal {
				assert.NoError(t, err)
				return
			}
			refusal, ok := errors.AsType[*OpenVINORefusalError](err)
			require.True(t, ok, "want an *OpenVINORefusalError, got %v", err)
			assert.Equal(t, RegistryIDPerchV2, refusal.ModelID)
			assert.Equal(t, tt.reason, refusal.Reason)
			assert.Equal(t, "GPU.0", refusal.Device)
			assert.Equal(t, tt.cause, refusal.Cause)
		})
	}
}

// TestOpenVINOBuildFailed pins the reason of a failure after the plan: the step,
// unless the error wraps the GPU fault sentinel (also through a builder).
func TestOpenVINOBuildFailed(t *testing.T) {
	t.Parallel()
	plain := errors.NewStd("compile failed")
	assert.Equal(t, ovReasonLoadFailed, openVINOBuildFailed(ovReasonLoadFailed, plain))
	assert.Equal(t, ovReasonInitFailed, openVINOBuildFailed(ovReasonInitFailed, plain))
	wrapped := errors.New(inference.ErrOpenVINOGPUFault).Category(errors.CategoryModelInit).Build()
	assert.Equal(t, ovReasonGPUFailed, openVINOBuildFailed(ovReasonLoadFailed, wrapped))
}

// TestOpenVINORefusal_UnwrapsCause checks that errors.Is reaches the GPU fault
// sentinel through a refusal.
func TestOpenVINORefusal_UnwrapsCause(t *testing.T) {
	t.Parallel()
	err := openVINOStepFailed(RegistryIDBat, conf.BackendPrefOpenVINO, conf.OVDeviceAuto, "GPU", ovReasonLoadFailed, inference.ErrOpenVINOGPUFault)
	refusal, ok := errors.AsType[*OpenVINORefusalError](err)
	require.True(t, ok)
	assert.Equal(t, ovReasonGPUFailed, refusal.Reason)
	require.ErrorIs(t, err, inference.ErrOpenVINOGPUFault)
}

// writeLabelFile writes a two-line label file for a family constructor.
func writeLabelFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "labels.txt")
	require.NoError(t, os.WriteFile(path, []byte("Sp one_Species One\nSp two_Species Two\n"), 0o600))
	return path
}

// explicitOpenVINOForms are the two ways a selection becomes explicit.
var explicitOpenVINOForms = []struct{ name, backend, device string }{
	{"backend openvino", conf.BackendPrefOpenVINO, conf.OVDeviceAuto},
	{"device gpu under auto", conf.BackendPrefAuto, conf.OVDeviceGPU},
}

// requireNoORTFallback asserts err is the not-built refusal for modelID.
func requireNoORTFallback(t *testing.T, err error, modelID string) {
	t.Helper()
	refusal, ok := errors.AsType[*OpenVINORefusalError](err)
	require.True(t, ok, "an explicit selection must refuse instead of building ONNX Runtime, got %v", err)
	assert.Equal(t, modelID, refusal.ModelID)
	assert.Equal(t, ovReasonNotBuilt, refusal.Reason)
}

// requireNotRefusal asserts err is not an OpenVINO refusal (auto falls back, so
// the constructor goes on to the ONNX Runtime build, which fails here only
// because the fixture model does not exist).
func requireNotRefusal(t *testing.T, err error) {
	t.Helper()
	_, ok := errors.AsType[*OpenVINORefusalError](err)
	assert.False(t, ok, "auto selection must keep the ONNX Runtime fallback, got %v", err)
}
