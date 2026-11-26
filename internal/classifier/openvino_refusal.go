package classifier

import (
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// OpenVINORefusalError is the typed error a model returns when OpenVINO cannot run it
// and the user's selection forbids ONNX Runtime (see openVINOSelectionExplicit):
// the model is left unloaded instead of falling back. Reason is the stable code;
// Device is the configured OpenVINO device preference for a planner decline and
// the planned compile target (CPU, GPU or GPU.<n>) for a failure after the plan.
// Cause carries the underlying error for logs and never reaches the API.
type OpenVINORefusalError struct {
	ModelID string
	Reason  OpenVINOReason
	Device  string
	Cause   error
}

// Error describes the refusal without paths: the model, the stable code and, when
// there is one, the cause.
func (r *OpenVINORefusalError) Error() string {
	msg := "OpenVINO cannot run model " + r.ModelID + ": " + r.Reason.Description() + " (" + string(r.Reason) + ")"
	if r.Cause != nil {
		msg += ": " + r.Cause.Error()
	}
	return msg
}

// Unwrap returns the cause so errors.Is and errors.As reach it, such as
// inference.ErrOpenVINOGPUFault.
func (r *OpenVINORefusalError) Unwrap() error { return r.Cause }

// openVINOBuildFailed returns the reason code for a failure after an accepted
// plan: gpu_failed when the error wraps the GPU fault sentinel, else step
// (ovReasonInitFailed or ovReasonLoadFailed).
func openVINOBuildFailed(step OpenVINOReason, err error) OpenVINOReason {
	if errors.Is(err, inference.ErrOpenVINOGPUFault) {
		return ovReasonGPUFailed
	}
	return step
}

// openVINONotUsed is the single place that handles OpenVINO not running a model,
// whether the planner declined (cause nil) or a build failed after the plan. The
// selection decides: with auto it returns nil (logging, except for a planner decline on a build without OpenVINO, which stays silent), so the caller builds the
// ONNX Runtime instance; with an explicit selection it logs at WARN and returns an
// *OpenVINORefusalError, so the caller leaves the model unloaded. Backend onnx is never
// explicit, so it always gets nil. device is the configured preference for a
// decline and the planned target for a failure.
func openVINONotUsed(modelID, backendPref, devicePref, device string, reason OpenVINOReason, cause error) error {
	explicit := openVINOSelectionExplicit(backendPref, devicePref)
	if !explicit && cause == nil && !openvinoBackendAvailable {
		// A build without OpenVINO stays silent on the auto path: a line at every
		// startup would add nothing.
		return nil
	}
	fields := []logger.Field{
		logger.String("model", modelID),
		logger.String("reason", reason.Description()),
		logger.String("reason_code", string(reason)),
		logger.String("device", device),
	}
	if cause != nil {
		fields = append(fields, logger.Error(cause))
	}
	log := GetLogger()
	if explicit {
		log.Warn("OpenVINO cannot run this model; it is not loaded (no ONNX Runtime fallback with an explicit OpenVINO selection)", fields...)
		return &OpenVINORefusalError{ModelID: modelID, Reason: reason, Device: device, Cause: cause}
	}
	if cause != nil {
		log.Warn("OpenVINO failed; using ONNX Runtime", fields...)
	} else {
		log.Info("OpenVINO not used; using ONNX Runtime", fields...)
	}
	return nil
}

// openVINOStepFailed is openVINONotUsed for a failure after an accepted plan: the
// reason is the step (init or load) unless the error is the GPU fault.
func openVINOStepFailed(modelID, backendPref, devicePref, device string, step OpenVINOReason, err error) error {
	return openVINONotUsed(modelID, backendPref, devicePref, device, openVINOBuildFailed(step, err), err)
}
