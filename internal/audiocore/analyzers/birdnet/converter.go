package birdnet

import (
	"encoding/binary"
	"math"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// FormatConverter handles audio format conversions for BirdNET
type FormatConverter struct {
	bufferPool audiocore.BufferPool
}

// NewFormatConverter creates a new format converter
func NewFormatConverter(bufferPool audiocore.BufferPool) *FormatConverter {
	return &FormatConverter{
		bufferPool: bufferPool,
	}
}

// ConvertToFloat32 converts audio data to float32 format required by BirdNET
func (c *FormatConverter) ConvertToFloat32(input []byte, output []float32, format audiocore.AudioFormat) error {
	if len(input) == 0 {
		return nil
	}

	// Validate output buffer size
	samplesNeeded := c.calculateSampleCount(input, format)
	if len(output) < samplesNeeded {
		return errors.New(nil).
			Component(audiocore.ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("samples_needed", samplesNeeded).
			Context("output_size", len(output)).
			Context("error", "output buffer too small").
			Build()
	}

	// Convert based on input format
	switch format.Encoding {
	case "pcm_s16le":
		return c.convertS16ToFloat32(input, output)
	case "pcm_s24le":
		return c.convertS24ToFloat32(input, output)
	case "pcm_s32le":
		return c.convertS32ToFloat32(input, output)
	case "pcm_f32le":
		return c.convertF32ToFloat32(input, output)
	case "pcm_u8":
		return c.convertU8ToFloat32(input, output)
	default:
		return errors.New(nil).
			Component(audiocore.ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("encoding", format.Encoding).
			Context("error", "unsupported audio encoding").
			Build()
	}
}

// calculateSampleCount calculates the number of samples in the input
func (c *FormatConverter) calculateSampleCount(input []byte, format audiocore.AudioFormat) int {
	bytesPerSample := format.BitDepth / 8
	return len(input) / bytesPerSample / format.Channels
}

// convertS16ToFloat32 converts 16-bit signed PCM to float32
func (c *FormatConverter) convertS16ToFloat32(input []byte, output []float32) error {
	sampleCount := len(input) / 2
	if sampleCount > len(output) {
		sampleCount = len(output)
	}

	for i := 0; i < sampleCount; i++ {
		// Read 16-bit sample
		sample := int16(binary.LittleEndian.Uint16(input[i*2:]))
		// Convert to float32 in range [-1.0, 1.0]
		output[i] = float32(sample) / 32768.0
	}

	return nil
}

// convertS24ToFloat32 converts 24-bit signed PCM to float32
func (c *FormatConverter) convertS24ToFloat32(input []byte, output []float32) error {
	sampleCount := len(input) / 3
	if sampleCount > len(output) {
		sampleCount = len(output)
	}

	for i := 0; i < sampleCount; i++ {
		// Read 24-bit sample
		b0 := input[i*3]
		b1 := input[i*3+1]
		b2 := input[i*3+2]
		
		// Convert to 32-bit signed
		sample := int32(b0) | (int32(b1) << 8) | (int32(b2) << 16)
		
		// Sign extend if negative (24-bit to 32-bit)
		if b2&0x80 != 0 {
			sample |= ^int32(0xFFFFFF) // Set upper 8 bits to 1
		}
		
		// Convert to float32 in range [-1.0, 1.0]
		output[i] = float32(sample) / 8388608.0 // 2^23
	}

	return nil
}

// convertS32ToFloat32 converts 32-bit signed PCM to float32
func (c *FormatConverter) convertS32ToFloat32(input []byte, output []float32) error {
	sampleCount := len(input) / 4
	if sampleCount > len(output) {
		sampleCount = len(output)
	}

	for i := 0; i < sampleCount; i++ {
		// Read 32-bit sample
		sample := int32(binary.LittleEndian.Uint32(input[i*4:]))
		// Convert to float32 in range [-1.0, 1.0]
		output[i] = float32(sample) / 2147483648.0 // 2^31
	}

	return nil
}

// convertF32ToFloat32 copies float32 data (may need endian conversion)
func (c *FormatConverter) convertF32ToFloat32(input []byte, output []float32) error {
	sampleCount := len(input) / 4
	if sampleCount > len(output) {
		sampleCount = len(output)
	}

	for i := 0; i < sampleCount; i++ {
		// Read float32 sample
		bits := binary.LittleEndian.Uint32(input[i*4:])
		output[i] = math.Float32frombits(bits)
	}

	return nil
}

// convertU8ToFloat32 converts 8-bit unsigned PCM to float32
func (c *FormatConverter) convertU8ToFloat32(input []byte, output []float32) error {
	sampleCount := len(input)
	if sampleCount > len(output) {
		sampleCount = len(output)
	}

	for i := 0; i < sampleCount; i++ {
		// Convert from unsigned to signed
		sample := int(input[i]) - 128
		// Convert to float32 in range [-1.0, 1.0]
		output[i] = float32(sample) / 128.0
	}

	return nil
}

// ResampleIfNeeded resamples audio to 48kHz if needed
func (c *FormatConverter) ResampleIfNeeded(input []float32, inputRate int, output []float32) error {
	if inputRate == 48000 {
		// No resampling needed
		copy(output, input)
		return nil
	}

	// Simple linear interpolation resampling
	// Note: For production, consider using a proper resampling library
	ratio := float64(48000) / float64(inputRate)
	outputSamples := int(float64(len(input)) * ratio)
	
	if outputSamples > len(output) {
		return errors.New(nil).
			Component(audiocore.ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("output_samples_needed", outputSamples).
			Context("output_buffer_size", len(output)).
			Context("error", "output buffer too small for resampling").
			Build()
	}

	for i := 0; i < outputSamples; i++ {
		// Calculate source position
		srcPos := float64(i) / ratio
		srcIdx := int(srcPos)
		frac := srcPos - float64(srcIdx)

		if srcIdx+1 < len(input) {
			// Linear interpolation
			output[i] = input[srcIdx]*(1-float32(frac)) + input[srcIdx+1]*float32(frac)
		} else if srcIdx < len(input) {
			// Last sample
			output[i] = input[srcIdx]
		}
	}

	return nil
}

// ConvertToMono converts stereo to mono by averaging channels
func (c *FormatConverter) ConvertToMono(input []float32, channels int) []float32 {
	if channels == 1 {
		return input
	}

	monoSamples := len(input) / channels
	output := make([]float32, monoSamples)

	for i := 0; i < monoSamples; i++ {
		sum := float32(0)
		for ch := 0; ch < channels; ch++ {
			sum += input[i*channels+ch]
		}
		output[i] = sum / float32(channels)
	}

	return output
}