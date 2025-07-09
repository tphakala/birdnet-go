package myaudio

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCaptureBufferSingleAllocation verifies that each source gets only one buffer allocation
func TestCaptureBufferSingleAllocation(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		source         string
		duration       int
		sampleRate     int
		bytesPerSample int
	}{
		{"standard_buffer", "test_source_1", 60, 48000, 2},
		{"high_sample_rate", "test_source_2", 30, 96000, 2},
		{"24bit_audio", "test_source_3", 60, 48000, 3},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// First allocation should succeed
			err := AllocateCaptureBuffer(tc.duration, tc.sampleRate, tc.bytesPerSample, tc.source)
			require.NoError(t, err, "First allocation should succeed")

			// Second allocation should fail
			err = AllocateCaptureBuffer(tc.duration, tc.sampleRate, tc.bytesPerSample, tc.source)
			assert.Error(t, err, "Second allocation should fail")
			assert.Contains(t, err.Error(), "already exists")

			// Using AllocateCaptureBufferIfNeeded should succeed without error
			err = AllocateCaptureBufferIfNeeded(tc.duration, tc.sampleRate, tc.bytesPerSample, tc.source)
			assert.NoError(t, err, "AllocateCaptureBufferIfNeeded should not error on existing buffer")

			// Clean up
			err = RemoveCaptureBuffer(tc.source)
			require.NoError(t, err, "Buffer removal should succeed")
		})
	}
}

// TestCaptureBufferReconnection simulates RTSP reconnection scenario
func TestCaptureBufferReconnection(t *testing.T) {
	t.Parallel()

	source := "rtsp://test.stream/camera1"
	duration := 60
	sampleRate := 48000
	bytesPerSample := 2

	// Initial allocation
	err := AllocateCaptureBufferIfNeeded(duration, sampleRate, bytesPerSample, source)
	require.NoError(t, err, "Initial allocation should succeed")

	// Simulate stream failure and removal
	err = RemoveCaptureBuffer(source)
	require.NoError(t, err, "Buffer removal should succeed")

	// Simulate reconnection - should allocate again
	err = AllocateCaptureBufferIfNeeded(duration, sampleRate, bytesPerSample, source)
	require.NoError(t, err, "Reallocation after removal should succeed")

	// Clean up
	err = RemoveCaptureBuffer(source)
	require.NoError(t, err)
}

// TestCaptureBufferCleanup verifies proper cleanup prevents orphaned buffers
func TestCaptureBufferCleanup(t *testing.T) {
	t.Parallel()

	sources := []string{"source1", "source2", "source3"}

	// Allocate buffers for all sources
	for _, source := range sources {
		err := AllocateCaptureBuffer(60, 48000, 2, source)
		require.NoError(t, err, "Allocation for %s should succeed", source)
	}

	// Verify all buffers exist
	for _, source := range sources {
		assert.True(t, HasCaptureBuffer(source), "Buffer for %s should exist", source)
	}

	// Remove specific buffers
	err := RemoveCaptureBuffer(sources[0])
	require.NoError(t, err)
	err = RemoveCaptureBuffer(sources[2])
	require.NoError(t, err)

	// Verify removal
	assert.False(t, HasCaptureBuffer(sources[0]), "Buffer for %s should not exist", sources[0])
	assert.True(t, HasCaptureBuffer(sources[1]), "Buffer for %s should still exist", sources[1])
	assert.False(t, HasCaptureBuffer(sources[2]), "Buffer for %s should not exist", sources[2])

	// Clean up remaining
	err = RemoveCaptureBuffer(sources[1])
	require.NoError(t, err)
}

// TestConcurrentBufferAllocation tests thread safety of buffer allocation
func TestConcurrentBufferAllocation(t *testing.T) {
	t.Parallel()

	const numGoroutines = 10
	source := "concurrent_test_source"

	var wg sync.WaitGroup
	successCount := 0
	var successMutex sync.Mutex

	// Try to allocate the same buffer from multiple goroutines
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			err := AllocateCaptureBuffer(60, 48000, 2, source)
			if err == nil {
				successMutex.Lock()
				successCount++
				successMutex.Unlock()
			}
		}()
	}

	wg.Wait()

	// Only one allocation should succeed
	assert.Equal(t, 1, successCount, "Only one allocation should succeed in concurrent scenario")

	// Clean up
	err := RemoveCaptureBuffer(source)
	require.NoError(t, err)
}

