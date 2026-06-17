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
// supported model, and no opt-out, the native-f16 (ASIMDHP) capability is the final
// deciding factor: true on ARMv8.2 (rpi5), false elsewhere (amd64). This makes the
// test robust on any host.
func TestShouldTryOpenVINO_Tagged_HardwareGateDecides(t *testing.T) {
	t.Parallel()
	bn := &BirdNET{Settings: &conf.Settings{}}
	bn.Settings.BirdNET.Backend = conf.BackendPrefOpenVINO
	bn.ModelInfo = ModelInfo{ID: DefaultModelVersion, Backend: BackendONNX}
	assert.Equal(t, cpuspec.HasNativeF16(), bn.shouldTryOpenVINO(),
		"with tag, supported model, and opt-in, the result must equal native-f16 capability")
}

// TestOpenVINOPlan_BirdNETFencedOffGPU verifies BirdNET v2.4 never targets the
// GPU: an explicit device=gpu yields no plan, and an auto plan (when produced)
// is always the CPU device. These cases never enumerate devices (the GPU branch
// short-circuits on the BirdNET fence), so the test needs no libopenvino_c.
func TestOpenVINOPlan_BirdNETFencedOffGPU(t *testing.T) {
	t.Parallel()
	_, ok := openVINOPlanFor(conf.BackendPrefOpenVINO, conf.OVDeviceGPU, DefaultModelVersion, "", birdnetLogitsOutputIndex)
	assert.False(t, ok, "BirdNET v2.4 + device=gpu must not produce a plan (GPU is fenced off)")

	if plan, ok := openVINOPlanFor(conf.BackendPrefAuto, conf.OVDeviceAuto, DefaultModelVersion, "", birdnetLogitsOutputIndex); ok {
		assert.Equal(t, inference.OVDeviceCPU, plan.device, "BirdNET auto plan must be CPU, never GPU")
	}
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
