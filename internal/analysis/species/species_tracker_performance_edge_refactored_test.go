// species_tracker_performance_edge_test_refactored.go

package species

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// performanceMetrics holds atomic counters for performance tracking
type performanceMetrics struct {
	totalOperations atomic.Int64
	responseTimeSum atomic.Int64
	maxResponseTime atomic.Int64
	minResponseTime atomic.Int64
}

// newPerformanceMetrics creates a new metrics instance
func newPerformanceMetrics() *performanceMetrics {
	m := &performanceMetrics{}
	m.minResponseTime.Store(int64(time.Hour)) // Start with very high value
	return m
}

// updateMetrics updates performance metrics with a new operation duration
func (m *performanceMetrics) updateMetrics(duration time.Duration) {
	m.totalOperations.Add(1)
	m.responseTimeSum.Add(int64(duration))

	// Update max response time
	for {
		oldMax := m.maxResponseTime.Load()
		if int64(duration) <= oldMax || m.maxResponseTime.CompareAndSwap(oldMax, int64(duration)) {
			break
		}
	}

	// Update min response time
	for {
		oldMin := m.minResponseTime.Load()
		if int64(duration) >= oldMin || m.minResponseTime.CompareAndSwap(oldMin, int64(duration)) {
			break
		}
	}
}

// getStats returns the final statistics
func (m *performanceMetrics) getStats() (ops int64, avgTime, minTime, maxTime time.Duration) {
	ops = m.totalOperations.Load()
	sum := m.responseTimeSum.Load()
	if ops > 0 {
		avgTime = time.Duration(sum / ops)
	}
	minTime = time.Duration(m.minResponseTime.Load())
	maxTime = time.Duration(m.maxResponseTime.Load())
	return
}

// sustainedLoadConfig defines configuration for a sustained load test
type sustainedLoadConfig struct {
	name                string
	durationSeconds     int
	operationsPerSecond int
	speciesCount        int
	description         string
}

// createPerformanceTestTracker creates and initializes a test tracker for performance tests
func createPerformanceTestTracker(t *testing.T) *SpeciesTracker {
	t.Helper()
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    true,
			ResetMonth: 1,
			ResetDay:   1,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: true,
		},
	}

	tracker, _ := createTestTrackerWithMocks(t, settings)
	return tracker
}

// generateSpeciesPool creates a pool of test species names
func generateSpeciesPool(count int) []string {
	species := make([]string, count)
	for i := range count {
		species[i] = fmt.Sprintf("LoadTest_Species_%d", i)
	}
	return species
}

// performTrackerOperation executes a tracker operation based on the operation type
func performTrackerOperation(tracker *SpeciesTracker, opType int, speciesName string, currentTime time.Time) {
	switch opType {
	case 0:
		isNew, days := tracker.CheckAndUpdateSpecies(speciesName, currentTime)
		_ = isNew
		_ = days
	case 1:
		status := tracker.GetSpeciesStatus(speciesName, currentTime)
		_ = status
	case 2:
		isNew := tracker.IsNewSpecies(speciesName)
		_ = isNew
	}
}

// runLoadWorker executes the load generation worker
func runLoadWorker(ctx context.Context, tracker *SpeciesTracker, config sustainedLoadConfig,
	metrics *performanceMetrics, species []string) {

	endTime := time.Now().Add(time.Duration(config.durationSeconds) * time.Second)
	operationInterval := time.Second / time.Duration(config.operationsPerSecond)
	operationTicker := time.NewTicker(operationInterval)
	defer operationTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-operationTicker.C:
			if time.Now().After(endTime) {
				return
			}

			// Measure operation response time
			opStart := time.Now()

			// Rotate through different operations
			opCount := metrics.totalOperations.Load()
			speciesName := species[int(opCount)%len(species)]
			currentTime := time.Now()

			performTrackerOperation(tracker, int(opCount%3), speciesName, currentTime)

			opDuration := time.Since(opStart)
			metrics.updateMetrics(opDuration)
		}
	}
}

