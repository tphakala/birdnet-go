// Package openvino provides a native OpenVINO inference backend for BirdNET
// classification, dynamically loaded from libopenvino_c at runtime. The real
// implementation is compiled only under the "openvino" build tag; other builds
// get stubs that report ErrOpenVINOUnavailable so callers degrade to ORT.
package openvino

import "github.com/tphakala/birdnet-go/internal/errors"

// DefaultPrecisionHint is the OpenVINO INFERENCE_PRECISION_HINT used for the
// f16 acceleration path on ARMv8.2 CPUs and the Intel iGPU.
const DefaultPrecisionHint = "f16"

const (
	// DeviceCPU is the OpenVINO CPU device. ARMv8.2 f16 acceleration runs here.
	DeviceCPU = "CPU"
	// DeviceGPU is the OpenVINO GPU device (Intel iGPU via intel_gpu_plugin). It
	// supports f16 natively and offloads inference from the CPU cores.
	DeviceGPU = "GPU"
)

// ErrOpenVINOUnavailable is returned when the OpenVINO backend is not compiled
// in (no "openvino" build tag) or libopenvino_c cannot be loaded at runtime.
// Callers treat it as "fall back to ORT". Declared with errors.NewStd (the
// internal/errors passthrough to stdlib errors.New) so it is a plain sentinel
// usable with errors.Is.
var ErrOpenVINOUnavailable = errors.NewStd("openvino: backend unavailable")

// Classifier runs inference and returns raw pre-activation logits. It is NOT
// goroutine-safe; callers must serialize access (BirdNET.mu serializes the full
// native call; this implementation has no internal mutex).
type Classifier interface {
	// PredictRaw runs one inference and returns raw logits in label order.
	PredictRaw(samples []float32) ([]float32, error)
	// NumClasses reports the element count of the model's selected logits output,
	// read from the compiled model (not derived from the label list). Callers use
	// it to validate the model output against the label count.
	NumClasses() int
	// Close releases the compiled model and infer request. It does not touch
	// the process-global core (see DestroyOV).
	Close() error
}

// Options configures a Classifier.
type Options struct {
	// PrecisionHint is the OpenVINO INFERENCE_PRECISION_HINT (e.g. "f16").
	PrecisionHint string
	// Threads sets INFERENCE_NUM_THREADS (CPU device only; ignored for GPU).
	// 0 lets OpenVINO auto-tune.
	Threads int
	// Device is the OpenVINO device to compile for: DeviceCPU (default) or
	// DeviceGPU. An empty string is treated as DeviceCPU.
	Device string
	// OutputIndex is the index of the logits output port to read. 0 (default)
	// for single-output models like BirdNET v2.4; 3 for the Perch v2 multi-output
	// graph whose species logits are the 4th output.
	OutputIndex int
}
