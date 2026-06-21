//go:build !(linux && arm64)

package cpuspec

// HasNativeF16 reports whether the CPU supports native half-precision SIMD.
// Off linux/arm64 the OpenVINO f16 path is never used, so this is always false.
func HasNativeF16() bool { return false }
