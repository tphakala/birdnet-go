package classifier

import (
	"os"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/cpuspec"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/hwprofile"
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
	ovReasonNotBuilt          = "binary not built with OpenVINO support"
	ovReasonBackendONNX       = "backend is set to onnx"
	ovReasonGPUUnavailable    = "gpu device requested but not available"
	ovReasonCPUUnsupported    = "cpu device not supported on this host (needs an ARMv8.2+/A76 CPU with native f16)"
	ovReasonNoDevice          = "no supported OpenVINO device (needs an ARMv8.2+/A76 CPU with native f16, or an Intel OpenVINO GPU)"
	ovReasonORTPreferredARM64 = "ONNX Runtime is preferred on arm64 CPUs; set birdnet.backend to openvino to use the OpenVINO CPU"
	ovReasonNotBirdNETv24     = "model is not the stock BirdNET v2.4 classifier"
	ovReasonNotPerchNoDFT     = "model is not the Perch no_dft variant"
	ovReasonUnverifiedWeights = "model weights are INT8 or of unrecognized precision, which are not validated on OpenVINO (set birdnet.backend to openvino to force OpenVINO at f32)"
)

// Go architecture names (runtime.GOARCH values) the OpenVINO device policy keys on.
const (
	goarchARM64 = "arm64"
	goarchAMD64 = "amd64"
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
// every device because both the GPU and the A76 CPU f16 paths break it).
// cpuspec.HasNativeF16() is true only on ARMv8.2+ (A76), which keeps amd64 CPU out
// of the auto path (a non-win); an explicit "cpu" device is still allowed on amd64
// for benchmarking/advanced use. On arm64 the auto backend declines the OpenVINO
// CPU before any OpenVINO library is loaded (see autoDeclinesOpenVINOCPU); backend
// "openvino" or device "cpu" opts back in. Perch, bat and v3.0 require ONNX
// Runtime before they plan (their builders call checkORTOrFail), so this entry
// point passes ONNX Runtime as available and auto always declines the arm64 CPU
// for them; BirdNET v2.4 plans through birdnetV24BasePlan, which passes the live
// answer. The plan helper loads the OpenVINO core (idempotent) only when it must
// enumerate devices for a GPU decision, so it is independent of caller ordering.
func openVINOPlanFor(backendPref, devicePref, modelID, libraryPath string, outputIndex int) (plan openVINOPlan, ok bool, reason string) {
	return openVINOPlanWithHost(backendPref, devicePref, modelID, outputIndex, liveOVHostFacts(libraryPath, true))
}

// openVINOPlanWithHost is openVINOPlanFor with the host facts supplied by the
// caller, so BirdNET v2.4 can pass its own ONNX Runtime answer.
func openVINOPlanWithHost(backendPref, devicePref, modelID string, outputIndex int, host *ovHostFacts) (plan openVINOPlan, ok bool, reason string) {
	if !openvinoBackendAvailable {
		return openVINOPlan{}, false, ovReasonNotBuilt
	}
	device, ok, reason := planOpenVINODevice(backendPref, devicePref, host)
	if !ok {
		return openVINOPlan{}, false, reason
	}
	return openVINOPlan{
		device:      device,
		outputIndex: outputIndex,
		precision:   openVINOPrecisionFor(modelID, device),
	}, true, ""
}

// ovHostFacts are the host facts the OpenVINO device decision reads. The GPU probe
// is a lazy closure because it loads the OpenVINO library and spawns a child
// process, so it must run only on the branches that need it.
type ovHostFacts struct {
	goarch       string      // runtime.GOARCH
	nativeF16    bool        // cpuspec.HasNativeF16()
	ortAvailable bool        // ONNX Runtime is available for this model's settings
	gpuAvailable func() bool // an OpenVINO GPU device is present
}

// liveOVHostFacts returns the facts of the running host with the given ONNX
// Runtime answer.
func liveOVHostFacts(libraryPath string, ortAvailable bool) *ovHostFacts {
	return &ovHostFacts{
		goarch:       runtime.GOARCH,
		nativeF16:    cpuspec.HasNativeF16(),
		ortAvailable: ortAvailable,
		gpuAvailable: func() bool { return openVINOGPUAvailable(libraryPath) },
	}
}

// planOpenVINODevice picks the OpenVINO device for the settings and host facts,
// or declines with a reason. It is pure apart from the lazy probes and reads
// nothing cached, so a settings change takes effect on the next call.
func planOpenVINODevice(backendPref, devicePref string, host *ovHostFacts) (device string, ok bool, reason string) {
	if backendPref == conf.BackendPrefONNX {
		return "", false, ovReasonBackendONNX
	}

	switch devicePref {
	case conf.OVDeviceGPU:
		if !host.gpuAvailable() {
			return "", false, ovReasonGPUUnavailable
		}
		return inference.OVDeviceGPU, true, ""
	case conf.OVDeviceCPU:
		if !host.cpuAllowed(devicePref) {
			return "", false, ovReasonCPUUnsupported
		}
		return inference.OVDeviceCPU, true, ""
	default: // "auto" or ""
		// Decline before the GPU probe so this decision neither loads
		// libopenvino_c nor spawns the probe child. On a CPU without native f16 the
		// OpenVINO CPU could not be used anyway, so keep the no-device reason there:
		// telling the user to set backend=openvino would not help.
		if backendPref != conf.BackendPrefOpenVINO && autoDeclinesOpenVINOCPU(host.goarch, host.nativeF16, host.ortAvailable) {
			if !host.nativeF16 {
				return "", false, ovReasonNoDevice
			}
			return "", false, ovReasonORTPreferredARM64
		}
		switch {
		case host.gpuAvailable():
			return inference.OVDeviceGPU, true, ""
		case host.cpuAllowed(devicePref):
			return inference.OVDeviceCPU, true, ""
		default:
			return "", false, ovReasonNoDevice
		}
	}
}

// openVINOPrecisionFor returns the INFERENCE_PRECISION_HINT for a (model, device)
// pair, or "" to use the backend default (f16).
//
// BirdNET v2.4 is forced to f32 on EVERY device. On the Intel GPU the f16 kernel
// miscompiles this single-output sigmoid model. Validated on an Iris Xe iGPU
// (2026-06-18), f16 collapses on realistic low-SNR audio (max confidence error
// ~0.8, wrong top-1, confidences fall to ~0) while a loud single-species clip
// survives by luck; f32 is bit-exact with ORT (~6e-6) and still ~4.6x faster than
// ORT CPU. The A76 CPU's native f16 fails on low-frequency-heavy audio (wind,
// handling noise): with the static [1, 144000] input shape, windows whose power is
// mostly below about 150 Hz lose every score (GitHub #4986), while f32 tracks the
// TFLite reference. Do NOT relax it to f16 on any device without re-running
// TestBirdNETV24OpenVINO_LowFrequencyParity on a genuine f16 device with
// low-frequency-heavy windows. INT8-weight files are additionally declined on
// auto; that depends on the file, not the registry ID, so it is handled by
// applyOpenVINOQuantizationPolicy.
//
// Perch v2 is likewise forced to f32 on the GPU. Its f16-GPU path was validated
// on an Iris Xe iGPU, but on an Intel Arc A380 (dGPU) the f16 kernel returns
// all-NaN logits for every window, which the pipeline then promoted to bogus
// detections. The plan carries only the device class ("GPU"), not the adapter,
// so the override necessarily covers every Intel GPU; f32 costs some throughput
// on adapters where f16 worked, but stays well within real time. Do NOT relax it
// to f16 without validating on a discrete Arc part.
//
// The bat embedding model is forced to f32 on EVERY device:
// its embedding head overflows at f16 on genuine f16 hardware (the A76 CPU's f16 NEON
// and the Intel iGPU), corrupting the raw embedding the bat classifier consumes and
// flipping detections. Unlike Perch v2 this is not GPU-only. Do NOT relax it to
// f16 without re-running the bat embedding parity check.
//
// BirdNET v3.0 is likewise forced to f32 on EVERY device. Its EfficientNetV2-S
// backbone is numerically unstable at f16 wherever genuine f16 kernels run (the Intel
// GPU always, and the A76 CPU's native f16), independent of the model file's weight
// precision. On fp16-weight regional tiles, f16 execution overflows to NaN (Sentry
// BIRDNET-GO-2H6: the 800-class north-america-east tile, "non-finite score (index 0
// of 800)", backend=OpenVINO precision=FP16); the in-graph sigmoid cannot rescue a
// NaN, so every window is dropped. On fp32-weight tiles f16 stays finite but silently
// inflates the scores: measured against an f32 reference on identical input, the max
// post-sigmoid confidence error was ~0.28 on the A76 CPU and ~0.29 on an Iris Xe iGPU
// (both far past the 0.15 divergence tolerance), with index 0 the worst (~25-30x). f32
// restores parity (~1e-4). Forced on the CPU too, not GPU-only, because the A76
// native-f16 path is affected as well. Do NOT relax it to f16 without re-running
// inference/openvino_parity_functional_test.go on a genuine f16 device (the Intel GPU
// and the A76 CPU) with an fp16-weight regional tile.
func openVINOPrecisionFor(modelID, device string) string {
	if modelID == DefaultModelVersion || modelID == RegistryIDBat || modelID == RegistryIDBirdNETV3 {
		return inference.OVPrecisionF32
	}
	if device == inference.OVDeviceGPU && modelID == RegistryIDPerchV2 {
		return inference.OVPrecisionF32
	}
	return ""
}

// openVINODeviceForBackend maps a hwprofile backend capability token to the
// concrete inference OpenVINO device it denotes, reporting ok=false for any
// token that is not an OpenVINO backend. Only OpenVINO carries an
// INFERENCE_PRECISION_HINT, so a non-OpenVINO backend can never override a
// model's declared file precision.
func openVINODeviceForBackend(backendToken string) (device string, ok bool) {
	switch backendToken {
	case hwprofile.CapOpenVINOCPU:
		return inference.OVDeviceCPU, true
	case hwprofile.CapOpenVINOGPU:
		return inference.OVDeviceGPU, true
	default:
		return "", false
	}
}

// BackendForcesFP32 reports whether running the given model on the given host
// backend forces FP32 execution, overriding whatever precision the model file
// declares. Only the OpenVINO backends carry an INFERENCE_PRECISION_HINT that
// can override the file precision (see openVINOPrecisionFor); every other
// backend (ONNX Runtime, and the CUDA/TensorRT compute backends) runs the file
// as stored, so this returns false for them.
//
// It is exported for the model-gallery recommender (internal/classifier/recommend),
// which uses it to avoid rewarding an fp16 variant's native-f16 speed on a
// backend that will not actually run that variant at f16. backendToken is a
// hwprofile capability token (hwprofile.CapOpenVINOCPU and friends). Keeping the
// policy here means the recommender never has to name a model ID, so any future
// change to the per-model precision policy propagates automatically. The rules
// that depend on the model file's weights rather than its registry ID
// (applyOpenVINOQuantizationPolicy) are not reflected here.
func BackendForcesFP32(registryID, backendToken string) bool {
	device, ok := openVINODeviceForBackend(backendToken)
	if !ok {
		return false
	}
	return openVINOPrecisionFor(registryID, device) == inference.OVPrecisionF32
}

// autoDeclinesOpenVINOCPU reports whether the auto backend declines the OpenVINO
// CPU device for a model. On arm64 it runs models on ONNX Runtime instead:
// measured on a Raspberry Pi 4 and a Pi 5, OpenVINO at f32 was slower than or
// tied with ONNX Runtime and held 114 to 166 MB more memory per model, and
// OpenVINO f16 is unsafe on the A76. The OpenVINO CPU is used on auto only on a
// CPU with native f16 when ONNX Runtime is missing. An explicit
// birdnet.backend=openvino or birdnet.openvinodevice=cpu bypasses this rule. It
// is always false off arm64.
func autoDeclinesOpenVINOCPU(goarch string, nativeF16, ortAvailable bool) bool {
	return goarch == goarchARM64 && (!nativeF16 || ortAvailable)
}

// cpuAllowed reports whether the OpenVINO CPU device may be used. The f16
// CPU kernels are safe only on ARMv8.2+ (nativeF16); on ARMv8.0 (A72) they
// would SIGILL, so the CPU path is rejected there. An explicit "cpu" preference
// on amd64 is allowed without the ARM f16 gate because the x86 CPU plugin is
// SIGILL-safe; auto never reaches this on amd64 (nativeF16 is false there).
func (h *ovHostFacts) cpuAllowed(devicePref string) bool {
	if h.nativeF16 {
		return true
	}
	return devicePref == conf.OVDeviceCPU && h.goarch == goarchAMD64
}

// openVINOGPUAvailable reports whether an OpenVINO GPU device (Intel iGPU/dGPU via
// the Intel GPU plugin) is present. It loads the OpenVINO core first (InitOpenVINO
// is idempotent and fast on repeat); a load failure means no usable OV at all.
func openVINOGPUAvailable(libraryPath string) bool {
	if err := inference.InitOpenVINO(libraryPath); err != nil {
		// A merely-absent OpenVINO library is the expected CPU-only case, not a
		// fault: device planning falls back to ONNX Runtime and logs that fallback
		// at INFO (logOpenVINODeclined). Log the absent case at Debug so a recurring
		// line does not read as a problem in a support dump, and reserve Warn for a
		// present-but-broken library (wrong version, missing symbol) whose load
		// failure a user would actually want surfaced. This still surfaces the real
		// cause of a broken explicit openvino/gpu opt-in, while staying quiet on a
		// host that simply never installed OpenVINO.
		if isLibraryAbsent(err) {
			GetLogger().Debug("OpenVINO library not installed; treating GPU as unavailable",
				logger.Error(err))
		} else {
			GetLogger().Warn("OpenVINO library load failed during device planning; treating GPU as unavailable",
				logger.Error(err))
		}
		return false
	}
	// Enumerate devices in a short-lived child process rather than in-process:
	// ov_core_get_available_devices walks the vendor driver stack, and a fault
	// there aborts the process before Go can recover (glibc heap-corruption
	// SIGABRT during cgo, issue #4236 - a systemd crash-loop on the first GPU
	// enablement). A crashing probe child degrades to the ordinary ORT
	// fallback instead. The probe result is cached per process, so this costs
	// one child run per library path.
	devices, err := inference.OpenVINOProbeDevices(libraryPath)
	if err != nil {
		GetLogger().Warn("OpenVINO device probe failed; treating GPU as unavailable",
			logger.Error(err))
		return false
	}
	return slices.Contains(devices, inference.OVDeviceGPU)
}

// isLibraryAbsent reports whether err indicates the OpenVINO shared library is
// simply not installed (the dynamic loader could not find the file), as opposed
// to present but broken. The loader reports a missing library only through its
// error text across the cgo boundary, so the message is the signal, matched
// case-insensitively for Linux dlopen ("cannot open shared object file"), macOS
// dyld ("image not found") and Windows LoadLibrary ("the specified module could
// not be found"), plus os.ErrNotExist for any path stat'd before the load.
//
// It is best-effort and deliberately conservative about staying quiet. A
// "permission denied" load failure is a present-but-broken case, not an absent
// one, so it is excluded and stays a Warn. Two residual imperfections are
// accepted because the only consequence is the Warn-vs-Debug log level (device
// planning falls back to ONNX Runtime either way, and logOpenVINODeclined still
// records the fallback at Info): a missing transitive dependency yields the same
// "cannot open shared object file" text as an absent primary and is treated as
// absent, and a non-English loader locale may not match the English text and so
// falls through to Warn.
func isLibraryAbsent(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	msg := strings.ToLower(err.Error())
	// A present-but-inaccessible library is broken, not absent; keep it a Warn.
	if strings.Contains(msg, "permission denied") {
		return false
	}
	return strings.Contains(msg, "cannot open shared object file") || // Linux dlopen
		strings.Contains(msg, "no such file or directory") ||
		strings.Contains(msg, "image not found") || // macOS dyld
		strings.Contains(msg, "the specified module could not be found") // Windows LoadLibrary
}

// openVINOPlan returns the OpenVINO plan for the primary BirdNET classifier, or
// false to use ORT. The primary classifier path applies sigmoid post-processing,
// so only the BirdNET v2.4 identity is valid here; Perch (softmax) runs its own
// OpenVINO path in the Perch ModelInstance (perch_onnx.go), never through this
// primary path. The plan carries the device (CPU/GPU), logits output index, and
// the precision hint (f32 on every device for BirdNET v2.4, see openVINOPrecisionFor;
// INT8 or unrecognized weights are declined on auto and run at f32 on an explicit
// openvino backend, see applyOpenVINOQuantizationPolicy). The weight precision is
// read from the model file name, exactly as the installed-variant load gate reads
// it, so the two cannot disagree.
func (bn *BirdNET) openVINOPlan() (plan openVINOPlan, ok bool, reason string) {
	if bn.ModelInfo.ID != DefaultModelVersion {
		return openVINOPlan{}, false, ovReasonNotBirdNETv24
	}
	return birdnetV24OpenVINOPlan(&bn.Settings.BirdNET, detectQuantization(bn.onnxModelPath()), defaultORTAvailable)
}

// birdnetV24OpenVINOPlan returns the OpenVINO plan for a BirdNET v2.4 model file
// whose weights have the given quantization. It is shared by model init
// ((*BirdNET).openVINOPlan) and the installed-variant load gate
// (Orchestrator.primaryVariantUsable) so both apply the same policy, and both
// pass detectQuantization of the model file path. A file whose name carries no
// precision token is treated as unverified (see applyOpenVINOQuantizationPolicy);
// only a file whose name misstates its precision can still get the wrong plan.
// ortAvailable answers whether ONNX Runtime is available at a configured library
// path; both callers pass the function their own ONNX Runtime check uses, so the
// plan and that check cannot disagree.
func birdnetV24OpenVINOPlan(cfg *conf.BirdNETConfig, quant Quantization, ortAvailable func(configuredPath string) bool) (plan openVINOPlan, ok bool, reason string) {
	plan, ok, reason = birdnetV24BasePlan(cfg, ortAvailable)
	return applyOpenVINOQuantizationPolicy(plan, ok, reason, cfg.Backend, quant)
}

// birdnetV24BasePlan is the device and precision plan birdnetV24OpenVINOPlan
// starts from, before the quantization policy is applied. BirdNET v2.4 is the one
// model with a live ONNX Runtime-missing case on arm64 auto (Perch, bat and v3.0
// require ONNX Runtime first), so it takes the ONNX Runtime answer from its
// caller. It is a variable only so tests can replace it: in the default (untagged)
// build openVINOPlanWithHost always declines with ovReasonNotBuilt, which would
// leave the policy unreachable through the production entry points.
var birdnetV24BasePlan = func(cfg *conf.BirdNETConfig, ortAvailable func(configuredPath string) bool) (plan openVINOPlan, ok bool, reason string) {
	return openVINOPlanWithHost(
		cfg.Backend,
		cfg.OpenVINODevice,
		DefaultModelVersion,
		birdnetLogitsOutputIndex,
		liveOVHostFacts(cfg.OpenVINOPath, ortAvailable(cfg.ONNXRuntimePath)),
	)
}

// defaultORTAvailable reports whether ONNX Runtime is available at the configured
// library path, with the probe the ONNX Runtime load gates use.
func defaultORTAvailable(configuredPath string) bool {
	return inference.CheckORTAvailability(configuredPath).Available
}

// applyOpenVINOQuantizationPolicy adjusts an OpenVINO plan for the weight
// precision of the model file, which openVINOPrecisionFor cannot see (it keys on
// the registry ID only). A declined plan passes through unchanged, and so does a
// plan for FP32 or FP16 weights. Every other precision (INT8, or unrecognized
// because the file name carries no precision token) is unverified.
//
// INT8 BirdNET v2.4 weights overflow under OpenVINO's default f16 execution on
// the A76 CPU: every inference returns +Inf logits and the wrong top-1, so every
// analysis window is dropped as non-finite (GitHub #4423). Measured on a
// Raspberry Pi 5 (OpenVINO 2026.2, ORT 1.25.1, one thread, real soundscape
// clips), both INT8 builds (the stock BirdNET_INT8_ARM.onnx and the gallery's
// int8-arm-dfttrunc) match ONNX Runtime at f32 (confidence error ~2e-6), but ORT
// is also faster than OpenVINO f32 (143 vs ~165 ms and 77 vs 87 ms per window).
// So on the auto backend an unverified model is declined here and runs on ORT,
// which is correct for any weights. Unrecognized precision is treated like INT8
// because it may be INT8 (a renamed file); a user-supplied FP32 model named
// without an fp32 token therefore runs on ORT on auto. An explicit
// backend=openvino is honored, forced to f32 so it stays correct even if the
// registry-level policy in openVINOPrecisionFor (already f32 for BirdNET v2.4)
// ever changes.
//
// The decline also covers the GPU, where the BirdNET v2.4 plan is already f32:
// INT8 weights on the OpenVINO GPU plugin have not been validated, while ORT is
// known to be correct, so auto stays on the verified backend. On a host with
// OpenVINO but no ONNX Runtime an unverified model therefore fails to load on auto;
// setting birdnet.backend to openvino runs it on OpenVINO at f32 instead. On arm64
// with the auto backend and device, an FP32 or FP16 file reaches OpenVINO only when
// ONNX Runtime is unavailable.
func applyOpenVINOQuantizationPolicy(plan openVINOPlan, ok bool, reason, backendPref string, quant Quantization) (outPlan openVINOPlan, outOK bool, outReason string) {
	if !ok || quant == QuantizationFP32 || quant == QuantizationFP16 {
		return plan, ok, reason
	}
	if backendPref != conf.BackendPrefOpenVINO {
		return openVINOPlan{}, false, ovReasonUnverifiedWeights
	}
	plan.precision = inference.OVPrecisionF32
	return plan, true, ""
}

// shouldTryOpenVINO reports whether the OpenVINO backend is eligible for the
// primary classifier: the boolean form of openVINOPlan, dropping the plan and the
// decline reason. True only when built with the openvino tag, the model is the
// BirdNET v2.4 identity, config does not opt out, a supported device is
// available (the Intel iGPU, or the ARM A76 CPU on an explicit opt-in or when ONNX
// Runtime is unavailable; both at f32, see openVINOPrecisionFor for why BirdNET
// v2.4 never uses f16), and the model file is not INT8 or of
// unrecognized precision on the auto backend (see
// applyOpenVINOQuantizationPolicy). initializeModel calls openVINOPlan directly
// so it can also log the decline reason; this predicate keeps the eligibility
// gate independently assertable in tests.
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
// default, which is f16 (see openVINOPrecisionFor), so it maps to FP16; the
// explicit override OVPrecisionF32 (Perch v2 on the GPU, and BirdNET v2.4, the bat
// embedding model and BirdNET v3.0 on every device) maps to FP32.
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
	// Publish the concrete OpenVINO device (CPU/GPU) the classifier bound to plus
	// the live backend and effective runtime precision, so the status card reports
	// "OpenVINO" + the real compute precision (FP32 for BirdNET v2.4 on every
	// device, see openVINOPrecisionFor) and the real device rather than the
	// static ONNX file metadata.
	bn.setRuntimeInfo(plan.device, BackendOpenVINO, openVINOEffectivePrecision(plan.precision))
	log.Info("OpenVINO model initialized",
		logger.String("model", modelPath),
		logger.String("device", plan.device),
		logger.String("precision", openVINOPrecisionLabel(plan.precision)),
		logger.Int("species", classifier.NumSpecies()),
		logger.String("init_time", time.Since(start).String()))
	return nil
}
