package myaudio

import (
	"testing"

	"github.com/gen2brain/malgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConvertToS16_MisalignedInput tests that misaligned input data is handled correctly.
//
// BUG #5: When the input sample count is not aligned to bytesPerSample,
// the remainder bytes are silently dropped without any warning.
// This could indicate corrupted or incomplete audio data.
//
// The fix should log a warning when misaligned data is detected.
func TestConvertToS16_MisalignedInput(t *testing.T) {
	t.Run("aligned_input_no_warning", func(t *testing.T) {
		// 24-bit audio: 3 bytes per sample
		// 6 bytes = exactly 2 samples (aligned)
		samples := make([]byte, 6)
		for i := range samples {
			samples[i] = byte(i)
		}

		result, fromPool, err := ConvertToS16(samples, malgo.FormatS24, nil)
		require.NoError(t, err)
		require.NotNil(t, result)

		// Should convert 2 samples to 4 bytes (2 bytes per 16-bit sample)
		assert.Len(t, *result, 4, "aligned input should convert completely")

		// Clean up if from pool
		if fromPool {
			returnS16BufferToPool(result)
		}
	})

	t.Run("misaligned_input_should_warn", func(t *testing.T) {
		// 24-bit audio: 3 bytes per sample
		// 7 bytes = 2 complete samples + 1 byte remainder (misaligned)
		samples := make([]byte, 7)
		for i := range samples {
			samples[i] = byte(i)
		}

		// The current behavior silently drops the 1 byte remainder
		// After the fix, this should still work but log a warning
		result, fromPool, err := ConvertToS16(samples, malgo.FormatS24, nil)
		require.NoError(t, err, "misaligned input should not error (but should warn)")
		require.NotNil(t, result)

		// Should only convert 2 complete samples (6 bytes) to 4 bytes
		assert.Len(t, *result, 4, "should only convert complete samples")

		// The 7th byte is dropped - this is the bug
		// With the fix, a warning would be logged (we can't easily test log output)
		// But we can at least document the behavior

		// Clean up if from pool
		if fromPool {
			returnS16BufferToPool(result)
		}
	})

	t.Run("32bit_misaligned_input", func(t *testing.T) {
		// 32-bit audio: 4 bytes per sample
		// 9 bytes = 2 complete samples + 1 byte remainder (misaligned)
		samples := make([]byte, 9)
		for i := range samples {
			samples[i] = byte(i)
		}

		result, fromPool, err := ConvertToS16(samples, malgo.FormatS32, nil)
		require.NoError(t, err)
		require.NotNil(t, result)

		// Should only convert 2 complete samples (8 bytes) to 4 bytes
		assert.Len(t, *result, 4, "should only convert complete samples")

		if fromPool {
			returnS16BufferToPool(result)
		}
	})

	t.Run("less_than_one_sample", func(t *testing.T) {
		// 24-bit audio: 3 bytes per sample
		// 2 bytes = 0 complete samples (less than minimum)
		samples := make([]byte, 2)
		samples[0] = 0x12
		samples[1] = 0x34

		result, fromPool, err := ConvertToS16(samples, malgo.FormatS24, nil)
		require.NoError(t, err)
		require.NotNil(t, result)

		// Should return empty slice (no complete samples)
		assert.Empty(t, *result, "less than one sample should return empty")
		assert.False(t, fromPool, "empty result should not be from pool")
	})
}

// TestConvertToS16_AlignmentCalculation verifies the alignment math
func TestConvertToS16_AlignmentCalculation(t *testing.T) {
	testCases := []struct {
		name           string
		inputSize      int
		format         malgo.FormatType
		bytesPerSample int
		expectedOutput int
		droppedBytes   int
	}{
		{"24bit_aligned", 9, malgo.FormatS24, 3, 6, 0},    // 3 samples → 6 bytes
		{"24bit_plus1", 10, malgo.FormatS24, 3, 6, 1},     // 3 samples + 1 dropped
		{"24bit_plus2", 11, malgo.FormatS24, 3, 6, 2},     // 3 samples + 2 dropped
		{"32bit_aligned", 12, malgo.FormatS32, 4, 6, 0},   // 3 samples → 6 bytes
		{"32bit_plus1", 13, malgo.FormatS32, 4, 6, 1},     // 3 samples + 1 dropped
		{"32bit_plus2", 14, malgo.FormatS32, 4, 6, 2},     // 3 samples + 2 dropped
		{"32bit_plus3", 15, malgo.FormatS32, 4, 6, 3},     // 3 samples + 3 dropped
		{"f32_aligned", 8, malgo.FormatF32, 4, 4, 0},      // 2 samples → 4 bytes
		{"f32_plus1", 9, malgo.FormatF32, 4, 4, 1},        // 2 samples + 1 dropped
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			samples := make([]byte, tc.inputSize)
			for i := range samples {
				samples[i] = byte(i % 256)
			}

			result, fromPool, err := ConvertToS16(samples, tc.format, nil)
			require.NoError(t, err)
			require.NotNil(t, result)

			assert.Len(t, *result, tc.expectedOutput,
				"expected %d bytes output for %d bytes input with %d bytes/sample",
				tc.expectedOutput, tc.inputSize, tc.bytesPerSample)

			// Verify the calculation
			validSamples := tc.inputSize / tc.bytesPerSample
			actualDropped := tc.inputSize - (validSamples * tc.bytesPerSample)
			assert.Equal(t, tc.droppedBytes, actualDropped,
				"expected %d dropped bytes", tc.droppedBytes)

			if fromPool {
				returnS16BufferToPool(result)
			}
		})
	}
}

// returnS16BufferToPool is a helper to return buffers to the pool
func returnS16BufferToPool(buf *[]byte) {
	if buf != nil {
		s16BufferPool.Put(buf)
	}
}
