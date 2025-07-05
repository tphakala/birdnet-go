package malgo

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/tphakala/malgo"
)

// ConvertToS16 converts audio samples from various formats to 16-bit PCM
// Adapted from myaudio/capture.go but simplified for audiocore usage
func ConvertToS16(samples []byte, sourceFormat malgo.FormatType, outputBuffer []byte) ([]byte, error) {
	if len(samples) == 0 {
		return []byte{}, nil
	}

	var bytesPerSample int
	switch sourceFormat {
	case malgo.FormatS16:
		// Already S16, just return a copy
		if outputBuffer != nil && len(outputBuffer) >= len(samples) {
			copy(outputBuffer, samples)
			return outputBuffer[:len(samples)], nil
		}
		output := make([]byte, len(samples))
		copy(output, samples)
		return output, nil
	case malgo.FormatS24:
		bytesPerSample = 3
	case malgo.FormatS32, malgo.FormatF32:
		bytesPerSample = 4
	case malgo.FormatU8:
		bytesPerSample = 1
	default:
		return nil, fmt.Errorf("unsupported source format: %v", sourceFormat)
	}

	// Ensure we have complete samples
	validSampleCount := len(samples) / bytesPerSample
	if validSampleCount == 0 {
		return []byte{}, nil
	}

	// Calculate required output size
	requiredSize := validSampleCount * 2 // 2 bytes per sample for 16-bit

	// Allocate or use provided buffer
	var output []byte
	if outputBuffer != nil && len(outputBuffer) >= requiredSize {
		output = outputBuffer[:requiredSize]
	} else {
		output = make([]byte, requiredSize)
	}

	// Convert samples
	for i := 0; i < validSampleCount; i++ {
		srcIdx := i * bytesPerSample
		dstIdx := i * 2

		switch sourceFormat {
		case malgo.FormatU8:
			// Convert 8-bit unsigned to 16-bit signed
			val := uint8(samples[srcIdx])
			// Convert from 0-255 range to -32768 to 32767
			sample := int16((int32(val) - 128) * 256)
			binary.LittleEndian.PutUint16(output[dstIdx:dstIdx+2], uint16(sample))

		case malgo.FormatS24:
			// Convert 24-bit to 16-bit
			val := int32(samples[srcIdx]) | int32(samples[srcIdx+1])<<8 | int32(samples[srcIdx+2])<<16
			// Sign extend if the most significant bit is set
			if (val & 0x800000) != 0 {
				val |= int32(-0x1000000)
			}
			// Shift right by 8 bits to get a 16-bit value
			val >>= 8
			// Clamp to 16-bit range
			if val > 32767 {
				val = 32767
			} else if val < -32768 {
				val = -32768
			}
			binary.LittleEndian.PutUint16(output[dstIdx:dstIdx+2], uint16(val))

		case malgo.FormatS32:
			// Convert 32-bit integer to 16-bit
			val := int32(binary.LittleEndian.Uint32(samples[srcIdx : srcIdx+4]))
			val >>= 16
			// Clamp to 16-bit range
			if val > 32767 {
				val = 32767
			} else if val < -32768 {
				val = -32768
			}
			binary.LittleEndian.PutUint16(output[dstIdx:dstIdx+2], uint16(val))

		case malgo.FormatF32:
			// Convert 32-bit float to 16-bit integer
			bits := binary.LittleEndian.Uint32(samples[srcIdx : srcIdx+4])
			val := math.Float32frombits(bits)
			// Scale float [-1.0, 1.0] to 16-bit integer range
			val *= 32767.0
			// Clamp to 16-bit range
			if val > 32767.0 {
				val = 32767.0
			} else if val < -32768.0 {
				val = -32768.0
			}
			binary.LittleEndian.PutUint16(output[dstIdx:dstIdx+2], uint16(int16(val)))
		}
	}

	return output, nil
}

// GetFormatInfo returns information about a malgo format type
func GetFormatInfo(format malgo.FormatType) (bytesPerSample int, name string) {
	switch format {
	case malgo.FormatU8:
		return 1, "U8"
	case malgo.FormatS16:
		return 2, "S16"
	case malgo.FormatS24:
		return 3, "S24"
	case malgo.FormatS32:
		return 4, "S32"
	case malgo.FormatF32:
		return 4, "F32"
	default:
		return 0, "Unknown"
	}
}

// CalculateBufferSize calculates the buffer size in bytes for a given format and frame count
func CalculateBufferSize(format malgo.FormatType, channels uint8, frameCount uint32) int {
	bytesPerSample, _ := GetFormatInfo(format)
	return bytesPerSample * int(channels) * int(frameCount)
}

// ConvertSampleRate performs basic sample rate conversion using linear interpolation
// This is a simple implementation suitable for real-time processing
func ConvertSampleRate(input []byte, inputRate, outputRate uint32) ([]byte, error) {
	if inputRate == outputRate {
		// No conversion needed
		output := make([]byte, len(input))
		copy(output, input)
		return output, nil
	}

	// Calculate number of samples (assuming 16-bit mono)
	inputSamples := len(input) / 2
	if inputSamples == 0 {
		return []byte{}, nil
	}

	// Calculate output size
	ratio := float64(outputRate) / float64(inputRate)
	outputSamples := int(float64(inputSamples) * ratio)
	output := make([]byte, outputSamples*2)

	// Simple linear interpolation
	for i := 0; i < outputSamples; i++ {
		// Calculate position in input
		pos := float64(i) / ratio
		idx := int(pos)
		frac := pos - float64(idx)

		if idx >= inputSamples-1 {
			// Use last sample
			idx = inputSamples - 1
			frac = 0
		}

		// Get samples
		sample1 := int16(binary.LittleEndian.Uint16(input[idx*2 : idx*2+2]))
		var sample2 int16
		if idx < inputSamples-1 {
			sample2 = int16(binary.LittleEndian.Uint16(input[(idx+1)*2 : (idx+1)*2+2]))
		} else {
			sample2 = sample1
		}

		// Linear interpolation
		interpolated := float64(sample1)*(1-frac) + float64(sample2)*frac

		// Clamp and convert back
		if interpolated > 32767 {
			interpolated = 32767
		} else if interpolated < -32768 {
			interpolated = -32768
		}

		binary.LittleEndian.PutUint16(output[i*2:i*2+2], uint16(int16(interpolated)))
	}

	return output, nil
}

