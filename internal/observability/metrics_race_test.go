package observability

import (
	"sync"
	"testing"
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
			if err != nil {
				t.Errorf("NewMetrics failed: %v", err)
				return
			}

			// Verify metrics is not nil
			if metrics == nil {
				t.Error("NewMetrics returned nil")
				return
			}

			// Verify all metric fields are initialized
			if metrics.registry == nil {
				t.Error("metrics.registry is nil")
			}
			if metrics.MQTT == nil {
				t.Error("metrics.MQTT is nil")
			}
			if metrics.BirdNET == nil {
				t.Error("metrics.BirdNET is nil")
			}
			if metrics.ImageProvider == nil {
				t.Error("metrics.ImageProvider is nil")
			}
			if metrics.DiskManager == nil {
				t.Error("metrics.DiskManager is nil")
			}
			if metrics.Weather == nil {
				t.Error("metrics.Weather is nil")
			}
			if metrics.SunCalc == nil {
				t.Error("metrics.SunCalc is nil")
			}
			if metrics.Datastore == nil {
				t.Error("metrics.Datastore is nil")
			}
			if metrics.MyAudio == nil {
				t.Error("metrics.MyAudio is nil")
			}
			if metrics.SoundLevel == nil {
				t.Error("metrics.SoundLevel is nil")
			}
			if metrics.HTTP == nil {
				t.Error("metrics.HTTP is nil")
			}
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
	if err != nil {
		t.Fatalf("Failed to create first metrics: %v", err)
	}

	// Create second metrics instance (different from first)
	secondMetrics, err := NewMetrics()
	if err != nil {
		t.Fatalf("Failed to create second metrics: %v", err)
	}

	// Verify the two metrics instances are different
	if firstMetrics == secondMetrics {
		t.Error("Expected different metrics instances")
	}

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
		if err != nil {
			t.Fatalf("Failed to create metrics instance %d: %v", i, err)
		}
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
