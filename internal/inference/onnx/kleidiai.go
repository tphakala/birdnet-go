package onnx

import (
	"fmt"

	ort "github.com/yalue/onnxruntime_go"
)

// KleidiAI workaround for the ONNX Runtime memory growth on Arm SME CPUs.
//
// Facts, checked against the ONNX Runtime sources at v1.25.1 unless noted:
//   - The session config key is defined in
//     include/onnxruntime/core/session/onnxruntime_session_options_config_keys.h
//     (kOrtSessionOptionsMlasDisableKleidiAi, "1" disables the KleidiAI kernels
//     even if available) and read in
//     onnxruntime/core/providers/cpu/mlas_backend_kernel_selector_config_utils.h.
//   - MLAS installs the KleidiAI SGEMM, dynamic-quantized GEMM and convolution
//     overrides only when the CPU reports SME
//     (onnxruntime/core/mlas/lib/platform.cpp). The key also turns off the
//     KleidiAI MatMulNBits kernels, which run on any Arm CPU with dotprod
//     (UseKleidiAI in onnxruntime/core/mlas/lib/qnbitgemm_kernel_neon.cpp).
//     KleidiAI is built for Arm only, so on x86 the entry changes nothing.
//   - The KleidiAI convolution LHS cache is keyed on input data and is never
//     evicted, so every inference with new input data grows memory. The leak is
//     fixed in ONNX Runtime 1.27.0 (microsoft/onnxruntime#28363), which removed
//     the input-data hash from the cache key.
const (
	// ortConfigMlasDisableKleidiAI is the session config key that turns the
	// KleidiAI kernels off.
	ortConfigMlasDisableKleidiAI = "mlas.disable_kleidiai"
	// ortConfigValueEnabled is the "on" value for boolean session config keys.
	ortConfigValueEnabled = "1"

	// KleidiAIFixedVersion is the first ONNX Runtime release without the
	// KleidiAI cache leak.
	KleidiAIFixedVersion = "1.27"
)

// configEntrySetter is the part of *ort.SessionOptions that the workaround
// needs, so the decision can be tested without the native library.
type configEntrySetter interface {
	AddSessionConfigEntry(key, value string) error
}

// KleidiAIWorkaroundNeeded reports whether sessions on the ONNX Runtime release
// named by ortVersion need the KleidiAI kernels disabled: true for releases
// before KleidiAIFixedVersion and for versions that cannot be parsed, false
// otherwise.
func KleidiAIWorkaroundNeeded(ortVersion string) bool {
	major, minor, ok := ParseORTVersion(ortVersion)
	if !ok {
		return true
	}
	fixedMajor, fixedMinor, _ := ParseORTVersion(KleidiAIFixedVersion)
	return major < fixedMajor || (major == fixedMajor && minor < fixedMinor)
}

// DisableKleidiAIIfNeeded disables the KleidiAI kernels on so when the loaded
// ONNX Runtime is older than KleidiAIFixedVersion. Call it on every session
// options object used for inference, before the session is created; the ONNX
// Runtime environment must already be initialized. On Arm CPUs without SME it
// affects only models with MatMulNBits operators; on x86 it changes
// nothing.
func DisableKleidiAIIfNeeded(so *ort.SessionOptions) error {
	return applyKleidiAIWorkaround(so, ort.GetVersion())
}

// applyKleidiAIWorkaround adds the disable entry to so when
// KleidiAIWorkaroundNeeded reports true for ortVersion.
func applyKleidiAIWorkaround(so configEntrySetter, ortVersion string) error {
	if !KleidiAIWorkaroundNeeded(ortVersion) {
		return nil
	}
	if err := so.AddSessionConfigEntry(ortConfigMlasDisableKleidiAI, ortConfigValueEnabled); err != nil {
		return fmt.Errorf("onnx: failed to disable KleidiAI kernels: %w", err)
	}
	return nil
}
