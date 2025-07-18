// api_goroutine_test.go: Tests for verifying goroutine cleanup in API v2

package api

import (
	"runtime"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// TestControllerShutdownCleansUpGoroutines verifies that background goroutines
// are properly cleaned up when the controller is shut down
func TestControllerShutdownCleansUpGoroutines(t *testing.T) {
	// Get baseline goroutine count before creating controller
	runtime.GC() // Force GC to clean up any lingering goroutines
	time.Sleep(100 * time.Millisecond) // Give time for cleanup
	baselineCount := runtime.NumGoroutine()

	// Create Echo instance
	e := echo.New()

	// Create mock datastore
	mockDS := new(MockDataStore)

	// Create settings with required paths
	settings := &conf.Settings{
		WebServer: conf.WebServerSettings{
			Debug: true,
		},
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				Export: conf.ExportSettings{
					Path: t.TempDir(),
				},
			},
		},
	}

	// Create control channel
	controlChan := make(chan string, 10)

	// Create mock metrics
	mockMetrics, _ := observability.NewMetrics()

	// Create controller WITH route initialization to start background goroutines
	controller, err := NewWithOptions(e, mockDS, settings, nil, nil, controlChan, nil, nil, mockMetrics, true)
	require.NoError(t, err)

	// Wait for goroutines to start
	time.Sleep(200 * time.Millisecond)

	// Get count with controller running
	runningCount := runtime.NumGoroutine()
	
	// Should have more goroutines than baseline (CPU monitoring, support cleanup, cache janitors, etc.)
	assert.Greater(t, runningCount, baselineCount, "Should have started background goroutines")

	// Shutdown the controller
	controller.Shutdown()

	// Wait for goroutines to terminate
	time.Sleep(500 * time.Millisecond)

	// Force GC again to clean up
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	// Get final count
	finalCount := runtime.NumGoroutine()

	// Should be back close to baseline (may have a few extra from the test framework itself)
	// Allow some tolerance as the exact count can vary
	tolerance := 5
	assert.LessOrEqual(t, finalCount, baselineCount+tolerance, 
		"Goroutine count after shutdown should be close to baseline. Baseline: %d, Final: %d", 
		baselineCount, finalCount)

	// The important check is that we have fewer goroutines than when running
	assert.Less(t, finalCount, runningCount, 
		"Should have fewer goroutines after shutdown. Running: %d, Final: %d",
		runningCount, finalCount)
}

// TestGoroutineCleanupWithoutRoutes verifies that creating a controller without
// routes doesn't start unnecessary goroutines
func TestGoroutineCleanupWithoutRoutes(t *testing.T) {
	// Get baseline goroutine count
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	baselineCount := runtime.NumGoroutine()

	// Setup test environment (which uses NewWithOptions with initializeRoutes=false)
	_, _, controller := setupTestEnvironment(t)

	// Wait a bit to ensure no goroutines are starting
	time.Sleep(200 * time.Millisecond)

	// Get count with controller
	withControllerCount := runtime.NumGoroutine()

	// Should not have significantly more goroutines (just the cache janitor)
	// Allow for some variance due to test framework
	assert.LessOrEqual(t, withControllerCount, baselineCount+3, 
		"Should not start many goroutines without route initialization")

	// Cleanup
	controller.Shutdown()
}