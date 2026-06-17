//go:build linux && arm64

package cpuspec

import "golang.org/x/sys/cpu"

// HasNativeF16 reports whether the CPU supports native half-precision SIMD
// (ASIMDHP). True only on ARMv8.2+ cores such as the Cortex-A76 (rpi5). Used to
// gate the OpenVINO f16 backend so a non-A76 host falls back to ORT instead of
// executing f16 kernels it cannot decode (SIGILL). golang.org/x/sys/cpu reads
// the ASIMDHP HWCAP bit at package init; cpu.ARM64.HasASIMDHP is false on
// ARMv8.0 cores (A53/A72), which lack native f16.
func HasNativeF16() bool {
	return cpu.ARM64.HasASIMDHP
}
