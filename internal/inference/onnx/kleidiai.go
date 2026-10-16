package onnx

import "fmt"

// KleidiAI workaround for the ONNX Runtime memory growth on Arm SME CPUs.
//
// Facts, checked against the ONNX Runtime sources at v1.25.1 unless noted:
//   - The session config key is defined in
//     include/onnxruntime/core/session/onnxruntime_session_options_config_keys.h
//     (kOrtSessionOptionsMlasDisableKleidiAi, "1" disables the KleidiAI kernels
//     even if available) and read in
//     onnxruntime/core/providers/cpu/mlas_backend_kernel_selector_config_utils.h.
//   - MLAS installs the KleidiAI overrides only when the CPU reports SME
//     (onnxruntime/core/mlas/lib/platform.cpp), so on x86, Raspberry Pi and
//     Apple M1 to M3 the entry changes nothing.
//   - The KleidiAI convolution LHS cache is keyed on input data and is never
//     evicted, so every inference with new input data grows memory. The leak is
//     fixed in ONNX Runtime 1.27.0 (microsoft/onnxruntime#28363), which removed
//     the input-data hash from the cache key.
//
// Measured on an Apple M5 Pro with Perch v2 int8_arm, 7 threads, fresh input per
// run, bundled ONNX Runtime 1.25.1: +1263 KB per run without the entry, flat
// with it, at about 16% longer inference time. ONNX Runtime 1.31.0 stays flat
// without the entry.
const (
	// ortConfigMlasDisableKleidiAI is the session config key that turns the
	// KleidiAI kernels off.
	ortConfigMlasDisableKleidiAI = "mlas.disable_kleidiai"
	// ortConfigValueEnabled is the "on" value for boolean session config keys.
	ortConfigValueEnabled = "1"

	// kleidiAILeakFixedMajor and kleidiAILeakFixedMinor are the first ONNX
	// Runtime release without the KleidiAI cache leak.
	kleidiAILeakFixedMajor = 1
	kleidiAILeakFixedMinor = 27

	// KleidiAIFixedVersion is the first ONNX Runtime release without the
	// KleidiAI cache leak, for log output. Keep it in step with the two
	// constants above.
	KleidiAIFixedVersion = "1.27"
)

// ConfigEntrySetter is the part of *ort.SessionOptions that
// ApplyKleidiAIWorkaround needs.
type ConfigEntrySetter interface {
	AddSessionConfigEntry(key, value string) error
}

// KleidiAIWorkaroundNeeded reports whether sessions on the ONNX Runtime release
// named by ortVersion need the KleidiAI kernels disabled: true for releases
// before 1.27 and for versions that cannot be parsed, false otherwise.
func KleidiAIWorkaroundNeeded(ortVersion string) bool {
	major, minor, ok := ParseORTVersion(ortVersion)
	if !ok {
		return true
	}
	return major < kleidiAILeakFixedMajor ||
		(major == kleidiAILeakFixedMajor && minor < kleidiAILeakFixedMinor)
}

// ApplyKleidiAIWorkaround disables the KleidiAI kernels on a session options
// object when KleidiAIWorkaroundNeeded reports true for ortVersion, and does
// nothing otherwise. Call it with ort.GetVersion() on every session that runs
// inference, before the session is created. It is a no-op on CPUs without SME.
func ApplyKleidiAIWorkaround(so ConfigEntrySetter, ortVersion string) error {
	if !KleidiAIWorkaroundNeeded(ortVersion) {
		return nil
	}
	if err := so.AddSessionConfigEntry(ortConfigMlasDisableKleidiAI, ortConfigValueEnabled); err != nil {
		return fmt.Errorf("onnx: failed to disable KleidiAI kernels: %w", err)
	}
	return nil
}
