package metrics

import (
	"strconv"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func TestRecordAudioConversion(t *testing.T) {
	// Create a new registry for testing
	registry := prometheus.NewRegistry()
	m, err := NewMyAudioMetrics(registry)
	assert.NoError(t, err)

	// Test various bit depths
	testCases := []struct {
		name           string
		conversionType string
		bitDepth       int
		status         string
	}{
		{"8-bit conversion", "wav", 8, "success"},
		{"16-bit conversion", "wav", 16, "success"},
		{"24-bit conversion", "wav", 24, "success"},
		{"32-bit conversion", "wav", 32, "success"},
		{"negative bit depth", "wav", -1, "error"},
		{"zero bit depth", "wav", 0, "error"},
		{"large bit depth", "wav", 192, "success"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Record the conversion
			m.RecordAudioConversion(tc.conversionType, tc.bitDepth, tc.status)

			// Verify the metric was recorded
			count := testutil.ToFloat64(m.audioConversionsTotal.WithLabelValues(
				tc.conversionType,
				strconv.Itoa(tc.bitDepth), // Updated implementation
				tc.status,
			))
			assert.Equal(t, float64(1), count)
		})
	}
}

func TestRecordAudioConversionError(t *testing.T) {
	// Create a new registry for testing
	registry := prometheus.NewRegistry()
	m, err := NewMyAudioMetrics(registry)
	assert.NoError(t, err)

	// Test various error scenarios
	testCases := []struct {
		name           string
		conversionType string
		bitDepth       int
		errorType      string
	}{
		{"8-bit error", "wav", 8, "invalid_format"},
		{"16-bit error", "wav", 16, "io_error"},
		{"24-bit error", "wav", 24, "memory_error"},
		{"32-bit error", "wav", 32, "timeout"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Record the error
			m.RecordAudioConversionError(tc.conversionType, tc.bitDepth, tc.errorType)

			// Verify the metric was recorded
			count := testutil.ToFloat64(m.audioConversionErrors.WithLabelValues(
				tc.conversionType,
				strconv.Itoa(tc.bitDepth), // Updated implementation
				tc.errorType,
			))
			assert.Equal(t, float64(1), count)
		})
	}
}

// Benchmark to measure performance improvement
func BenchmarkRecordAudioConversion_FmtSprintf(b *testing.B) {
	registry := prometheus.NewRegistry()
	m, err := NewMyAudioMetrics(registry)
	assert.NoError(b, err)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.RecordAudioConversion("wav", 16, "success")
	}
}

func BenchmarkRecordAudioConversionError_FmtSprintf(b *testing.B) {
	registry := prometheus.NewRegistry()
	m, err := NewMyAudioMetrics(registry)
	assert.NoError(b, err)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.RecordAudioConversionError("wav", 16, "error")
	}
}

func TestRecordBufferAllocationAttempt(t *testing.T) {
	// Create a new registry for testing
	registry := prometheus.NewRegistry()
	m, err := NewMyAudioMetrics(registry)
	assert.NoError(t, err)

	// Test allocation attempt tracking scenarios
	testCases := []struct {
		name       string
		bufferType string
		source     string
		result     string
		expected   float64
	}{
		{"first allocation", "capture", "rtsp://camera1", "first_allocation", 1},
		{"repeated blocked", "capture", "rtsp://camera1", "repeated_blocked", 1},
		{"error case", "capture", "rtsp://camera2", "error", 1},
		{"attempted tracking", "capture", "rtsp://camera3", "attempted", 1},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Record the allocation attempt
			m.RecordBufferAllocationAttempt(tc.bufferType, tc.source, tc.result)

			// Verify the metric was recorded
			count := testutil.ToFloat64(m.bufferAllocationAttempts.WithLabelValues(
				tc.bufferType,
				tc.source,
				tc.result,
			))
			assert.Equal(t, tc.expected, count)
		})
	}
	
	// Test repeated allocation scenario
	t.Run("repeated allocation scenario", func(t *testing.T) {
		source := "rtsp://test_camera"
		
		// Simulate multiple allocation attempts
		m.RecordBufferAllocationAttempt("capture", source, "attempted")
		m.RecordBufferAllocationAttempt("capture", source, "first_allocation")
		
		// Simulate repeated attempts
		for i := 0; i < 3; i++ {
			m.RecordBufferAllocationAttempt("capture", source, "attempted")
			m.RecordBufferAllocationAttempt("capture", source, "repeated_blocked")
		}
		
		// Verify counts
		attemptedCount := testutil.ToFloat64(m.bufferAllocationAttempts.WithLabelValues(
			"capture", source, "attempted",
		))
		assert.Equal(t, float64(4), attemptedCount, "Should have 4 attempted allocations")
		
		firstAllocCount := testutil.ToFloat64(m.bufferAllocationAttempts.WithLabelValues(
			"capture", source, "first_allocation",
		))
		assert.Equal(t, float64(1), firstAllocCount, "Should have 1 successful first allocation")
		
		blockedCount := testutil.ToFloat64(m.bufferAllocationAttempts.WithLabelValues(
			"capture", source, "repeated_blocked",
		))
		assert.Equal(t, float64(3), blockedCount, "Should have 3 blocked repeated allocations")
	})
}
