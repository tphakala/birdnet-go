package datastore

import (
	"sync"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/observability/metrics"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// TestDataStoreMetricsThreadSafety tests that metrics field access is thread-safe
func TestDataStoreMetricsThreadSafety(t *testing.T) {
	t.Parallel()

	ds := &DataStore{
		metrics: &Metrics{},
	}

	// Create a mock SunCalc instance
	sunCalc := &suncalc.SunCalc{}
	ds.SunCalc = sunCalc

	const numGoroutines = 10
	const numOperations = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2) // 2 types of operations

	// Start goroutines that set metrics
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				// Create a new metrics instance for each set operation
				newMetrics := &Metrics{}
				ds.SetMetrics(newMetrics)
				time.Sleep(time.Microsecond) // Small delay to increase chance of race
			}
		}()
	}

	// Start goroutines that set SunCalc metrics
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				// Create a new SunCalc metrics instance
				sunCalcMetrics := &metrics.SunCalcMetrics{}
				ds.SetSunCalcMetrics(sunCalcMetrics)
				time.Sleep(time.Microsecond) // Small delay to increase chance of race
			}
		}()
	}

	// Wait for all operations to complete
	wg.Wait()

	// Verify the DataStore is in a consistent state
	if ds.metrics == nil {
		t.Error("metrics field should not be nil after operations")
	}
}

// TestDataStoreMetricsAccessThreadSafety tests that metrics field reads are thread-safe
func TestDataStoreMetricsAccessThreadSafety(t *testing.T) {
	t.Parallel()

	ds := &DataStore{
		metrics: &Metrics{},
	}

	const numGoroutines = 10
	const numOperations = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2) // 2 types of operations

	// Start goroutines that set metrics
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				newMetrics := &Metrics{}
				ds.SetMetrics(newMetrics)
				time.Sleep(time.Microsecond)
			}
		}()
	}

	// Start goroutines that access metrics (simulating monitoring)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				// Simulate the pattern used in monitoring.go
				ds.metricsMu.RLock()
				metrics := ds.metrics
				ds.metricsMu.RUnlock()
				
				// Use the metrics reference safely
				if metrics != nil {
					// Simulate metrics call (no-op for test)
					_ = metrics
				}
				time.Sleep(time.Microsecond)
			}
		}()
	}

	wg.Wait()
}

// TestDataStoreSetSunCalcMetricsThreadSafety tests thread safety of SunCalc metrics setting
func TestDataStoreSetSunCalcMetricsThreadSafety(t *testing.T) {
	t.Parallel()

	ds := &DataStore{
		SunCalc: &suncalc.SunCalc{},
	}

	const numGoroutines = 10
	const numOperations = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2)

	// Start goroutines that set SunCalc metrics
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				sunCalcMetrics := &metrics.SunCalcMetrics{}
				ds.SetSunCalcMetrics(sunCalcMetrics)
				time.Sleep(time.Microsecond)
			}
		}()
	}

	// Start goroutines that access SunCalc field
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				// Simulate the pattern used in SetSunCalcMetrics
				ds.metricsMu.RLock()
				sunCalc := ds.SunCalc
				ds.metricsMu.RUnlock()
				
				if sunCalc != nil {
					// Simulate accessing SunCalc (no-op for test)
					_ = sunCalc
				}
				time.Sleep(time.Microsecond)
			}
		}()
	}

	wg.Wait()
}

// TestDataStoreMetricsNilSafety tests that nil metrics don't cause panics
func TestDataStoreMetricsNilSafety(t *testing.T) {
	t.Parallel()

	ds := &DataStore{
		metrics: nil, // Start with nil metrics
	}

	// Test SetMetrics with nil
	ds.SetMetrics(nil)

	// Test SetSunCalcMetrics with nil DataStore.SunCalc
	ds.SetSunCalcMetrics(&metrics.SunCalcMetrics{})

	// Test SetSunCalcMetrics with nil metrics
	ds.SunCalc = &suncalc.SunCalc{}
	ds.SetSunCalcMetrics(nil)

	// Test SetSunCalcMetrics with wrong type
	ds.SetSunCalcMetrics("not a metrics object")

	// All operations should complete without panics
}

// TestDataStoreMetricsRaceCondition uses the race detector to catch race conditions
func TestDataStoreMetricsRaceCondition(t *testing.T) {
	// This test is most effective when run with: go test -race
	t.Parallel()

	ds := &DataStore{
		metrics: &Metrics{},
		SunCalc: &suncalc.SunCalc{},
	}

	const numGoroutines = 50
	const numOperations = 20

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 3) // 3 types of operations

	// Concurrent SetMetrics operations
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				ds.SetMetrics(&Metrics{})
			}
		}()
	}

	// Concurrent SetSunCalcMetrics operations
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				ds.SetSunCalcMetrics(&metrics.SunCalcMetrics{})
			}
		}()
	}

	// Concurrent metrics access operations
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				// Access pattern similar to monitoring code
				ds.metricsMu.RLock()
				localMetrics := ds.metrics
				localSunCalc := ds.SunCalc
				ds.metricsMu.RUnlock()

				// Use the local references
				if localMetrics != nil {
					_ = localMetrics
				}
				if localSunCalc != nil {
					_ = localSunCalc
				}
			}
		}()
	}

	wg.Wait()
}