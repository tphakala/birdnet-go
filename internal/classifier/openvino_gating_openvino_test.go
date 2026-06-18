//go:build openvino

package classifier

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/cpuspec"
	"github.com/tphakala/birdnet-go/internal/inference"
)

// TestShouldTryOpenVINO_Tagged_OptOut verifies Backend="onnx" opts out even when the
// openvino backend is compiled in.
func TestShouldTryOpenVINO_Tagged_OptOut(t *testing.T) {
	t.Parallel()
	bn := &BirdNET{Settings: &conf.Settings{}}
	bn.Settings.BirdNET.Backend = conf.BackendPrefONNX
	bn.ModelInfo = ModelInfo{ID: DefaultModelVersion, Backend: BackendONNX}
	assert.False(t, bn.shouldTryOpenVINO(), "Backend=onnx must opt out even with the openvino tag")
}

// TestShouldTryOpenVINO_Tagged_WrongModel verifies a non-v2.4 model never uses OpenVINO.
func TestShouldTryOpenVINO_Tagged_WrongModel(t *testing.T) {
	t.Parallel()
	bn := &BirdNET{Settings: &conf.Settings{}}
	bn.Settings.BirdNET.Backend = conf.BackendPrefAuto
	bn.ModelInfo = ModelInfo{ID: "BirdNET_V2.3", Backend: BackendONNX}
	assert.False(t, bn.shouldTryOpenVINO(), "a non-v2.4 model must not use OpenVINO in the PoC")
}

// TestShouldTryOpenVINO_Tagged_HardwareGateDecides verifies that with the tag, a
// supported model, and no opt-out, the hardware is the final deciding factor:
// eligibility is true when the host has ARM A76 f16 CPU acceleration OR an
// available Intel OpenVINO GPU (BirdNET v2.4 runs there at f32). This keeps the
// test robust on any host (rpi5 A76, amd64+iGPU, or amd64 CPU-only).
func TestShouldTryOpenVINO_Tagged_HardwareGateDecides(t *testing.T) {
	t.Parallel()
	bn := &BirdNET{Settings: &conf.Settings{}}
	bn.Settings.BirdNET.Backend = conf.BackendPrefOpenVINO
	bn.ModelInfo = ModelInfo{ID: DefaultModelVersion, Backend: BackendONNX}
	// Compute the same hardware availability the planner uses (auto path: GPU
	// preferred when present, else the ARM f16 CPU gate). The hardware portion of
	// `expected` is intentionally mirror-logic of the implementation, so it proves
	// nothing about the device decision itself; what this test actually guards is
	// that the tag/opt-in/model gates upstream do NOT block eligibility (a
	// regression that ignored the openvino tag, the BirdNET v2.4 model gate, or the
	// opt-in would make `shouldTryOpenVINO` diverge from `expected` on a capable
	// host). The fixed-expectation gate coverage lives in the sibling _OptOut,
	// _WrongModel, and TestOpenVINOPlan_* tests.
	expected := cpuspec.HasNativeF16() || openVINOGPUAvailable("")
	assert.Equal(t, expected, bn.shouldTryOpenVINO(),
		"with tag, supported model, and opt-in, eligibility must equal ARM f16 CPU capability or OV GPU availability")
}

// TestOpenVINOPlan_ExplicitCPU verifies the explicit CPU device gate: allowed on
// ARMv8.2 (HasNativeF16) and on amd64 (x86 OV CPU is SIGILL-safe), rejected on
// ARMv8.0 (A72). It carries the requested output index through. The explicit-CPU
// path never enumerates devices, so no libopenvino_c is required.
func TestOpenVINOPlan_ExplicitCPU(t *testing.T) {
	t.Parallel()
	plan, ok := openVINOPlanFor(conf.BackendPrefOpenVINO, conf.OVDeviceCPU, RegistryIDPerchV2, "", perchLogitsOutputIndex)
	expected := cpuspec.HasNativeF16() || runtime.GOARCH == "amd64"
	assert.Equal(t, expected, ok, "explicit CPU is allowed on ARM A76 or amd64, not on ARM A72")
	if ok {
		assert.Equal(t, inference.OVDeviceCPU, plan.device)
		assert.Equal(t, perchLogitsOutputIndex, plan.outputIndex)
	}
}

// TestOpenVINOPlan_ONNXOptOut verifies Backend=onnx opts out for any model,
// including Perch, without touching the OpenVINO library.
func TestOpenVINOPlan_ONNXOptOut(t *testing.T) {
	t.Parallel()
	_, ok := openVINOPlanFor(conf.BackendPrefONNX, conf.OVDeviceAuto, RegistryIDPerchV2, "", perchLogitsOutputIndex)
	assert.False(t, ok, "Backend=onnx must opt out of OpenVINO for Perch too")
}
