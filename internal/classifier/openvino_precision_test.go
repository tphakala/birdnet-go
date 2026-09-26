package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/hwprofile"
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
		// BirdNET v3.0 (EfficientNetV2-S) is numerically unstable at f16 wherever
		// genuine f16 kernels run (the Intel GPU, and the A76 CPU's native f16):
		// fp16-weight regional tiles overflow to NaN and fp32-weight tiles silently
		// inflate the scores. Like bat, it must be f32 on BOTH devices, not GPU-only.
		// See BIRDNET-GO-2H6.
		{
			name:    "birdnet v3.0 on GPU is forced to f32",
			modelID: RegistryIDBirdNETV3,
			device:  inference.OVDeviceGPU,
			want:    inference.OVPrecisionF32,
		},
		{
			name:    "birdnet v3.0 on CPU is forced to f32 (A76 native f16 corrupts scores)",
			modelID: RegistryIDBirdNETV3,
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
// (OVPrecisionF32: BirdNET v2.4 and Perch v2 on the GPU, bat and BirdNET v3.0 on
// every device) maps to FP32. Tag-agnostic
// like openVINOEffectivePrecision itself, so it runs in the default suite.
func TestOpenVINOEffectivePrecision(t *testing.T) {
	t.Parallel()
	assert.Equal(t, string(QuantizationFP16), openVINOEffectivePrecision(""),
		"empty hint is the backend f16 default, shown as FP16")
	assert.Equal(t, string(QuantizationFP32), openVINOEffectivePrecision(inference.OVPrecisionF32),
		"the f32 hint (BirdNET v2.4 and Perch v2 on the GPU, bat and BirdNET v3.0 on every device) is shown as FP32")
}

// TestBackendForcesFP32 verifies the backend-token predicate the model-gallery
// recommender uses to decide whether a variant's declared file precision (for
// example an fp16 model) will actually run at f16 on a given host backend, or be
// overridden to f32. Only the OpenVINO backends carry an INFERENCE_PRECISION_HINT
// that can override the file precision; ONNX Runtime and the CUDA/TensorRT
// compute backends run the file as stored. The truth table mirrors
// openVINOPrecisionFor: bat and BirdNET v3.0 are f32 on every OpenVINO device,
// BirdNET v2.4 and Perch v2 only on the OpenVINO GPU.
//
// Tag-agnostic like BackendForcesFP32 itself, so it runs in the default suite.
func TestBackendForcesFP32(t *testing.T) {
	t.Parallel()

	// cudaBackendToken is a representative non-OpenVINO compute backend: only the
	// OpenVINO backends carry a precision hint, so a CUDA host runs the file as
	// stored and never forces f32.
	const cudaBackendToken = "cuda"

	tests := []struct {
		name         string
		registryID   string
		backendToken string
		want         bool
	}{
		{
			name:         "birdnet v3.0 on openvino cpu is forced to f32",
			registryID:   RegistryIDBirdNETV3,
			backendToken: hwprofile.CapOpenVINOCPU,
			want:         true,
		},
		{
			name:         "birdnet v3.0 on openvino gpu is forced to f32",
			registryID:   RegistryIDBirdNETV3,
			backendToken: hwprofile.CapOpenVINOGPU,
			want:         true,
		},
		{
			name:         "birdnet v3.0 on onnx runtime cpu runs the file as stored",
			registryID:   RegistryIDBirdNETV3,
			backendToken: hwprofile.CapONNXRuntimeCPU,
			want:         false,
		},
		{
			name:         "birdnet v3.0 on a non-openvino gpu backend runs the file as stored",
			registryID:   RegistryIDBirdNETV3,
			backendToken: cudaBackendToken,
			want:         false,
		},
		{
			name:         "bat on openvino cpu is forced to f32",
			registryID:   RegistryIDBat,
			backendToken: hwprofile.CapOpenVINOCPU,
			want:         true,
		},
		{
			name:         "birdnet v2.4 on openvino cpu keeps the f16 default",
			registryID:   DefaultModelVersion,
			backendToken: hwprofile.CapOpenVINOCPU,
			want:         false,
		},
		{
			name:         "birdnet v2.4 on openvino gpu is forced to f32",
			registryID:   DefaultModelVersion,
			backendToken: hwprofile.CapOpenVINOGPU,
			want:         true,
		},
		{
			name:         "perch v2 on openvino cpu keeps the f16 default",
			registryID:   RegistryIDPerchV2,
			backendToken: hwprofile.CapOpenVINOCPU,
			want:         false,
		},
		{
			name:         "unknown model on openvino cpu forces nothing",
			registryID:   "",
			backendToken: hwprofile.CapOpenVINOCPU,
			want:         false,
		},
		{
			name:         "empty backend token is not an openvino backend",
			registryID:   RegistryIDBirdNETV3,
			backendToken: "",
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, BackendForcesFP32(tt.registryID, tt.backendToken))
		})
	}
}
