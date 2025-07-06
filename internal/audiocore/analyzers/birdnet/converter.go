package birdnet

import (
	"encoding/binary"
	"math"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// FormatConverter handles audio format conversions for BirdNET
type FormatConverter struct {
	bufferPool audiocore.BufferPool
}

// NewFormatConverter creates a new format converter
func NewFormatConverter(bufferPool audiocore.BufferPool) *FormatConverter {
	if bufferPool == nil {
		panic("FormatConverter: bufferPool cannot be nil")
	}
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
	// Validate format to prevent division by zero
	if format.BitDepth <= 0 || format.Channels <= 0 {
		return 0
	}
	
	bytesPerSample := format.BitDepth / 8
	if bytesPerSample <= 0 {
		return 0
	}
	
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

// ResampleIfNeeded validates that input audio is at the required sample rate
// BirdNET-Go expects all audio sources to provide audio at the sample rate defined
// in conf.SampleRate and does not perform sample rate conversion. This ensures
// consistent processing and avoids quality loss from resampling.
func (c *FormatConverter) ResampleIfNeeded(input []float32, inputRate int, output []float32) error {
	if inputRate != conf.SampleRate {
		return errors.New(nil).
			Component(audiocore.ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("input_rate", inputRate).
			Context("expected_rate", conf.SampleRate).
			Context("error", "input audio must match configured sample rate - sample rate conversion is not supported").
			Build()
	}
	
	// Input is at correct sample rate, just copy
	copy(output, input)
	return nil
}

// ConvertToMono converts stereo to mono by averaging channels
func (c *FormatConverter) ConvertToMono(input []float32, channels int) ([]float32, error) {
	if channels <= 0 {
		return nil, errors.New(nil).
			Component(audiocore.ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("channels", channels).
			Context("error", "invalid channel count").
			Build()
	}

	if channels == 1 {
		return input, nil
	}

	// Validate input length is divisible by channels
	if len(input)%channels != 0 {
		return nil, errors.New(nil).
			Component(audiocore.ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("input_length", len(input)).
			Context("channels", channels).
			Context("error", "input length not divisible by channel count").
			Build()
	}

	monoSamples := len(input) / channels
	
	// Allocate output directly - no need for buffer pool here
	// since we're returning the slice and can't control when it's released
	output := make([]float32, monoSamples)

	for i := 0; i < monoSamples; i++ {
		sum := float32(0)
		for ch := 0; ch < channels; ch++ {
			sum += input[i*channels+ch]
		}
		output[i] = sum / float32(channels)
	}

	return output, nil
}