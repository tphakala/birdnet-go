package myaudio

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCaptureBuffer_ReadSegmentPrecision tests that ReadSegment calculates
// byte indices with sub-second precision.
func TestCaptureBuffer_ReadSegmentPrecision(t *testing.T) {
	// Do not use t.Parallel() - this test accesses the buffer's internal state

	t.Run("subsecond_offset_precision", func(t *testing.T) {
		// BUG #3: The current code does:
		//   startIndex := int(startOffset.Seconds()) * cb.sampleRate * cb.bytesPerSample
		// This truncates sub-second precision because int() is applied BEFORE multiplication.
		//
		// Example with 48000 Hz, 2 bytes/sample:
		//   startOffset = 1.5 seconds
		//   Current: int(1.5) * 48000 * 2 = 1 * 48000 * 2 = 96000 bytes (WRONG - loses 0.5s)
		//   Correct: int(1.5 * 48000 * 2) = int(144000) = 144000 bytes (RIGHT)
		//
		// The fix should do multiplication in float64 BEFORE converting to int.

		// Create a buffer: 5 seconds at 48kHz, 16-bit (2 bytes per sample)
		sampleRate := 48000
		bytesPerSample := 2
		durationSec := 5

		cb := NewCaptureBuffer(durationSec, sampleRate, bytesPerSample, "test-precision")
		require.NotNil(t, cb)

		// Fill the buffer with recognizable data
		// Each sample position has a unique value for easy verification
		fullData := make([]byte, cb.bufferSize)
		for i := range fullData {
			fullData[i] = byte(i % 256)
		}
		cb.Write(fullData)

		// Now the buffer is full and initialized
		// startTime was set during Write

		// Test: Request a segment starting at 1.5 seconds from buffer start
		// With 48kHz, 2 bytes/sample: 1.5 seconds = 1.5 * 48000 * 2 = 144000 bytes

		// Calculate the expected byte index for 1.5 seconds
		expectedStartIndex := int(1.5 * float64(sampleRate) * float64(bytesPerSample))
		// Expected: 144000

		// With the bug, the actual calculation would be:
		// The bug converts seconds to int BEFORE multiplication
		offsetSeconds := 1.5
		buggyStartIndex := int(offsetSeconds) * sampleRate * bytesPerSample
		// Buggy: int(1.5) = 1, so 1 * 48000 * 2 = 96000

		// These should be different - the bug causes loss of precision
		assert.NotEqual(t, expectedStartIndex, buggyStartIndex,
			"test setup error: expected and buggy indices should differ")

		// The difference represents the precision loss
		precisionLoss := expectedStartIndex - buggyStartIndex
		assert.Equal(t, 48000, precisionLoss, "precision loss should be 0.5 seconds worth of bytes")

		// Now test the actual ReadSegment function
		// We need to request data starting 1.5 seconds after the buffer's startTime
		requestStartTime := cb.startTime.Add(time.Duration(1500) * time.Millisecond)
		requestDuration := 1 // 1 second of audio

		// Read the segment
		segment, err := cb.ReadSegment(requestStartTime, requestDuration)
		require.NoError(t, err, "ReadSegment should succeed")
		require.NotNil(t, segment, "segment should not be nil")

		// Verify the segment starts at the correct position
		// The first byte of the segment should be fullData[expectedStartIndex]
		// If the bug exists, it will be fullData[buggyStartIndex] instead

		// Expected first byte (correct calculation)
		expectedFirstByte := byte(expectedStartIndex % 256)

		// Buggy first byte (if the bug exists)
		buggyFirstByte := byte(buggyStartIndex % 256)

		// The actual first byte from the segment
		actualFirstByte := segment[0]

		// This assertion will FAIL with the current bug
		assert.Equal(t, expectedFirstByte, actualFirstByte,
			"segment should start at the correct byte position (expected: %d at index %d, got: %d which would be at index ~%d)",
			expectedFirstByte, expectedStartIndex, actualFirstByte, buggyStartIndex)

		// Also verify they're different (the bug produces wrong result)
		if actualFirstByte == buggyFirstByte && actualFirstByte != expectedFirstByte {
			t.Errorf("BUG CONFIRMED: segment starts at buggy index %d instead of correct index %d",
				buggyStartIndex, expectedStartIndex)
		}
	})

	t.Run("integer_seconds_should_work_correctly", func(t *testing.T) {
		// Test that integer second offsets work correctly (no regression)
		sampleRate := 48000
		bytesPerSample := 2
		durationSec := 5

		cb := NewCaptureBuffer(durationSec, sampleRate, bytesPerSample, "test-int-seconds")
		require.NotNil(t, cb)

		// Fill buffer
		fullData := make([]byte, cb.bufferSize)
		for i := range fullData {
			fullData[i] = byte(i % 256)
		}
		cb.Write(fullData)

		// Request segment starting exactly 2 seconds in
		requestStartTime := cb.startTime.Add(2 * time.Second)
		segment, err := cb.ReadSegment(requestStartTime, 1)
		require.NoError(t, err)
		require.NotNil(t, segment)

		// 2 seconds = 2 * 48000 * 2 = 192000 bytes
		expectedIndex := 2 * sampleRate * bytesPerSample
		expectedFirstByte := byte(expectedIndex % 256)

		assert.Equal(t, expectedFirstByte, segment[0],
			"integer second offset should calculate correctly")
	})

	t.Run("millisecond_precision", func(t *testing.T) {
		// Test millisecond-level precision
		sampleRate := 48000
		bytesPerSample := 2
		durationSec := 5

		cb := NewCaptureBuffer(durationSec, sampleRate, bytesPerSample, "test-ms-precision")
		require.NotNil(t, cb)

		// Fill buffer
		fullData := make([]byte, cb.bufferSize)
		for i := range fullData {
			fullData[i] = byte(i % 256)
		}
		cb.Write(fullData)

		// Request segment starting at 100ms (0.1 seconds)
		// 0.1 seconds = 0.1 * 48000 * 2 = 9600 bytes
		requestStartTime := cb.startTime.Add(100 * time.Millisecond)
		segment, err := cb.ReadSegment(requestStartTime, 1)
		require.NoError(t, err)
		require.NotNil(t, segment)

		// With the bug: int(0.1) = 0, so index = 0 (WRONG)
		// Correct: int(0.1 * 48000 * 2) = 9600
		expectedIndex := int(0.1 * float64(sampleRate) * float64(bytesPerSample))
		expectedFirstByte := byte(expectedIndex % 256)

		// This will FAIL with the bug
		assert.Equal(t, expectedFirstByte, segment[0],
			"100ms offset should calculate to byte index %d", expectedIndex)
	})
}
