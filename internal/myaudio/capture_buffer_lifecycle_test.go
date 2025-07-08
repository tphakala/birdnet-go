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
	// Note: Cannot run in parallel due to global allocation tracking
	
	// Enable allocation tracking for this test
	EnableAllocationTracking(true)
	defer EnableAllocationTracking(false)
	ResetAllocationTracking()

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

			// Verify allocation count
			count := GetAllocationCount(tc.source)
			assert.Equal(t, 2, count, "Should have tracked 2 allocation attempts (1 successful, 1 failed)")

			// Clean up
			err = RemoveCaptureBuffer(tc.source)
			require.NoError(t, err, "Buffer removal should succeed")
		})
	}
}

// TestCaptureBufferReconnection simulates RTSP reconnection scenario
func TestCaptureBufferReconnection(t *testing.T) {
	// Note: Cannot run in parallel due to global allocation tracking
	
	EnableAllocationTracking(true)
	defer EnableAllocationTracking(false)
	ResetAllocationTracking()

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

	// Verify this is considered as 2 separate allocations, not repeated
	count := GetAllocationCount(source)
	assert.Equal(t, 2, count, "Should have 2 allocations tracked")

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

// TestAllocationTracking verifies the allocation tracking system works correctly
func TestAllocationTracking(t *testing.T) {
	t.Parallel()

	// Test with tracking disabled
	EnableAllocationTracking(false)
	ResetAllocationTracking()

	source := "test_no_tracking"
	allocID := TrackAllocation(source, 1024)
	assert.Empty(t, allocID, "Allocation ID should be empty when tracking is disabled")
	assert.Equal(t, 0, GetAllocationCount(source), "No allocations should be tracked when disabled")

	// Test with tracking enabled
	EnableAllocationTracking(true)
	defer EnableAllocationTracking(false)

	source = "test_with_tracking"
	allocID = TrackAllocation(source, 1024)
	assert.NotEmpty(t, allocID, "Allocation ID should be generated when tracking is enabled")
	assert.Equal(t, 1, GetAllocationCount(source), "Allocation should be tracked")

	// Track another allocation
	allocID2 := TrackAllocation(source, 2048)
	assert.NotEmpty(t, allocID2, "Second allocation ID should be generated")
	assert.NotEqual(t, allocID, allocID2, "Allocation IDs should be unique")
	assert.Equal(t, 2, GetAllocationCount(source), "Second allocation should be tracked")

	// Verify report generation
	report := GetAllocationReport()
	assert.Contains(t, report, source)
	assert.Contains(t, report, "REPEATED ALLOCATIONS DETECTED")
}

// TestConcurrentBufferAllocation tests thread safety of buffer allocation
func TestConcurrentBufferAllocation(t *testing.T) {
	t.Parallel()

	EnableAllocationTracking(true)
	defer EnableAllocationTracking(false)
	ResetAllocationTracking()

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
	
	// All attempts should be tracked
	allocCount := GetAllocationCount(source)
	assert.GreaterOrEqual(t, allocCount, numGoroutines, "All allocation attempts should be tracked")

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

	EnableAllocationTracking(true)
	defer EnableAllocationTracking(false)
	ResetAllocationTracking()

	sources := []string{"rtsp://cam1", "rtsp://cam2", "rtsp://cam3", "rtsp://cam4"}
	
	// First initialization should succeed
	err := InitCaptureBuffers(60, 48000, 2, sources)
	assert.NoError(t, err, "Initial buffer initialization should succeed")

	// Verify all buffers were created
	for _, source := range sources {
		assert.True(t, HasCaptureBuffer(source), "Buffer for %s should exist", source)
		assert.Equal(t, 1, GetAllocationCount(source), "Should have 1 allocation for %s", source)
	}

	// Second initialization should also succeed (using AllocateCaptureBufferIfNeeded internally)
	err = InitCaptureBuffers(60, 48000, 2, sources)
	assert.NoError(t, err, "Repeated initialization should not error")

	// Verify no successful repeated allocations
	for _, source := range sources {
		// Will have 2 tracked attempts, but only 1 successful
		assert.True(t, HasCaptureBuffer(source), "Buffer for %s should still exist", source)
	}

	// Clean up
	for _, source := range sources {
		err = RemoveCaptureBuffer(source)
		require.NoError(t, err)
	}
}

// TestAllocationReportGeneration tests the allocation report functionality
func TestAllocationReportGeneration(t *testing.T) {
	t.Parallel()

	EnableAllocationTracking(true)
	defer EnableAllocationTracking(false)
	ResetAllocationTracking()

	// Create some allocations
	sources := []string{"source_a", "source_b", "source_c"}
	
	// source_a: single allocation
	err := AllocateCaptureBuffer(60, 48000, 2, sources[0])
	require.NoError(t, err)

	// source_b: repeated allocation attempts
	err = AllocateCaptureBuffer(60, 48000, 2, sources[1])
	require.NoError(t, err)
	err = AllocateCaptureBuffer(60, 48000, 2, sources[1]) // This should fail
	assert.Error(t, err)

	// source_c: no allocation
	
	// Generate report
	report := GetAllocationReport()
	
	// Verify report contents
	assert.Contains(t, report, "Capture Buffer Allocation Report")
	assert.Contains(t, report, sources[0])
	assert.Contains(t, report, sources[1])
	assert.NotContains(t, report, sources[2]) // No allocation for source_c
	assert.Contains(t, report, "REPEATED ALLOCATIONS DETECTED") // For source_b

	// Print summary
	PrintAllocationSummary()

	// Clean up
	for _, source := range sources[:2] {
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