// TestBufferLifecycleWithErrors tests buffer lifecycle with various error conditions
func TestBufferLifecycleWithErrors(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		duration       int
		sampleRate     int
		bytesPerSample int
		source         string
		expectError    bool
		errorContains  string
	}{
		{"invalid_duration", 0, 48000, 2, "test1", true, "invalid capture buffer duration"},
		{"negative_duration", -10, 48000, 2, "test2", true, "invalid capture buffer duration"},
		{"invalid_sample_rate", 60, 0, 2, "test3", true, "invalid sample rate"},
		{"invalid_bytes_per_sample", 60, 48000, 0, "test4", true, "invalid bytes per sample"},
		{"empty_source", 60, 48000, 2, "", true, "empty source name"},
		{"valid_allocation", 60, 48000, 2, "valid_source", false, ""},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := AllocateCaptureBuffer(tc.duration, tc.sampleRate, tc.bytesPerSample, tc.source)

			if tc.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.errorContains)
			} else {
				assert.NoError(t, err)
				// Clean up successful allocation
				err = RemoveCaptureBuffer(tc.source)
				assert.NoError(t, err)
			}
		})
	}
}

// TestInitCaptureBuffers tests batch buffer initialization
func TestInitCaptureBuffers(t *testing.T) {
	t.Parallel()

	sources := []string{"rtsp://cam1", "rtsp://cam2", "rtsp://cam3", "rtsp://cam4"}

	// First initialization should succeed
	err := InitCaptureBuffers(60, 48000, 2, sources)
	assert.NoError(t, err, "Initial buffer initialization should succeed")

	// Verify all buffers were created
	for _, source := range sources {
		assert.True(t, HasCaptureBuffer(source), "Buffer for %s should exist", source)
	}

	// Second initialization should also succeed (using AllocateCaptureBufferIfNeeded internally)
	err = InitCaptureBuffers(60, 48000, 2, sources)
	assert.NoError(t, err, "Repeated initialization should not error")

	// Verify buffers still exist
	for _, source := range sources {
		assert.True(t, HasCaptureBuffer(source), "Buffer for %s should still exist", source)
	}

	// Clean up
	for _, source := range sources {
		err = RemoveCaptureBuffer(source)
		require.NoError(t, err)
	}
}

// TestMemoryLeakPrevention verifies that buffers are properly cleaned up
func TestMemoryLeakPrevention(t *testing.T) {
	t.Parallel()

	const iterations = 100
	source := "leak_test_source"

	for i := 0; i < iterations; i++ {
		// Allocate
		err := AllocateCaptureBuffer(60, 48000, 2, source)
		require.NoError(t, err, "Allocation %d should succeed", i)

		// Write some data
		testData := make([]byte, 1024)
		err = WriteToCaptureBuffer(source, testData)
		require.NoError(t, err, "Write %d should succeed", i)

		// Remove
		err = RemoveCaptureBuffer(source)
		require.NoError(t, err, "Removal %d should succeed", i)

		// Verify it's gone
		assert.False(t, HasCaptureBuffer(source), "Buffer should not exist after removal %d", i)
	}
}

// BenchmarkCaptureBufferAllocation benchmarks buffer allocation performance
func BenchmarkCaptureBufferAllocation(b *testing.B) {
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		source := fmt.Sprintf("bench_source_%d", i)
		err := AllocateCaptureBuffer(60, 48000, 2, source)
		if err != nil {
			b.Fatalf("Allocation failed: %v", err)
		}

		err = RemoveCaptureBuffer(source)
		if err != nil {
			b.Fatalf("Removal failed: %v", err)
		}
	}
}

// BenchmarkCaptureBufferIfNeeded benchmarks the IfNeeded allocation pattern
func BenchmarkCaptureBufferIfNeeded(b *testing.B) {
	source := "bench_source_ifneeded"

	// Pre-allocate the buffer
	err := AllocateCaptureBuffer(60, 48000, 2, source)
	if err != nil {
		b.Fatalf("Initial allocation failed: %v", err)
	}
	defer RemoveCaptureBuffer(source) //nolint:errcheck // Cleanup, error not critical in defer

	b.ResetTimer()

	for b.Loop() {
		err := AllocateCaptureBufferIfNeeded(60, 48000, 2, source)
		if err != nil {
			b.Fatalf("AllocateCaptureBufferIfNeeded failed: %v", err)
		}
	}
}
