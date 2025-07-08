//go:build go1.18
// +build go1.18

package myaudio

import (
	"testing"
	
	"github.com/tphakala/birdnet-go/internal/conf"
)

// FuzzConvert16BitToFloat32 tests the convert16BitToFloat32 function with random input
func FuzzConvert16BitToFloat32(f *testing.F) {
	// Add seed corpus with various edge cases
	f.Add([]byte{})                          // Empty
	f.Add([]byte{0x00})                      // Single byte (invalid)
	f.Add([]byte{0x00, 0x00})                // Single zero sample
	f.Add([]byte{0xFF, 0x7F})                // Max positive
	f.Add([]byte{0x00, 0x80})                // Max negative
	f.Add([]byte{0x00, 0x00, 0xFF, 0x7F})    // Two samples
	f.Add([]byte{0x00, 0x40, 0x00, 0xC0})    // Positive and negative
	
	// Add larger samples
	largeSample := make([]byte, 1000)
	for i := range largeSample {
		largeSample[i] = byte(i % 256)
	}
	f.Add(largeSample)
	
	// Add standard buffer size
	standardSample := make([]byte, conf.BufferSize) // Standard buffer size
	f.Add(standardSample)
	
	f.Fuzz(func(t *testing.T, data []byte) {
		// Skip if data length is odd (invalid for 16-bit samples)
		if len(data)%2 != 0 {
			t.Skip("Odd length data is invalid for 16-bit samples")
		}
		
		// Run the conversion
		result := convert16BitToFloat32(data)
		
		// Verify the output length
		expectedLen := len(data) / 2
		if len(result) != expectedLen {
			t.Errorf("Expected %d samples, got %d", expectedLen, len(result))
		}
		
		// Verify all values are within valid float32 range [-1.0, 1.0)
		for i, val := range result {
			if val < -1.0 || val >= 1.0 {
				t.Errorf("Sample %d out of range: %f", i, val)
			}
			
			// Check for NaN or Inf
			if val != val { // NaN check
				t.Errorf("Sample %d is NaN", i)
			}
			if val > 1e30 || val < -1e30 { // Rough infinity check
				t.Errorf("Sample %d appears to be infinite: %f", i, val)
			}
		}
		
		// If using pool, return the buffer
		if len(result) == conf.BufferSize/2 { // Standard size
			ReturnFloat32Buffer(result)
		}
	})
}

// FuzzConvertToFloat32_AllBitDepths tests the ConvertToFloat32 function with various bit depths
func FuzzConvertToFloat32_AllBitDepths(f *testing.F) {
	// Add seed corpus for different bit depths
	bitDepths := []int{16, 24, 32}
	
	for _, depth := range bitDepths {
		bytesPerSample := depth / 8
		
		// Empty
		f.Add(depth, []byte{})
		
		// Single sample
		singleSample := make([]byte, bytesPerSample)
		f.Add(depth, singleSample)
		
		// Multiple samples
		multiSample := make([]byte, bytesPerSample*10)
		f.Add(depth, multiSample)
	}
	
	f.Fuzz(func(t *testing.T, bitDepth int, data []byte) {
		// Only test valid bit depths
		if bitDepth != 16 && bitDepth != 24 && bitDepth != 32 {
			t.Skip("Invalid bit depth")
		}
		
		bytesPerSample := bitDepth / 8
		
		// Skip if data length is not aligned
		if len(data)%bytesPerSample != 0 {
			t.Skip("Data length not aligned with bit depth")
		}
		
		// Run the conversion
		result, err := ConvertToFloat32(data, bitDepth)
		
		// Should not error for valid bit depths
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
			return
		}
		
		// Verify we got a result
		if result == nil {
			t.Error("Result is nil")
			return
		}
		
		// Verify it's a single channel
		if len(result) != 1 {
			t.Errorf("Expected 1 channel, got %d", len(result))
			return
		}
		
		// Verify the output length
		expectedLen := len(data) / bytesPerSample
		if len(result[0]) != expectedLen {
			t.Errorf("Expected %d samples, got %d", expectedLen, len(result[0]))
		}
		
		// Verify all values are within valid range
		for i, val := range result[0] {
			if val < -1.0 || val > 1.0 {
				t.Errorf("Sample %d out of range: %f", i, val)
			}
		}
	})
}

// FuzzFloat32PoolOperations tests the float32 pool with random operations
func FuzzFloat32PoolOperations(f *testing.F) {
	// Ensure pool is initialized
	if float32Pool == nil {
		if err := InitFloat32Pool(); err != nil {
			f.Fatalf("Failed to initialize pool: %v", err)
		}
	}
	
	// Add seed corpus - sequences of operations
	// Format: even indices are operations (0=get, 1=put), odd indices are data
	f.Add([]byte{0, 0}) // Single get
	f.Add([]byte{0, 0, 1, 0}) // Get then put
	f.Add([]byte{0, 0, 0, 0, 1, 0, 1, 0}) // Multiple gets and puts
	
	var buffers [][]float32
	
	f.Fuzz(func(t *testing.T, ops []byte) {
		if len(ops) < 2 {
			t.Skip("Need at least one operation")
		}
		
		// Process operations in pairs
		for i := 0; i < len(ops)-1; i += 2 {
			op := ops[i] % 2 // 0 = get, 1 = put
			
			switch op {
			case 0: // Get
				buf := float32Pool.Get()
				if buf == nil {
					t.Error("Got nil buffer from pool")
				} else if len(buf) != conf.BufferSize/2 {
					t.Errorf("Got wrong size buffer: %d, expected %d", len(buf), conf.BufferSize/2)
				} else {
					buffers = append(buffers, buf)
				}
				
			case 1: // Put
				if len(buffers) > 0 {
					// Return the last buffer
					buf := buffers[len(buffers)-1]
					buffers = buffers[:len(buffers)-1]
					float32Pool.Put(buf)
				}
			}
		}
		
		// Clean up - return all remaining buffers
		for _, buf := range buffers {
			float32Pool.Put(buf)
		}
		buffers = nil
	})
}