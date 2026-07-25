package hwprofile

import (
	"slices"
	"strconv"
)

// Capability tokens. The vocabulary is deliberately identical to the selection
// keys and backend names used in the published model manifests, so a host
// profile joins to a manifest entry by string equality and nothing has to
// translate between two spellings of the same fact.
//
// The manifest vocabulary also contains "cuda" and "tensorrt". No BirdNET-Go
// build ships either runtime, so nothing emits those tokens yet.
const (
	// CapX86_64 marks a 64-bit x86 host.
	CapX86_64 = "x86-64"
	// CapAArch64 marks a 64-bit ARM host.
	CapAArch64 = "aarch64"
	// CapAArch64A76 marks a 64-bit ARM host with native half-precision SIMD,
	// which in practice means a Cortex-A76 or newer (Raspberry Pi 5).
	CapAArch64A76 = "aarch64-a76"

	// CapTFLite marks the TensorFlow Lite backend as usable.
	CapTFLite = "tflite"
	// CapONNXRuntimeCPU marks the ONNX Runtime CPU backend as usable.
	CapONNXRuntimeCPU = "onnxruntime-cpu"
	// CapOpenVINOCPU marks the OpenVINO CPU backend as usable.
	CapOpenVINOCPU = "openvino-cpu"
	// CapOpenVINOGPU marks the OpenVINO GPU backend as usable.
	CapOpenVINOGPU = "openvino-gpu"

	// CapLowRAM marks a host below the low-memory threshold, where the largest
	// model variants do not fit.
	CapLowRAM = "low-ram"
	// CapFP16Native marks a host whose CPU executes half-precision SIMD natively.
	CapFP16Native = "fp16-native"
	// CapOpenVINOGPUIntelGenPrefix is the prefix of the per-generation Intel GPU
	// token, completed with the generation number (e.g. "openvino-gpu-intel-gen12").
	// It exists so generation-specific driver defects can be expressed as
	// manifest data rather than as code.
	CapOpenVINOGPUIntelGenPrefix = "openvino-gpu-intel-gen"
)

// lowRAMThresholdBytes is the ceiling below which a host is tagged low-ram.
// 2 GiB is where the larger classifier variants stop fitting alongside the
// audio pipeline on the boards this runs on.
const lowRAMThresholdBytes int64 = 2 * 1024 * 1024 * 1024

// deviceGPU is the OpenVINO device name for a GPU, as returned by the runtime's
// device enumeration.
const deviceGPU = "GPU"

// Capabilities returns the capability tokens for this profile. It is pure: it
// derives everything from the already-probed Profile and performs no I/O, so
// the same profile always yields the same tokens.
//
// Order is stable (architecture, backends, modifiers) and duplicates are
// removed, so the result can be compared or logged directly.
func (p Profile) Capabilities() []string {
	caps := make([]string, 0, 8)

	switch p.Arch {
	case archAMD64, arch386:
		caps = append(caps, CapX86_64)
	case archARM64:
		caps = append(caps, CapAArch64)
		if p.HasNativeF16 {
			caps = append(caps, CapAArch64A76)
		}
	case archARM:
		// 32-bit ARM has no single token: the manifests distinguish armv7l from
		// the older variants, which is exactly what CPUArch already resolved from
		// /proc/cpuinfo.
		if p.CPUArch != "" && p.CPUArch != archARM {
			caps = append(caps, p.CPUArch)
		}
	}

	if p.Backends.TFLite.Available {
		caps = append(caps, CapTFLite)
	}
	if p.Backends.ONNX.Available {
		caps = append(caps, CapONNXRuntimeCPU)
	}
	if p.Backends.OpenVINO.Supported {
		caps = append(caps, CapOpenVINOCPU)
		if slices.Contains(p.Backends.OpenVINO.Devices, deviceGPU) {
			caps = append(caps, CapOpenVINOGPU)
		}
	}

	if p.TotalRAMBytes > 0 && p.TotalRAMBytes < lowRAMThresholdBytes {
		caps = append(caps, CapLowRAM)
	}
	if p.HasNativeF16 {
		caps = append(caps, CapFP16Native)
	}

	// Per-generation Intel GPU tokens are emitted for every Intel GPU present,
	// not only usable ones: a manifest entry can then exclude a generation whose
	// driver miscompiles a variant regardless of how the GPU is reached.
	for i := range p.Accelerators {
		a := p.Accelerators[i]
		if a.Vendor == VendorIntel && a.Generation > 0 {
			caps = append(caps, CapOpenVINOGPUIntelGenPrefix+strconv.Itoa(a.Generation))
		}
	}

	return dedupe(caps)
}

// dedupe removes repeated tokens while preserving first-seen order. Two GPUs of
// the same generation would otherwise emit the same token twice.
func dedupe(tokens []string) []string {
	seen := make(map[string]struct{}, len(tokens))
	out := tokens[:0]
	for _, t := range tokens {
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}
