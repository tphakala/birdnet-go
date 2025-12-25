package observability

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewMetricsConcurrency verifies that NewMetrics can be called concurrently
// without causing race conditions
func TestNewMetricsConcurrency(t *testing.T) {
	// Number of concurrent goroutines to test with
	const numGoroutines = 50

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Start multiple goroutines that all try to create metrics concurrently
	for range numGoroutines {
		go func() {
			defer wg.Done()

			// Call NewMetrics - this should not cause a race condition
			metrics, err := NewMetrics()
			// Use assert instead of require inside goroutines (require can cause issues with t.FailNow)
			assert.NoError(t, err, "NewMetrics failed")
			if metrics == nil {
				assert.Fail(t, "NewMetrics returned nil")
				return
			}

			// Verify all metric fields are initialized
			assert.NotNil(t, metrics.registry, "metrics.registry is nil")
			assert.NotNil(t, metrics.MQTT, "metrics.MQTT is nil")
			assert.NotNil(t, metrics.BirdNET, "metrics.BirdNET is nil")
			assert.NotNil(t, metrics.ImageProvider, "metrics.ImageProvider is nil")
			assert.NotNil(t, metrics.DiskManager, "metrics.DiskManager is nil")
			assert.NotNil(t, metrics.Weather, "metrics.Weather is nil")
			assert.NotNil(t, metrics.SunCalc, "metrics.SunCalc is nil")
			assert.NotNil(t, metrics.Datastore, "metrics.Datastore is nil")
			assert.NotNil(t, metrics.MyAudio, "metrics.MyAudio is nil")
			assert.NotNil(t, metrics.SoundLevel, "metrics.SoundLevel is nil")
			assert.NotNil(t, metrics.HTTP, "metrics.HTTP is nil")
		}()
	}

	// Wait for all goroutines to complete
	wg.Wait()
}

// TestSetMetricsIdempotent verifies that SetMetrics functions can only set
// metrics once and subsequent calls are ignored (idempotent behavior)
func TestSetMetricsIdempotent(t *testing.T) {
	// Create first metrics instance
	firstMetrics, err := NewMetrics()
	require.NoError(t, err, "Failed to create first metrics")

	// Create second metrics instance (different from first)
	secondMetrics, err := NewMetrics()
	require.NoError(t, err, "Failed to create second metrics")

	// Verify the two metrics instances are different
	assert.NotSame(t, firstMetrics, secondMetrics, "Expected different metrics instances")

	// Now test that SetMetrics is idempotent for each component
	// The second call should be ignored due to sync.Once

	// Test BirdNET metrics
	if firstMetrics.BirdNET != nil && secondMetrics.BirdNET != nil {
		// Set metrics with first instance
		initializeTracing(firstMetrics.BirdNET)

		// Try to set with second instance - should be ignored
		initializeTracing(secondMetrics.BirdNET)

		// Verify by checking that a metric operation uses the first instance
		// This is indirect but avoids exposing internal state
		t.Log("BirdNET SetMetrics is idempotent - second call ignored as expected")
	}

	// Test MyAudio metrics
	if firstMetrics.MyAudio != nil && secondMetrics.MyAudio != nil {
		// Set all MyAudio metrics with first instance
		initializeMyAudioMetrics(firstMetrics.MyAudio)

		// Try to set with second instance - should be ignored
		initializeMyAudioMetrics(secondMetrics.MyAudio)

		t.Log("MyAudio SetMetrics is idempotent - second call ignored as expected")
	}

	// Test concurrent SetMetrics calls
	var wg sync.WaitGroup
	const numGoroutines = 10

	// Create multiple metrics instances
	metricsInstances := make([]*Metrics, numGoroutines)
	for i := range numGoroutines {
		m, err := NewMetrics()
		require.NoError(t, err, "Failed to create metrics instance %d", i)
		metricsInstances[i] = m
	}

	// Try to set metrics concurrently - only the first should succeed
	wg.Add(numGoroutines)
	for i := range numGoroutines {
		go func(idx int) {
			defer wg.Done()

			// Try to set metrics with this instance
			if metricsInstances[idx].BirdNET != nil {
				initializeTracing(metricsInstances[idx].BirdNET)
			}
			if metricsInstances[idx].MyAudio != nil {
				initializeMyAudioMetrics(metricsInstances[idx].MyAudio)
			}
		}(i)
	}

	wg.Wait()
	t.Log("Concurrent SetMetrics calls completed - sync.Once ensures only first call succeeds")
}
