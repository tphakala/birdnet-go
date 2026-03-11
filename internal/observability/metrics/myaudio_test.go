package metrics

import (
	"strconv"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordAudioConversion(t *testing.T) {
	// Create a new registry for testing
	registry := prometheus.NewRegistry()
	m, err := NewMyAudioMetrics(registry)
	require.NoError(t, err)

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
			assert.InDelta(t, float64(1), count, 0.01)
		})
	}
}

func TestRecordAudioConversionError(t *testing.T) {
	// Create a new registry for testing
	registry := prometheus.NewRegistry()
	m, err := NewMyAudioMetrics(registry)
	require.NoError(t, err)

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
			assert.InDelta(t, float64(1), count, 0.01)
		})
	}
}

// Benchmark to measure performance improvement
func BenchmarkRecordAudioConversion_FmtSprintf(b *testing.B) {
	registry := prometheus.NewRegistry()
	m, err := NewMyAudioMetrics(registry)
	require.NoError(b, err)

	for b.Loop() {
		m.RecordAudioConversion("wav", 16, "success")
	}
}

func BenchmarkRecordAudioConversionError_FmtSprintf(b *testing.B) {
	registry := prometheus.NewRegistry()
	m, err := NewMyAudioMetrics(registry)
	require.NoError(b, err)

	for b.Loop() {
		m.RecordAudioConversionError("wav", 16, "error")
	}
}

func TestRecordBirdNETProcessingOverrun(t *testing.T) {
	t.Parallel()

	t.Run("records counter and histograms", func(t *testing.T) {
		t.Parallel()
		registry := prometheus.NewRegistry()
		m, err := NewMyAudioMetrics(registry)
		require.NoError(t, err)

		m.RecordBirdNETProcessingOverrun("mic_0", 3.5, 2.4)

		// Counter incremented
		count := testutil.ToFloat64(m.birdnetProcessingOverrunsTotal.WithLabelValues("mic_0"))
		assert.InDelta(t, 1.0, count, 0.01)

		// Verify histograms were observed by collecting all metrics from registry
		metricFamilies, err := registry.Gather()
		require.NoError(t, err)

		var foundDuration, foundRatio bool
		for _, mf := range metricFamilies {
			switch mf.GetName() {
			case "myaudio_birdnet_processing_overrun_duration_seconds":
				foundDuration = true
				assert.Positive(t, mf.GetMetric()[0].GetHistogram().GetSampleCount())
			case "myaudio_birdnet_processing_overrun_ratio":
				foundRatio = true
				assert.Positive(t, mf.GetMetric()[0].GetHistogram().GetSampleCount())
			}
		}
		assert.True(t, foundDuration, "duration histogram should be present")
		assert.True(t, foundRatio, "ratio histogram should be present")
	})

	t.Run("multiple overruns accumulate", func(t *testing.T) {
		t.Parallel()
		registry := prometheus.NewRegistry()
		m, err := NewMyAudioMetrics(registry)
		require.NoError(t, err)

		for range 5 {
			m.RecordBirdNETProcessingOverrun("rtsp_camera1", 4.0, 2.4)
		}

		count := testutil.ToFloat64(m.birdnetProcessingOverrunsTotal.WithLabelValues("rtsp_camera1"))
		assert.InDelta(t, 5.0, count, 0.01)
	})

	t.Run("zero buffer length skips ratio", func(t *testing.T) {
		t.Parallel()
		registry := prometheus.NewRegistry()
		m, err := NewMyAudioMetrics(registry)
		require.NoError(t, err)

		m.RecordBirdNETProcessingOverrun("mic_zero", 3.0, 0)

		// Counter should still increment
		count := testutil.ToFloat64(m.birdnetProcessingOverrunsTotal.WithLabelValues("mic_zero"))
		assert.InDelta(t, 1.0, count, 0.01)

		// Ratio histogram should have zero samples (skipped due to zero buffer length)
		metricFamilies, err := registry.Gather()
		require.NoError(t, err)
		for _, mf := range metricFamilies {
			if mf.GetName() == "myaudio_birdnet_processing_overrun_ratio" {
				for _, metric := range mf.GetMetric() {
					assert.Zero(t, metric.GetHistogram().GetSampleCount(),
						"ratio histogram should have no samples when buffer length is zero")
				}
			}
		}
	})
}

func TestRecordBufferAllocationAttempt(t *testing.T) {
	// Create a new registry for testing
	registry := prometheus.NewRegistry()
	m, err := NewMyAudioMetrics(registry)
	require.NoError(t, err)

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
			assert.InDelta(t, tc.expected, count, 0.01)
		})
	}

	// Test repeated allocation scenario
	t.Run("repeated allocation scenario", func(t *testing.T) {
		source := "rtsp://test_camera"

		// Simulate multiple allocation attempts
		m.RecordBufferAllocationAttempt("capture", source, "attempted")
		m.RecordBufferAllocationAttempt("capture", source, "first_allocation")

		// Simulate repeated attempts
		for range 3 {
			m.RecordBufferAllocationAttempt("capture", source, "attempted")
			m.RecordBufferAllocationAttempt("capture", source, "repeated_blocked")
		}

		// Verify counts
		attemptedCount := testutil.ToFloat64(m.bufferAllocationAttempts.WithLabelValues(
			"capture", source, "attempted",
		))
		assert.InDelta(t, float64(4), attemptedCount, 0.01, "Should have 4 attempted allocations")

		firstAllocCount := testutil.ToFloat64(m.bufferAllocationAttempts.WithLabelValues(
			"capture", source, "first_allocation",
		))
		assert.InDelta(t, float64(1), firstAllocCount, 0.01, "Should have 1 successful first allocation")

		blockedCount := testutil.ToFloat64(m.bufferAllocationAttempts.WithLabelValues(
			"capture", source, "repeated_blocked",
		))
		assert.InDelta(t, float64(3), blockedCount, 0.01, "Should have 3 blocked repeated allocations")
	})
}
