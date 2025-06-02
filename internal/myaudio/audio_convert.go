package myaudio

import (
	"encoding/binary"
	"fmt"
	"math"
	"sync"

	"github.com/tphakala/malgo"
)

// Buffer pool for reusing audio format conversion buffers
var s16BufferPool = sync.Pool{
	New: func() interface{} {
		// Create a buffer large enough for most audio frames
		buffer := make([]byte, 32768)
		return &buffer
	},
}

// ReturnBufferToPool returns a buffer to the pool if it was allocated from the pool
func ReturnBufferToPool(bufferPtr *[]byte, fromPool bool) {
	if fromPool && bufferPtr != nil {
		s16BufferPool.Put(bufferPtr)
	}
}

// ConvertToS16 converts audio in various formats to signed 16-bit PCM
// It returns a pointer to the output buffer, a boolean indicating if the buffer
// came from the pool, and any error that occurred
func ConvertToS16(samples []byte, sourceFormat malgo.FormatType, outputBuffer []byte) (outputBufferPtr *[]byte, fromPool bool, err error) {
	if len(samples) == 0 {
		return &samples, false, nil // Return empty buffer
	}

	// If source format is already S16, just return the original
	if sourceFormat == malgo.FormatS16 {
		return &samples, false, nil
	}

	// Calculate output buffer size
	outputSize := len(samples)

	// Format-specific adjustments
	switch sourceFormat {
	case malgo.FormatU8:
		// 8-bit to 16-bit = 2x size
		outputSize *= 2
	case malgo.FormatS24:
		// 24-bit to 16-bit = 2/3 size
		outputSize = (outputSize / 3) * 2
	case malgo.FormatS32:
		// 32-bit to 16-bit = half size
		outputSize /= 2
	case malgo.FormatF32:
		// 32-bit float to 16-bit = half size
		outputSize /= 2
	default:
		return nil, false, fmt.Errorf("unsupported format: %v", sourceFormat)
	}

	// Allocate or reuse a buffer for the output
	var output []byte
	var outputPtr *[]byte

	if outputBuffer != nil && len(outputBuffer) >= outputSize {
		// Use the provided buffer if large enough
		output = outputBuffer[:outputSize]
		outputPtr = &outputBuffer
		fromPool = false
	} else {
		// Get a buffer from the pool
		bufferPtr := s16BufferPool.Get().(*[]byte)
		buffer := *bufferPtr

		// Ensure it's large enough
		if len(buffer) < outputSize {
			// Pool-provided buffer isn't big enough, resize it
			buffer = make([]byte, outputSize)
			*bufferPtr = buffer
		}

		output = buffer[:outputSize]
		outputPtr = bufferPtr
		fromPool = true
	}

	// Convert based on source format
	switch sourceFormat {
	case malgo.FormatU8:
		convertU8ToS16(samples, output)
	case malgo.FormatS24:
		convertS24ToS16(samples, output)
	case malgo.FormatS32:
		convertS32ToS16(samples, output)
	case malgo.FormatF32:
		convertF32ToS16(samples, output)
	}

	return outputPtr, fromPool, nil
}

// convertU8ToS16 converts unsigned 8-bit samples to signed 16-bit
func convertU8ToS16(input, output []byte) {
	for i, sample := range input {
		// Convert 8-bit unsigned (0-255) to 16-bit signed (-32768 to 32767)
		// First center at 0 by subtracting 128, then scale up
		val := int16((int(sample) - 128) << 8)
		binary.LittleEndian.PutUint16(output[i*2:], uint16(val))
	}
}

// convertS24ToS16 converts signed 24-bit samples to signed 16-bit
func convertS24ToS16(input, output []byte) {
	inputLen := len(input) / 3 // 3 bytes per 24-bit sample
	for i := 0; i < inputLen; i++ {
		// Read 3 bytes as little-endian and convert to int32
		b0, b1, b2 := input[i*3], input[i*3+1], input[i*3+2]
		val := int32(uint32(b0) | (uint32(b1) << 8) | (uint32(b2) << 16))

		// Sign extend from 24-bit to 32-bit
		if (val & 0x800000) != 0 {
			// If the sign bit is set, extend with 1s
			val |= ^int32(0xFFFFFF) // Complement of 0xFFFFFF as int32
		}

		// Scale down from 24-bit to 16-bit with rounding
		val = (val + 0x80) >> 8

		// Clamp to int16 range
		if val > 32767 {
			val = 32767
		} else if val < -32768 {
			val = -32768
		}

		binary.LittleEndian.PutUint16(output[i*2:], uint16(val))
	}
}

// convertS32ToS16 converts signed 32-bit samples to signed 16-bit
func convertS32ToS16(input, output []byte) {
	inputLen := len(input) / 4 // 4 bytes per 32-bit sample
	for i := 0; i < inputLen; i++ {
		// Read 4 bytes as little-endian
		val := int32(binary.LittleEndian.Uint32(input[i*4:]))

		// Scale down from 32-bit to 16-bit with rounding
		val = (val + 0x8000) >> 16

		// Clamp to int16 range
		if val > 32767 {
			val = 32767
		} else if val < -32768 {
			val = -32768
		}

		binary.LittleEndian.PutUint16(output[i*2:], uint16(val))
	}
}

// convertF32ToS16 converts 32-bit float samples to signed 16-bit
func convertF32ToS16(input, output []byte) {
	inputLen := len(input) / 4 // 4 bytes per float32
	for i := 0; i < inputLen; i++ {
		// Read 4 bytes as float32
		bits := binary.LittleEndian.Uint32(input[i*4:])
		val := math.Float32frombits(bits)

		// Convert float32 [-1.0, 1.0] to int16 [-32768, 32767]
		scaled := int32(val * 32767.0)

		// Clamp to int16 range
		if scaled > 32767 {
			scaled = 32767
		} else if scaled < -32768 {
			scaled = -32768
		}

		binary.LittleEndian.PutUint16(output[i*2:], uint16(scaled))
	}
}
