package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/inference"
)

// TestOpenVINOPrecisionFor verifies the per-(model, device) precision policy:
// BirdNET v2.4 and Perch v2 are forced to f32 on the GPU (the GPU f16 kernel
// miscompiles BirdNET v2.4 on Iris Xe and returns all-NaN Perch logits on an Arc
// A380), while the CPU paths keep the f16 default.
//
// This is intentionally NOT behind the openvino build tag: openVINOPrecisionFor
// lives in a tag-agnostic file and compiles into every build, but CI never builds
// the openvino tag, so a regression that reverted the f32 forcing (re-enabling the
// broken f16 GPU path) would otherwise pass CI green. This pure-policy check has no
// hardware dependency and runs in the default test suite.
func TestOpenVINOPrecisionFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		modelID string
		device  string
		want    string
	}{
		{
			name:    "birdnet v2.4 on GPU is forced to f32",
			modelID: DefaultModelVersion,
			device:  inference.OVDeviceGPU,
			want:    inference.OVPrecisionF32,
		},
		{
			name:    "birdnet v2.4 on CPU keeps the f16 default",
			modelID: DefaultModelVersion,
			device:  inference.OVDeviceCPU,
			want:    "",
		},
		{
			name:    "perch v2 on GPU is forced to f32 (f16 returns all-NaN logits on Intel Arc A380)",
			modelID: RegistryIDPerchV2,
			device:  inference.OVDeviceGPU,
			want:    inference.OVPrecisionF32,
		},
		{
			name:    "perch v2 on CPU keeps the f16 default",
			modelID: RegistryIDPerchV2,
			device:  inference.OVDeviceCPU,
			want:    "",
		},
		// The bat embedding model overflows at f16 on every device, so it must be
		// forced to f32 on BOTH GPU and CPU (unlike BirdNET v2.4, which is f32 on
		// GPU only).
		{
			name:    "bat on GPU is forced to f32",
			modelID: RegistryIDBat,
			device:  inference.OVDeviceGPU,
			want:    inference.OVPrecisionF32,
		},
		{
			name:    "bat on CPU is forced to f32 (f16 overflows the embedding head)",
			modelID: RegistryIDBat,
			device:  inference.OVDeviceCPU,
			want:    inference.OVPrecisionF32,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, openVINOPrecisionFor(tt.modelID, tt.device))
		})
	}
}

// TestOpenVINOEffectivePrecision verifies the mapping from an OpenVINO
// INFERENCE_PRECISION_HINT to the display precision shown on the inference status
// card. The empty default hint (f16) maps to FP16, and the explicit override
// (OVPrecisionF32: BirdNET v2.4 and Perch v2 on the GPU, bat everywhere) maps to
// FP32. Tag-agnostic
// like openVINOEffectivePrecision itself, so it runs in the default suite.
func TestOpenVINOEffectivePrecision(t *testing.T) {
	t.Parallel()
	assert.Equal(t, string(QuantizationFP16), openVINOEffectivePrecision(""),
		"empty hint is the backend f16 default, shown as FP16")
	assert.Equal(t, string(QuantizationFP32), openVINOEffectivePrecision(inference.OVPrecisionF32),
		"the f32 hint (BirdNET v2.4 and Perch v2 on the GPU) is shown as FP32")
}
