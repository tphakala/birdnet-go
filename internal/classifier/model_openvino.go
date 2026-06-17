package classifier

import (
	"os"
	"runtime"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/cpuspec"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// birdnetLogitsOutputIndex is the output port index of the BirdNET v2.4 logits
// (a single-output graph). Perch v2 uses a different index (see perchLogitsOutputIndex).
const birdnetLogitsOutputIndex = 0

// openVINOPlan describes how a model should run on the OpenVINO backend.
type openVINOPlan struct {
	device      string // inference.OVDeviceCPU or inference.OVDeviceGPU
	outputIndex int    // logits output port index
}

// openVINOPlanFor decides whether and how to run a model on the OpenVINO backend,
// returning the plan and true when OV should be attempted, or false to use ORT.
//
//   - backendPref:  settings.BirdNET.Backend ("auto"/"onnx"/"openvino").
//   - devicePref:   settings.BirdNET.OpenVINODevice ("auto"/"cpu"/"gpu").
//   - modelID:      the model registry identity (drives the GPU fence).
//   - libraryPath:  settings.BirdNET.OpenVINOPath (libopenvino_c location).
//   - outputIndex:  the logits output port for this model.
//
// BirdNET v2.4 is fenced off the GPU this release (only Perch may target the Intel
// iGPU). cpuspec.HasNativeF16() is true only on ARMv8.2+ (A76), which keeps amd64
// CPU out of the auto path (a non-win); an explicit "cpu" device is still allowed
// on amd64 for benchmarking/advanced use. The plan helper loads the OpenVINO core
// (idempotent) only when it must enumerate devices for a GPU decision, so it is
// independent of caller ordering.
func openVINOPlanFor(backendPref, devicePref, modelID, libraryPath string, outputIndex int) (openVINOPlan, bool) {
	if !openvinoBackendAvailable {
		return openVINOPlan{}, false
	}
	if backendPref == conf.BackendPrefONNX {
		return openVINOPlan{}, false
	}

	allowGPU := modelID != DefaultModelVersion // BirdNET v2.4 stays CPU-only.

	var device string
	switch devicePref {
	case conf.OVDeviceGPU:
		if !allowGPU || !openVINOGPUAvailable(libraryPath) {
			return openVINOPlan{}, false
		}
		device = inference.OVDeviceGPU
	case conf.OVDeviceCPU:
		if !openVINOCPUAllowed(devicePref) {
			return openVINOPlan{}, false
		}
		device = inference.OVDeviceCPU
	default: // "auto" or ""
		switch {
		case allowGPU && openVINOGPUAvailable(libraryPath):
			device = inference.OVDeviceGPU
		case openVINOCPUAllowed(devicePref):
			device = inference.OVDeviceCPU
		default:
			return openVINOPlan{}, false
		}
	}

	return openVINOPlan{device: device, outputIndex: outputIndex}, true
}

// openVINOCPUAllowed reports whether the OpenVINO CPU device may be used. The f16
// CPU kernels are safe only on ARMv8.2+ (HasNativeF16); on ARMv8.0 (A72) they
// would SIGILL, so the CPU path is rejected there. An explicit "cpu" preference
// on amd64 is allowed without the ARM f16 gate because the x86 CPU plugin is
// SIGILL-safe; auto never reaches this on amd64 (HasNativeF16 is false there).
func openVINOCPUAllowed(devicePref string) bool {
	if cpuspec.HasNativeF16() {
		return true
	}
	return devicePref == conf.OVDeviceCPU && runtime.GOARCH == "amd64"
}

// openVINOGPUAvailable reports whether an OpenVINO GPU device (Intel iGPU/dGPU via
// the Intel GPU plugin) is present. It loads the OpenVINO core first (InitOpenVINO
// is idempotent and fast on repeat); a load failure means no usable OV at all.
func openVINOGPUAvailable(libraryPath string) bool {
	if err := inference.InitOpenVINO(libraryPath); err != nil {
		return false
	}
	return inference.OpenVINOHasDevice(inference.OVDeviceGPU)
}

// openVINOPlan returns the OpenVINO plan for the primary BirdNET classifier, or
// false to use ORT. The primary classifier path applies sigmoid post-processing,
// so only the BirdNET v2.4 identity is valid here; Perch (softmax) runs its own
// OpenVINO path in the Perch ModelInstance (perch_onnx.go), never through this
// primary path. The plan also carries the device (CPU/GPU) and logits output index.
func (bn *BirdNET) openVINOPlan() (openVINOPlan, bool) {
	if bn.ModelInfo.ID != DefaultModelVersion {
		return openVINOPlan{}, false
	}
	return openVINOPlanFor(
		bn.Settings.BirdNET.Backend,
		bn.Settings.BirdNET.OpenVINODevice,
		bn.ModelInfo.ID,
		bn.Settings.BirdNET.OpenVINOPath,
		birdnetLogitsOutputIndex,
	)
}

// shouldTryOpenVINO reports whether the OpenVINO backend should be attempted for
// the primary classifier before falling back to ORT. True only when built with
// the openvino tag, the model is the BirdNET v2.4 identity, config does not opt
// out, and a supported device is available (ARM A76 f16 CPU; the GPU is fenced
// off for BirdNET this release).
func (bn *BirdNET) shouldTryOpenVINO() bool {
	_, ok := bn.openVINOPlan()
	return ok
}

// initializeOpenVINOModel loads the FP32 ONNX classifier via the OpenVINO
// backend. Returns a non-nil error on any failure so the caller falls back to
// ORT; it never panics, so a missing library or unsupported model cannot
// prevent startup.
func (bn *BirdNET) initializeOpenVINOModel() error {
	start := time.Now()
	log := GetLogger()
	settings := bn.Settings

	plan, ok := bn.openVINOPlan()
	if !ok {
		return errors.Newf("OpenVINO is not eligible for this model or host").
			Category(errors.CategoryModelInit).
			Context("model_id", bn.ModelInfo.ID).Build()
	}

	modelPath := bn.onnxModelPath()
	if modelPath == "" {
		return errors.Newf("OpenVINO classifier model path is empty").
			Category(errors.CategoryModelInit).
			Context("model_id", bn.ModelInfo.ID).Build()
	}
	rawPath := modelPath
	modelPath = os.ExpandEnv(modelPath)
	modelPath, err := conf.ExpandTildePath(modelPath)
	if err != nil {
		return errors.New(err).Category(errors.CategoryFileIO).Context("path", rawPath).Build()
	}

	if err := inference.InitOpenVINO(settings.BirdNET.OpenVINOPath); err != nil {
		return errors.New(err).Category(errors.CategoryModelInit).
			Context("openvino_path", settings.BirdNET.OpenVINOPath).
			Timing("openvino-init", time.Since(start)).Build()
	}

	classifier, err := inference.NewOpenVINOClassifier(modelPath, inference.OpenVINOClassifierOptions{
		Labels:      settings.BirdNET.Labels,
		Threads:     settings.BirdNET.Threads,
		Device:      plan.device,
		OutputIndex: plan.outputIndex,
	})
	if err != nil {
		return errors.New(err).Category(errors.CategoryModelInit).
			ModelContext(modelPath, bn.ModelInfo.ID).
			Timing("openvino-model-init", time.Since(start)).Build()
	}

	bn.classifier = classifier
	log.Info("OpenVINO model initialized",
		logger.String("model", modelPath),
		logger.String("device", plan.device),
		logger.Int("species", classifier.NumSpecies()),
		logger.String("init_time", time.Since(start).String()))
	return nil
}
