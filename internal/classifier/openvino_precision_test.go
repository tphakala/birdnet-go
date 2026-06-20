package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/inference"
)

// TestOpenVINOPrecisionFor verifies the per-(model, device) precision policy:
// BirdNET v2.4 is forced to f32 on the GPU (its GPU f16 kernel miscompiles this
// model; validated on Iris Xe), while CPU and Perch keep the f16 default.
//
// This is intentionally NOT behind the openvino build tag: openVINOPrecisionFor
// lives in a tag-agnostic file and compiles into every build, but CI never builds
// the openvino tag, so a regression that reverted the f32 forcing (re-enabling the
// broken f16 GPU path) would otherwise pass CI green. This pure-policy check has no
// hardware dependency and runs in the default test suite.
func TestOpenVINOPrecisionFor(t *testing.T) {
	t.Parallel()
	assert.Equal(t, inference.OVPrecisionF32, openVINOPrecisionFor(DefaultModelVersion, inference.OVDeviceGPU),
		"BirdNET v2.4 on the GPU must be forced to f32")
	assert.Empty(t, openVINOPrecisionFor(DefaultModelVersion, inference.OVDeviceCPU),
		"BirdNET v2.4 on CPU keeps the f16 default")
	assert.Empty(t, openVINOPrecisionFor(RegistryIDPerchV2, inference.OVDeviceGPU),
		"Perch on the GPU keeps f16 (validated parity with f32)")
	assert.Empty(t, openVINOPrecisionFor(RegistryIDPerchV2, inference.OVDeviceCPU),
		"Perch on CPU keeps the f16 default")
}

// TestOpenVINOEffectivePrecision verifies the mapping from an OpenVINO
// INFERENCE_PRECISION_HINT to the display precision shown on the inference status
// card. The empty default hint (f16) maps to FP16, and the only explicit override
// emitted (OVPrecisionF32, the BirdNET v2.4 GPU path) maps to FP32. Tag-agnostic
// like openVINOEffectivePrecision itself, so it runs in the default suite.
func TestOpenVINOEffectivePrecision(t *testing.T) {
	t.Parallel()
	assert.Equal(t, string(QuantizationFP16), openVINOEffectivePrecision(""),
		"empty hint is the backend f16 default, shown as FP16")
	assert.Equal(t, string(QuantizationFP32), openVINOEffectivePrecision(inference.OVPrecisionF32),
		"the f32 hint (BirdNET v2.4 GPU) is shown as FP32")
}