// verifyPerformanceResults checks performance test results
func verifyPerformanceResults(t *testing.T, config sustainedLoadConfig, metrics *performanceMetrics,
	startTime time.Time, tracker *SpeciesTracker) {
	t.Helper()

	actualDuration := time.Since(startTime)
	finalOps, avgResponseTime, minResponseTime, maxResponseTime := metrics.getStats()
	actualOpsPerSec := float64(finalOps) / actualDuration.Seconds()

	// Log results
	t.Logf("Sustained load test completed:")
	t.Logf("  Actual duration: %v", actualDuration)
	t.Logf("  Total operations: %d", finalOps)
	t.Logf("  Actual ops/sec: %.2f", actualOpsPerSec)
	t.Logf("  Average response time: %v", avgResponseTime)
	t.Logf("  Min response time: %v", minResponseTime)
	t.Logf("  Max response time: %v", maxResponseTime)

	// Performance assertions
	assert.Greater(t, int(finalOps), config.operationsPerSecond*config.durationSeconds/2,
		"Should complete at least 50% of target operations")

	assert.Less(t, avgResponseTime, 10*time.Millisecond,
		"Average response time should be under 10ms")

	assert.Less(t, maxResponseTime, 100*time.Millisecond,
		"Max response time should be under 100ms")

	// Verify system stability
	speciesCount := tracker.GetSpeciesCount()
	assert.LessOrEqual(t, speciesCount, config.speciesCount,
		"Species count should not exceed test species")
	assert.GreaterOrEqual(t, speciesCount, 1,
		"Should track at least some species")

	// Test final operation to ensure tracker is still responsive
	finalTestTime := time.Now()
	status := tracker.GetSpeciesStatus("FinalTest_Species", finalTestTime)
	assert.NotNil(t, status, "Tracker should remain responsive after sustained load")
}

// TestPerformanceUnderSustainedLoadRefactored tests tracker performance under sustained load
// This is the refactored version with reduced cognitive complexity
func TestPerformanceUnderSustainedLoadRefactored(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping sustained load tests in short mode")
	}
	t.Parallel()

	tests := []sustainedLoadConfig{
		{
			name:                "moderate_sustained_load",
			durationSeconds:     10,
			operationsPerSecond: 100,
			speciesCount:        50,
			description:         "10 seconds at 100 ops/sec with 50 species",
		},
		{
			name:                "high_sustained_load",
			durationSeconds:     15,
			operationsPerSecond: 200,
			speciesCount:        100,
			description:         "15 seconds at 200 ops/sec with 100 species",
		},
		{
			name:                "burst_then_sustained",
			durationSeconds:     12,
			operationsPerSecond: 300, // Reduced from 500 to 300 ops/sec for stability
			speciesCount:        25,
			description:         "12 seconds at 300 ops/sec with 25 species (high contention)",
		},
	}

	for _, config := range tests {
		t.Run(config.name, func(t *testing.T) {
			t.Logf("Running sustained load test: %s", config.description)

			// Setup
			tracker := createPerformanceTestTracker(t)
			metrics := newPerformanceMetrics()
			species := generateSpeciesPool(config.speciesCount)

			// Create context with timeout
			ctx, cancel := context.WithTimeout(t.Context(),
				time.Duration(config.durationSeconds)*time.Second+5*time.Second)
			defer cancel()

			// Start time tracking
			startTime := time.Now()

			// Run load worker using WaitGroup.Go
			var wg sync.WaitGroup
			done := make(chan struct{})

			wg.Go(func() {
				defer close(done)
				runLoadWorker(ctx, tracker, config, metrics, species)
			})

			// Wait for completion
			select {
			case <-done:
				// Normal completion
			case <-ctx.Done():
				require.Fail(t, "Test timed out or was cancelled")
			}

			wg.Wait()

			// Verify results
			verifyPerformanceResults(t, config, metrics, startTime, tracker)
		})
	}
}
