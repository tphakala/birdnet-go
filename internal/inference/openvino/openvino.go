// Package openvino provides a native OpenVINO inference backend for BirdNET
// classification, dynamically loaded from libopenvino_c at runtime. The real
// implementation is compiled only under the "openvino" build tag; other builds
// get stubs that report ErrOpenVINOUnavailable so callers degrade to ORT.
package openvino

import "github.com/tphakala/birdnet-go/internal/errors"

// DefaultPrecisionHint is the OpenVINO INFERENCE_PRECISION_HINT used for the
// f16 acceleration path on ARMv8.2 CPUs.
const DefaultPrecisionHint = "f16"

// ErrOpenVINOUnavailable is returned when the OpenVINO backend is not compiled
// in (no "openvino" build tag) or libopenvino_c cannot be loaded at runtime.
// Callers treat it as "fall back to ORT". Declared with errors.NewStd (the
// internal/errors passthrough to stdlib errors.New) so it is a plain sentinel
// usable with errors.Is.
var ErrOpenVINOUnavailable = errors.NewStd("openvino: backend unavailable")

// Classifier runs inference and returns raw pre-activation logits. It is NOT
// goroutine-safe; callers must serialize access (the classifier holds mu).
type Classifier interface {
	// PredictRaw runs one inference and returns raw logits in label order.
	PredictRaw(samples []float32) ([]float32, error)
	// Close releases the compiled model and infer request. It does not touch
	// the process-global core (see DestroyOV).
	Close() error
}

// Options configures a Classifier.
type Options struct {
	// PrecisionHint is the OpenVINO INFERENCE_PRECISION_HINT (e.g. "f16").
	PrecisionHint string
	// Threads sets INFERENCE_NUM_THREADS. 0 lets OpenVINO auto-tune.
	Threads int
}
