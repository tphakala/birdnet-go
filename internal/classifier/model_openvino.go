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
	precision   string // INFERENCE_PRECISION_HINT; "" = backend default (f16)
}

// openVINOPlanFor decides whether and how to run a model on the OpenVINO backend,
// returning the plan and true when OV should be attempted, or false to use ORT.
//
//   - backendPref:  settings.BirdNET.Backend ("auto"/"onnx"/"openvino").
//   - devicePref:   settings.BirdNET.OpenVINODevice ("auto"/"cpu"/"gpu").
//   - modelID:      the model registry identity (drives the precision policy).
//   - libraryPath:  settings.BirdNET.OpenVINOPath (libopenvino_c location).
//   - outputIndex:  the logits output port for this model.
//
// Both BirdNET v2.4 and Perch may target the Intel iGPU; the per-(model, device)
// precision is chosen by openVINOPrecisionFor (BirdNET v2.4 is forced to f32 on
// the GPU because the GPU f16 kernel miscompiles it). cpuspec.HasNativeF16() is
// true only on ARMv8.2+ (A76), which keeps amd64 CPU out of the auto path (a
// non-win); an explicit "cpu" device is still allowed on amd64 for
// benchmarking/advanced use. The plan helper loads the OpenVINO core (idempotent)
// only when it must enumerate devices for a GPU decision, so it is independent of
// caller ordering.
func openVINOPlanFor(backendPref, devicePref, modelID, libraryPath string, outputIndex int) (openVINOPlan, bool) {
	if !openvinoBackendAvailable {
		return openVINOPlan{}, false
	}
	if backendPref == conf.BackendPrefONNX {
		return openVINOPlan{}, false
	}

	var device string
	switch devicePref {
	case conf.OVDeviceGPU:
		if !openVINOGPUAvailable(libraryPath) {
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
		case openVINOGPUAvailable(libraryPath):
			device = inference.OVDeviceGPU
		case openVINOCPUAllowed(devicePref):
			device = inference.OVDeviceCPU
		default:
			return openVINOPlan{}, false
		}
	}

	return openVINOPlan{
		device:      device,
		outputIndex: outputIndex,
		precision:   openVINOPrecisionFor(modelID, device),
	}, true
}

// openVINOPrecisionFor returns the INFERENCE_PRECISION_HINT for a (model, device)
// pair, or "" to use the backend default (f16).
//
// BirdNET v2.4 is forced to f32 on the GPU: the Intel GPU plugin's f16 kernel
// miscompiles this single-output sigmoid model. Validated on an Iris Xe iGPU
// (2026-06-18), f16 collapses on realistic low-SNR audio (max confidence error
// ~0.8, wrong top-1, confidences fall to ~0) while a loud single-species clip
// survives by luck; f32 is bit-exact with ORT (~6e-6) and still ~4.6x faster than
// ORT CPU. CPU f16 (incl. ARM A76) and Perch v2 f16-GPU are unaffected, so the
// override is scoped to BirdNET-on-GPU. Do NOT widen it to f16 without re-running
// the inference/openvino_parity_functional_test.go soundscape parity check.
func openVINOPrecisionFor(modelID, device string) string {
	if device == inference.OVDeviceGPU && modelID == DefaultModelVersion {
		return inference.OVPrecisionF32
	}
	return ""
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
		// Surface the load failure: without this, an explicit openvino/gpu opt-in
		// with a bad OpenVINOPath would silently fall back to ORT carrying only a
		// generic "not eligible" message, hiding the real library-load cause.
		GetLogger().Warn("OpenVINO library load failed during device planning; treating GPU as unavailable",
			logger.Error(err))
		return false
	}
	return inference.OpenVINOHasDevice(inference.OVDeviceGPU)
}

// openVINOPlan returns the OpenVINO plan for the primary BirdNET classifier, or
// false to use ORT. The primary classifier path applies sigmoid post-processing,
// so only the BirdNET v2.4 identity is valid here; Perch (softmax) runs its own
// OpenVINO path in the Perch ModelInstance (perch_onnx.go), never through this
// primary path. The plan carries the device (CPU/GPU), logits output index, and
// the precision hint (f32 on the GPU for BirdNET v2.4; see openVINOPrecisionFor).
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
// out, and a supported device is available (ARM A76 f16 CPU, or the Intel iGPU at
// f32; see openVINOPrecisionFor for why BirdNET v2.4 uses f32 on the GPU).
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
		Labels:        settings.BirdNET.Labels,
		Threads:       settings.BirdNET.Threads,
		Device:        plan.device,
		OutputIndex:   plan.outputIndex,
		PrecisionHint: plan.precision,
	})
	if err != nil {
		return errors.New(err).Category(errors.CategoryModelInit).
			ModelContext(modelPath, bn.ModelInfo.ID).
			Timing("openvino-model-init", time.Since(start)).Build()
	}

	bn.classifier = classifier
	// Record the concrete OpenVINO device (CPU/GPU) the classifier bound to so
	// Device() reports the real execution provider rather than the backend string.
	bn.device = plan.device
	log.Info("OpenVINO model initialized",
		logger.String("model", modelPath),
		logger.String("device", plan.device),
		logger.Int("species", classifier.NumSpecies()),
		logger.String("init_time", time.Since(start).String()))
	return nil
}
