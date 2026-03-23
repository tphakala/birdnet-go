// Package convert provides PCM audio conversion utilities with SIMD acceleration.
//
// All float64 operations use github.com/tphakala/simd/f64 for SIMD-accelerated
// computation with automatic scalar fallback on unsupported platforms.
package convert

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/tphakala/birdnet-go/internal/errors"
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

	// formatAAC is the AAC audio format string.
	formatAAC = "aac"
)

// GetFileExtension returns the appropriate file extension for a given audio format string.
func GetFileExtension(format string) string {
	switch format {
	case formatAAC:
		return "m4a"
	default:
		return format
	}
}

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
// Odd-length input: If the input has an odd number of bytes, the trailing byte is
// silently ignored (PCM16 requires 2 bytes per sample). A debug log could be added
// at the call site if callers want to detect this condition.
//
// Note: Uses pcm16ScaleFactor (32768.0) as divisor to map the full int16 range [-32768, 32767]
// to [-1.0, ~0.99997]. This ensures -32768 maps exactly to -1.0.
// See Float64ToBytesPCM16 for the inverse operation.
func BytesToFloat64PCM16(samples []byte) []float64 {
	if len(samples) < 2 {
		return []float64{}
	}
	// Truncate to an even number of bytes so a trailing odd byte is
	// never passed to the inner loop. PCM16 requires 2 bytes per sample.
	evenLen := len(samples) &^ 1
	sampleCount := evenLen / 2
	floatSamples := make([]float64, sampleCount)
	BytesToFloat64PCM16Into(floatSamples, samples[:evenLen])
	return floatSamples
}

// BytesToFloat64PCM16Into converts 16-bit PCM byte data into a pre-allocated float64 slice.
// dst must have length >= len(samples)/2 (the function indexes dst[i] directly).
// This avoids allocation when used with pooled buffers.
func BytesToFloat64PCM16Into(dst []float64, samples []byte) {
	sampleCount := len(samples) / 2
	for i := range sampleCount {
		dst[i] = float64(int16(binary.LittleEndian.Uint16(samples[i*2:]))) / pcm16ScaleFactor //nolint:gosec // G115: intentional uint16→int16 bit reinterpretation for PCM audio
	}
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

	// Bounds check: ensure output buffer is large enough.
	requiredLen := len(floatSamples) * 2
	if len(output) < requiredLen {
		return fmt.Errorf("Float64ToBytesPCM16: output buffer too small (need %d bytes, got %d)", requiredLen, len(output))
	}

	// Clamp all values using SIMD (modifies floatSamples in-place).
	f64.Clamp(floatSamples, floatSamples, -1.0, 1.0)

	// Convert to bytes.
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

// ConvertToFloat32 converts a byte slice representing audio samples to a 2D slice of float32 samples.
// The function supports 16, 24, and 32 bit depths.
func ConvertToFloat32(sample []byte, bitDepth int) ([][]float32, error) {
	switch bitDepth {
	case 16:
		return [][]float32{convert16BitToFloat32(sample)}, nil
	case 24:
		return [][]float32{convert24BitToFloat32(sample)}, nil
	case 32:
		return [][]float32{convert32BitToFloat32(sample)}, nil
	default:
		return nil, errors.Newf("unsupported audio bit depth: %d", bitDepth).
			Component("audiocore/convert").
			Category(errors.CategoryValidation).
			Context("operation", "convert_to_float32").
			Context("bit_depth", bitDepth).
			Context("supported_bit_depths", "16,24,32").
			Build()
	}
}

// convert16BitToFloat32 converts 16-bit PCM samples (little-endian) to float32 values.
func convert16BitToFloat32(sample []byte) []float32 {
	length := len(sample) / 2
	float32Data := make([]float32, length)
	divisor := float32(32768.0)

	for i := range length {
		s := int16(sample[i*2]) | int16(sample[i*2+1])<<8
		float32Data[i] = float32(s) / divisor
	}

	return float32Data
}

// convert24BitToFloat32 converts 24-bit PCM samples (little-endian) to float32 values.
func convert24BitToFloat32(sample []byte) []float32 {
	length := len(sample) / 3
	float32Data := make([]float32, length)
	divisor := float32(8388608.0)

	for i := range length {
		s := int32(sample[i*3]) | int32(sample[i*3+1])<<8 | int32(sample[i*3+2])<<16
		if (s & 0x00800000) > 0 {
			s |= ^0x00FFFFFF // Two's complement sign extension
		}
		float32Data[i] = float32(s) / divisor
	}

	return float32Data
}

// convert32BitToFloat32 converts 32-bit PCM samples (little-endian) to float32 values.
func convert32BitToFloat32(sample []byte) []float32 {
	length := len(sample) / 4
	float32Data := make([]float32, length)
	divisor := float32(2147483648.0)

	for i := range length {
		s := int32(sample[i*4]) | int32(sample[i*4+1])<<8 | int32(sample[i*4+2])<<16 | int32(sample[i*4+3])<<24
		float32Data[i] = float32(s) / divisor
	}

	return float32Data
}
