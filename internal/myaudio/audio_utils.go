package myaudio

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/tphakala/simd/f64"
)

// PCM16 conversion constants.
// These define the scaling factors for converting between int16 PCM and normalized float64.
const (
	// pcm16MaxPositive is the maximum positive value for int16 (32767).
	// Used as multiplier when converting float64 to PCM16 to ensure symmetric output range.
	pcm16MaxPositive = 32767.0

	// pcm16ScaleFactor is used as divisor when converting PCM16 to float64.
	// Using 32768 ensures -32768 maps exactly to -1.0.
	pcm16ScaleFactor = 32768.0
)

// getFileExtension returns the appropriate file extension based on the format
func GetFileExtension(format string) string {
	switch format {
	case "aac":
		return "m4a"
	default:
		return format
	}
}

// =============================================================================
// Core audio utility functions
// These use SIMD-accelerated operations from github.com/tphakala/simd
// with automatic scalar fallback on unsupported platforms.
// =============================================================================

// SumOfSquaresFloat64 computes the sum of squared values using SIMD.
// This is the core computation for RMS calculation.
func SumOfSquaresFloat64(samples []float64) float64 {
	if len(samples) == 0 {
		return 0.0
	}
	return f64.DotProduct(samples, samples)
}

// CalculateRMSFloat64 calculates the Root Mean Square of audio samples.
// Uses SIMD-accelerated SumOfSquaresFloat64 internally.
func CalculateRMSFloat64(samples []float64) float64 {
	if len(samples) == 0 {
		return 0.0
	}
	return math.Sqrt(SumOfSquaresFloat64(samples) / float64(len(samples)))
}

// ClampFloat64 clamps a single float64 value to the range [-1.0, 1.0].
// Small enough to be inlined by the Go compiler.
func ClampFloat64(v float64) float64 {
	if v > 1.0 {
		return 1.0
	}
	if v < -1.0 {
		return -1.0
	}
	return v
}

// ClampFloat64Slice clamps all values in a slice to [-1.0, 1.0] in-place using SIMD.
func ClampFloat64Slice(samples []float64) {
	if len(samples) == 0 {
		return
	}
	f64.Clamp(samples, samples, -1.0, 1.0)
}

// BytesToFloat64PCM16 converts 16-bit PCM bytes (little-endian) to normalized float64 [-1.0, 1.0].
// Allocates a new slice for the result.
//
// Empty/short input handling: Returns an empty slice (not nil) if input has fewer than 2 bytes.
// This allows safe iteration over the result without nil checks.
//
// Note: Uses pcm16ScaleFactor (32768.0) as divisor to map the full int16 range [-32768, 32767]
// to [-1.0, ~0.99997]. This ensures -32768 maps exactly to -1.0.
// See Float64ToBytesPCM16 for the inverse operation.
func BytesToFloat64PCM16(samples []byte) []float64 {
	if len(samples) < 2 {
		return []float64{}
	}
	sampleCount := len(samples) / 2
	floatSamples := make([]float64, sampleCount)
	// Iterate by sample count to safely ignore any trailing odd byte
	for i := range sampleCount {
		floatSamples[i] = float64(int16(binary.LittleEndian.Uint16(samples[i*2:]))) / pcm16ScaleFactor //nolint:gosec // G115: intentional uint16→int16 bit reinterpretation for PCM audio
	}
	return floatSamples
}

// Float64ToBytesPCM16 converts normalized float64 [-1.0, 1.0] to 16-bit PCM bytes with clamping.
// Writes directly into the output slice (must be pre-allocated with len >= len(floatSamples)*2).
// Uses SIMD for clamping.
//
// Empty input handling: Returns nil (success) for empty input slice. This is a no-op that
// allows callers to skip explicit empty checks.
//
// Note: Uses pcm16MaxPositive (32767.0) as multiplier to map [-1.0, 1.0] to [-32767, 32767].
// This differs from BytesToFloat64PCM16 which uses pcm16ScaleFactor (32768.0) as divisor,
// creating a slight asymmetry: -1.0 converts to -32767 (not -32768). This is intentional
// to avoid overflow when converting 1.0 and maintains symmetric output range.
// Round-trip conversion may lose up to 1 LSB of precision.
//
// WARNING: This function modifies floatSamples in-place during clamping for performance.
// If you need to preserve the original values, make a copy before calling this function.
//
// Returns an error if the output slice is too small to hold the converted samples.
func Float64ToBytesPCM16(floatSamples []float64, output []byte) error {
	if len(floatSamples) == 0 {
		return nil
	}

	// Bounds check: ensure output buffer is large enough
	requiredLen := len(floatSamples) * 2
	if len(output) < requiredLen {
		return fmt.Errorf("Float64ToBytesPCM16: output buffer too small (need %d bytes, got %d)", requiredLen, len(output))
	}

	// Clamp all values using SIMD (modifies floatSamples in-place)
	f64.Clamp(floatSamples, floatSamples, -1.0, 1.0)

	// Convert to bytes
	for i, sample := range floatSamples {
		intSample := int16(sample * pcm16MaxPositive)
		binary.LittleEndian.PutUint16(output[i*2:], uint16(intSample)) //nolint:gosec // G115: intentional int16→uint16 bit reinterpretation for PCM audio
	}
	return nil
}

// MinFloat64 returns the minimum value in a slice using SIMD.
func MinFloat64(samples []float64) float64 {
	if len(samples) == 0 {
		return 0.0
	}
	return f64.Min(samples)
}

// MaxFloat64 returns the maximum value in a slice using SIMD.
func MaxFloat64(samples []float64) float64 {
	if len(samples) == 0 {
		return 0.0
	}
	return f64.Max(samples)
}

// SumFloat64 returns the sum of all values in a slice using SIMD.
func SumFloat64(samples []float64) float64 {
	if len(samples) == 0 {
		return 0.0
	}
	return f64.Sum(samples)
}

// MeanFloat64 returns the arithmetic mean of values in a slice using SIMD.
func MeanFloat64(samples []float64) float64 {
	if len(samples) == 0 {
		return 0.0
	}
	return f64.Mean(samples)
}

// MinMaxSumFloat64 computes min, max, and sum using SIMD operations.
// For very small slices (< 8 elements), SIMD overhead may not be beneficial,
// but the library handles this internally with scalar fallback.
func MinMaxSumFloat64(samples []float64) (minVal, maxVal, sum float64) {
	if len(samples) == 0 {
		return 0, 0, 0
	}
	return f64.Min(samples), f64.Max(samples), f64.Sum(samples)
}

// ScaleFloat64Slice multiplies all elements by a scalar value in-place using SIMD.
func ScaleFloat64Slice(samples []float64, scalar float64) {
	if len(samples) == 0 {
		return
	}
	f64.Scale(samples, samples, scalar)
}
