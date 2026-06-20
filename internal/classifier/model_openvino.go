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

// OpenVINO decline reasons. openVINOPlanFor / openVINOPlan return one of these as
// the third value when OpenVINO is not used, so the init path can log exactly why
// the backend was declined instead of falling back silently (see logOpenVINODeclined).
const (
	ovReasonNotBuilt       = "binary not built with OpenVINO support"
	ovReasonBackendONNX    = "backend is set to onnx"
	ovReasonGPUUnavailable = "gpu device requested but not available"
	ovReasonCPUUnsupported = "cpu device not supported on this host (needs an ARMv8.2+/A76 CPU with native f16)"
	ovReasonNoDevice       = "no supported OpenVINO device (needs an ARMv8.2+/A76 CPU with native f16, or an Intel OpenVINO GPU)"
	ovReasonNotBirdNETv24  = "model is not the stock BirdNET v2.4 classifier"
	ovReasonNotPerchNoDFT  = "model is not the Perch no_dft variant"
)

// openVINOPlan describes how a model should run on the OpenVINO backend.
type openVINOPlan struct {
	device      string // inference.OVDeviceCPU or inference.OVDeviceGPU
	outputIndex int    // logits output port index
	precision   string // INFERENCE_PRECISION_HINT; "" = backend default (f16)
}

// openVINOPlanFor decides whether and how to run a model on the OpenVINO backend,
// returning the plan and true when OV should be attempted, or false to use ORT.
// The third return value is a human-readable reason when OV is declined (empty
// when accepted), so callers can log why instead of falling back silently.
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
func openVINOPlanFor(backendPref, devicePref, modelID, libraryPath string, outputIndex int) (plan openVINOPlan, ok bool, reason string) {
	if !openvinoBackendAvailable {
		return openVINOPlan{}, false, ovReasonNotBuilt
	}
	if backendPref == conf.BackendPrefONNX {
		return openVINOPlan{}, false, ovReasonBackendONNX
	}

	var device string
	switch devicePref {
	case conf.OVDeviceGPU:
		if !openVINOGPUAvailable(libraryPath) {
			return openVINOPlan{}, false, ovReasonGPUUnavailable
		}
		device = inference.OVDeviceGPU
	case conf.OVDeviceCPU:
		if !openVINOCPUAllowed(devicePref) {
			return openVINOPlan{}, false, ovReasonCPUUnsupported
		}
		device = inference.OVDeviceCPU
	default: // "auto" or ""
		switch {
		case openVINOGPUAvailable(libraryPath):
			device = inference.OVDeviceGPU
		case openVINOCPUAllowed(devicePref):
			device = inference.OVDeviceCPU
		default:
			return openVINOPlan{}, false, ovReasonNoDevice
		}
	}

	return openVINOPlan{
		device:      device,
		outputIndex: outputIndex,
		precision:   openVINOPrecisionFor(modelID, device),
	}, true, ""
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
func (bn *BirdNET) openVINOPlan() (plan openVINOPlan, ok bool, reason string) {
	if bn.ModelInfo.ID != DefaultModelVersion {
		return openVINOPlan{}, false, ovReasonNotBirdNETv24
	}
	return openVINOPlanFor(
		bn.Settings.BirdNET.Backend,
		bn.Settings.BirdNET.OpenVINODevice,
		bn.ModelInfo.ID,
		bn.Settings.BirdNET.OpenVINOPath,
		birdnetLogitsOutputIndex,
	)
}

// shouldTryOpenVINO reports whether the OpenVINO backend is eligible for the
// primary classifier: the boolean form of openVINOPlan, dropping the plan and the
// decline reason. True only when built with the openvino tag, the model is the
// BirdNET v2.4 identity, config does not opt out, and a supported device is
// available (ARM A76 f16 CPU, or the Intel iGPU at f32; see openVINOPrecisionFor
// for why BirdNET v2.4 uses f32 on the GPU). initializeModel calls openVINOPlan
// directly so it can also log the decline reason; this predicate keeps the
// eligibility gate independently assertable in tests.
func (bn *BirdNET) shouldTryOpenVINO() bool {
	_, ok, _ := bn.openVINOPlan()
	return ok
}

// openVINOPrecisionLabel renders an OpenVINO precision hint for logging. An empty
// hint means the backend default, which is f16 (see openVINOPrecisionFor).
func openVINOPrecisionLabel(precision string) string {
	if precision == "" {
		return "f16"
	}
	return precision
}

// openVINOEffectivePrecision maps an OpenVINO INFERENCE_PRECISION_HINT to the
// effective runtime precision label shown on the inference status card, using the
// shared Quantization vocabulary ("FP16"/"FP32"). An empty hint means the backend
// default, which is f16 (see openVINOPrecisionFor), so it maps to FP16; the only
// explicit override currently emitted is OVPrecisionF32 (BirdNET v2.4 on the GPU),
// which maps to FP32.
func openVINOEffectivePrecision(precisionHint string) string {
	if precisionHint == inference.OVPrecisionF32 {
		return string(QuantizationFP32)
	}
	return string(QuantizationFP16)
}

// logOpenVINODeclined logs, once at model init, that the OpenVINO backend was not
// used and why. It logs at WARN when the user explicitly set backend=openvino
// (they asked for it and did not get it) and at INFO on the auto path (a normal,
// expected outcome). In a standard build that cannot use OpenVINO at all, the auto
// path is suppressed entirely so it does not add a line to every startup; an
// explicit opt-in on such a build still warns.
func logOpenVINODeclined(model, backendPref, reason string) {
	explicit := backendPref == conf.BackendPrefOpenVINO
	if !openvinoBackendAvailable && !explicit {
		return
	}
	fields := []logger.Field{
		logger.String("model", model),
		logger.String("reason", reason),
	}
	log := GetLogger()
	if explicit {
		log.Warn("OpenVINO requested but not used; using ONNX Runtime", fields...)
		return
	}
	log.Info("OpenVINO not used; using ONNX Runtime", fields...)
}

// initializeOpenVINOModel loads the FP32 ONNX classifier via the OpenVINO
// backend. Returns a non-nil error on any failure so the caller falls back to
// ORT; it never panics, so a missing library or unsupported model cannot
// prevent startup.
func (bn *BirdNET) initializeOpenVINOModel() error {
	start := time.Now()
	log := GetLogger()
	settings := bn.Settings

	plan, ok, reason := bn.openVINOPlan()
	if !ok {
		return errors.Newf("OpenVINO is not eligible for this model or host").
			Category(errors.CategoryModelInit).
			Context("model_id", bn.ModelInfo.ID).
			Context("reason", reason).Build()
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
	// Record the live backend and effective runtime precision so the status card
	// reports "OpenVINO" + the real compute precision (FP16 by default, FP32 only
	// for the BirdNET v2.4 GPU path) instead of the static ONNX file metadata.
	bn.backend = BackendOpenVINO
	bn.precision = openVINOEffectivePrecision(plan.precision)
	log.Info("OpenVINO model initialized",
		logger.String("model", modelPath),
		logger.String("device", plan.device),
		logger.String("precision", openVINOPrecisionLabel(plan.precision)),
		logger.Int("species", classifier.NumSpecies()),
		logger.String("init_time", time.Since(start).String()))
	return nil
}
