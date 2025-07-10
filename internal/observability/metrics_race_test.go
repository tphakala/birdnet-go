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
	for i := 0; i < numGoroutines; i++ {
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