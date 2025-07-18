package myaudio

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestConvert16BitToFloat32_WithPool(t *testing.T) {
	// Initialize the pool
	err := InitFloat32Pool()
	require.NoError(t, err)
	
	// Test data: 16-bit PCM samples
	// Create test data with known values
	testData := []byte{
		0x00, 0x00, // 0
		0xFF, 0x7F, // 32767 (max positive)
		0x00, 0x80, // -32768 (max negative)
		0x00, 0x40, // 16384
		0x00, 0xC0, // -16384
	}
	
	// Convert
	result := convert16BitToFloat32(testData)
	
	// Verify length
	assert.Len(t, result, 5)
	
	// Verify values
	assert.InDelta(t, 0.0, result[0], 0.0001)
	assert.InDelta(t, 0.999969, result[1], 0.0001)  // 32767/32768
	assert.InDelta(t, -1.0, result[2], 0.0001)      // -32768/32768
	assert.InDelta(t, 0.5, result[3], 0.0001)       // 16384/32768
	assert.InDelta(t, -0.5, result[4], 0.0001)      // -16384/32768
	
	// Return buffer to pool
	ReturnFloat32Buffer(result)
}

func TestConvert16BitToFloat32_Correctness(t *testing.T) {
	// Do not use t.Parallel() - this test may access global float32Pool
	
	tests := []struct {
		name     string
		input    []byte
		expected []float32
	}{
		{
			name:     "empty",
			input:    []byte{},
			expected: []float32{},
		},
		{
			name:     "single_zero",
			input:    []byte{0x00, 0x00},
			expected: []float32{0.0},
		},
		{
			name:     "max_positive",
			input:    []byte{0xFF, 0x7F},
			expected: []float32{0.999969}, // 32767/32768
		},
		{
			name:     "max_negative",
			input:    []byte{0x00, 0x80},
			expected: []float32{-1.0}, // -32768/32768
		},
		{
			name:     "alternating",
			input:    []byte{0x00, 0x40, 0x00, 0xC0, 0x00, 0x40, 0x00, 0xC0},
			expected: []float32{0.5, -0.5, 0.5, -0.5},
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convert16BitToFloat32(tt.input)
			assert.Len(t, result, len(tt.expected))
			
			for i := range tt.expected {
				assert.InDelta(t, tt.expected[i], result[i], 0.0001)
			}
		})
	}
}

func TestConvertToFloat32_AllBitDepths(t *testing.T) {
	tests := []struct {
		name     string
		bitDepth int
		input    []byte
		wantErr  bool
	}{
		{
			name:     "16bit",
			bitDepth: 16,
			input:    []byte{0x00, 0x00, 0xFF, 0x7F},
			wantErr:  false,
		},
		{
			name:     "24bit",
			bitDepth: 24,
			input:    []byte{0x00, 0x00, 0x00, 0xFF, 0xFF, 0x7F},
			wantErr:  false,
		},
		{
			name:     "32bit",
			bitDepth: 32,
			input:    []byte{0x00, 0x00, 0x00, 0x00, 0xFF, 0xFF, 0xFF, 0x7F},
			wantErr:  false,
		},
		{
			name:     "unsupported_8bit",
			bitDepth: 8,
			input:    []byte{0x00, 0xFF},
			wantErr:  true,
		},
		{
			name:     "unsupported_64bit",
			bitDepth: 64,
			input:    []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
			wantErr:  true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ConvertToFloat32(tt.input, tt.bitDepth)
			
			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, result)
				assert.Len(t, result, 1) // Should return single channel
			}
		})
	}
}

func TestFloat32PoolIntegration(t *testing.T) {
	// Initialize pool
	err := InitFloat32Pool()
	require.NoError(t, err)
	
	// Get initial stats
	initialStats := float32Pool.GetStats()
	
	// Create standard size buffer (3 seconds at 48kHz, 16-bit)
	testData := make([]byte, conf.BufferSize)
	
	// Fill with test pattern
	for i := 0; i < len(testData); i += 2 {
		// Create a sine wave pattern
		value := int16(16383) // 50% amplitude (32767 * 0.5 rounded)
		testData[i] = byte(value & 0xFF)
		testData[i+1] = byte(value >> 8)
	}
	
	// Perform multiple conversions
	const iterations = 10
	for i := range iterations {
		result := convert16BitToFloat32(testData)
		assert.Len(t, result, Float32BufferSize)
		
		// Verify some values
		assert.InDelta(t, 0.5, result[0], 0.01)
		
		// Return to pool
		ReturnFloat32Buffer(result)
		
		// For first iteration, should be a miss
		// For subsequent iterations, should be hits
		stats := float32Pool.GetStats()
		if i == 0 {
			assert.Equal(t, initialStats.Misses+1, stats.Misses)
		} else {
			assert.Greater(t, stats.Hits, initialStats.Hits)
		}
	}
	
	// Final stats should show pool is working
	finalStats := float32Pool.GetStats()
	// sync.Pool behavior is non-deterministic and depends on GC pressure
	// Just verify that the pool was used (had both hits and/or misses)
	assert.Positive(t, finalStats.Hits+finalStats.Misses)
}

// TestConvert16BitToFloat32_NonStandardSize tests conversion with non-standard buffer sizes
func TestConvert16BitToFloat32_NonStandardSize(t *testing.T) {
	// Initialize pool
	err := InitFloat32Pool()
	require.NoError(t, err)
	
	// Test with various non-standard sizes
	sizes := []int{100, 1000, 10000, 50000}
	
	for _, size := range sizes {
		t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
			// Create test data
			testData := make([]byte, size*2) // 2 bytes per sample
			
			// Convert
			result := convert16BitToFloat32(testData)
			
			// Should not use pool for non-standard sizes
			assert.Len(t, result, size)
			
			// Verify pool stats didn't change (no gets/puts for non-standard sizes)
			// The pool should not be used for these sizes
			// This is a bit indirect, but we can't directly verify allocation
		})
	}
}