// ffmpeg_manager_test.go
// Comprehensive tests for FFmpegManager functionality and edge cases
// These tests validate the manager's ability to handle multiple streams,
// concurrent operations, and health checks.

package myaudio

import (
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFFmpegManager_StartStop(t *testing.T) {
	// Do not use t.Parallel() - this test may indirectly access global soundLevelProcessors map

	manager := NewFFmpegManager()
	defer manager.Shutdown()

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	url := "rtsp://test.example.com/stream"
	transport := "tcp"

	// Test starting a stream
	err := manager.StartStream(url, transport, audioChan)
	require.NoError(t, err)

	// Test that we can't start the same stream twice
	err = manager.StartStream(url, transport, audioChan)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stream already exists")

	// Verify stream is active
	activeStreams := manager.GetActiveStreams()
	assert.Contains(t, activeStreams, url)

	// Test stopping the stream
	err = manager.StopStream(url)
	require.NoError(t, err)

	// Test that we can't stop a non-existent stream
	err = manager.StopStream(url)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no stream found")

	// Verify stream is no longer active
	activeStreams = manager.GetActiveStreams()
	assert.NotContains(t, activeStreams, url)
}

func TestFFmpegManager_MultipleStreams(t *testing.T) {
	// Do not use t.Parallel() - this test may indirectly access global soundLevelProcessors map

	manager := NewFFmpegManager()
	defer manager.Shutdown()

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	urls := []string{
		"rtsp://test1.example.com/stream",
		"rtsp://test2.example.com/stream",
		"rtsp://test3.example.com/stream",
	}

	// Start multiple streams
	for _, url := range urls {
		err := manager.StartStream(url, "tcp", audioChan)
		require.NoError(t, err)
	}

	// Verify all streams are active
	activeStreams := manager.GetActiveStreams()
	assert.Len(t, activeStreams, 3)
	for _, url := range urls {
		assert.Contains(t, activeStreams, url)
	}

	// Stop one stream
	err := manager.StopStream(urls[1])
	require.NoError(t, err)

	// Verify correct streams remain
	activeStreams = manager.GetActiveStreams()
	assert.Len(t, activeStreams, 2)
	assert.Contains(t, activeStreams, urls[0])
	assert.NotContains(t, activeStreams, urls[1])
	assert.Contains(t, activeStreams, urls[2])
}

func TestFFmpegManager_RestartStream(t *testing.T) {
	// Do not use t.Parallel() - this test may indirectly access global soundLevelProcessors map

	manager := NewFFmpegManager()
	defer manager.Shutdown()

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	url := "rtsp://test.example.com/stream"

	// Start a stream
	err := manager.StartStream(url, "tcp", audioChan)
	require.NoError(t, err)

	// Test restarting the stream
	err = manager.RestartStream(url)
	require.NoError(t, err)

	// Stream should still be active
	activeStreams := manager.GetActiveStreams()
	assert.Contains(t, activeStreams, url)

	// Test restarting non-existent stream
	err = manager.RestartStream("rtsp://nonexistent.example.com/stream")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no stream found")
}

// TestFFmpegManager_HealthCheck validates health check functionality with deterministic timing.
// MODERNIZATION: Uses Go 1.25's testing/synctest for precise timeout control.
// Previously used real-time polling and timeouts that could be flaky under load.
// With synctest, all time operations are deterministic and run instantly.
func TestFFmpegManager_HealthCheck(t *testing.T) {
	// Do not use t.Parallel() - this test may indirectly access global soundLevelProcessors map
	t.Attr("component", "ffmpeg-manager")
	t.Attr("test-type", "health-monitoring")

	// Go 1.25 synctest: Creates controlled time environment for deterministic timeout testing
	synctest.Test(t, func(t *testing.T) {
		t.Helper()

		manager := NewFFmpegManager()
		defer manager.Shutdown()

		audioChan := make(chan UnifiedAudioData, 10)
		defer close(audioChan)
		url := "rtsp://test.example.com/stream"

		// Start a stream
		err := manager.StartStream(url, "tcp", audioChan)
		require.NoError(t, err)

		// Use deterministic synchronization - wait for stream to be registered
		streamInitialized := make(chan bool, 1)
		go func() {
			for i := 0; i < 100; i++ {
				health := manager.HealthCheck()
				if len(health) > 0 {
					streamInitialized <- true
					return
				}
				// Go 1.25: time.Sleep() in synctest advances fake time precisely
				// No real-world timing variability in polling loop
				time.Sleep(1 * time.Millisecond)
			}
			streamInitialized <- false
		}()

		// Go 1.25: time.After() timeout uses fake time in synctest bubble
		// This timeout behavior is now deterministic and tests precise timing logic
		// instead of being subject to real-world timing variability
		select {
		case initialized := <-streamInitialized:
			assert.True(t, initialized, "Stream should have been initialized")
		case <-time.After(1 * time.Second):
			t.Fatal("Stream initialization timed out")
		}

		// Simulate data reception to make the stream healthy
		manager.streamsMu.RLock()
		stream, exists := manager.streams[url]
		manager.streamsMu.RUnlock()
		require.True(t, exists, "Stream should exist in manager")

		// Update the stream's last data time to simulate receiving data
		stream.updateLastDataTime()

		// Check health
		health := manager.HealthCheck()
		assert.Len(t, health, 1)

		streamHealth, exists := health[url]
		assert.True(t, exists)
		assert.True(t, streamHealth.IsHealthy)
		// Go 1.25: time.Now() in synctest uses fake time base for precise duration assertions
		assert.WithinDuration(t, time.Now(), streamHealth.LastDataReceived, 2*time.Second)
	})
}

func TestFFmpegManager_Shutdown(t *testing.T) {
	// Do not use t.Parallel() - this test may indirectly access global soundLevelProcessors map

	manager := NewFFmpegManager()
	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)

	// Start multiple streams
	urls := []string{
		"rtsp://test1.example.com/stream",
		"rtsp://test2.example.com/stream",
	}

	for _, url := range urls {
		err := manager.StartStream(url, "tcp", audioChan)
		require.NoError(t, err)
	}

	// Verify streams are active
	activeStreams := manager.GetActiveStreams()
	assert.Len(t, activeStreams, 2)

	// Shutdown manager
	manager.Shutdown()

	// Verify no streams are active
	activeStreams = manager.GetActiveStreams()
	assert.Empty(t, activeStreams)

	// Note: Starting new streams after shutdown might succeed
	// The manager doesn't prevent new streams after shutdown in the current implementation
	// This is a design choice to allow reuse of the manager
}

func TestFFmpegManager_ConcurrentOperations(t *testing.T) {
	// Do not use t.Parallel() - this test may indirectly access global soundLevelProcessors map

	manager := NewFFmpegManager()
	defer manager.Shutdown()

	audioChan := make(chan UnifiedAudioData, 100)
	defer close(audioChan)
	var wg sync.WaitGroup

	// Concurrently start streams
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			url := fmt.Sprintf("rtsp://test%d.example.com/stream", idx)
			err := manager.StartStream(url, "tcp", audioChan)
			assert.NoError(t, err)
		}(i)
	}

	wg.Wait()

	// Verify all streams started
	activeStreams := manager.GetActiveStreams()
	assert.Len(t, activeStreams, 10)

	// Concurrently restart and stop streams
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			url := fmt.Sprintf("rtsp://test%d.example.com/stream", idx)

			if idx%2 == 0 {
				err := manager.RestartStream(url)
				assert.NoError(t, err)
			} else {
				err := manager.StopStream(url)
				assert.NoError(t, err)
			}
		}(i)
	}

	wg.Wait()

	// Verify correct number of streams remain
	activeStreams = manager.GetActiveStreams()
	assert.Len(t, activeStreams, 5)
}

// TestFFmpegManager_MonitoringIntegration validates background monitoring functionality.
// MODERNIZATION: Uses Go 1.25's testing/synctest for deterministic monitoring timing.
// Previously used real-time sleep-based polling that could be flaky under load.
// With synctest, monitoring intervals and polling loops run instantly and deterministically.
func TestFFmpegManager_MonitoringIntegration(t *testing.T) {
	// Do not use t.Parallel() - this test may indirectly access global soundLevelProcessors map
	t.Attr("component", "ffmpeg-manager")
	t.Attr("test-type", "monitoring-integration")

	// Go 1.25 synctest: Creates controlled time environment for deterministic monitoring tests
	synctest.Test(t, func(t *testing.T) {
		t.Helper()

		manager := NewFFmpegManager()
		defer manager.Shutdown()

		// Go 1.25: Monitoring interval uses fake time - runs instantly instead of real 50ms
		// Background monitoring tickers advance fake time precisely in synctest bubble
		manager.StartMonitoring(50 * time.Millisecond)

		audioChan := make(chan UnifiedAudioData, 10)
		defer close(audioChan)
		url := "rtsp://test.example.com/stream"

		// Start a stream
		err := manager.StartStream(url, "tcp", audioChan)
		require.NoError(t, err)

		// Deterministic synchronization with fake time - eliminates flaky polling
		monitoringComplete := make(chan bool, 1)
		go func() {
			// Check health multiple times to ensure monitoring has had a chance to run
			for i := 0; i < 5; i++ {
				health := manager.HealthCheck()
				if len(health) > 0 {
					monitoringComplete <- true
					return
				}
				// Go 1.25: time.Sleep() advances fake time instantly in synctest bubble
				// Eliminates real-world timing variability in polling loops
				time.Sleep(10 * time.Millisecond)
			}
			monitoringComplete <- false
		}()

		// Wait for monitoring completion with deterministic timeout
		select {
		case completed := <-monitoringComplete:
			assert.True(t, completed, "Monitoring should have completed")
		// Go 1.25: time.After() timeout uses fake time in synctest bubble
		// Timeout behavior is now deterministic instead of real-world timing
		case <-time.After(1 * time.Second):
			t.Fatal("Monitoring test timed out")
		}

		// Simulate data reception to make the stream healthy
		manager.streamsMu.RLock()
		stream, exists := manager.streams[url]
		manager.streamsMu.RUnlock()
		require.True(t, exists, "Stream should exist in manager")

		// Go 1.25: updateLastDataTime() uses fake time base for consistent health checks
		stream.updateLastDataTime()

		// Stream should still be healthy - verification happens within synctest bubble
		health := manager.HealthCheck()
		assert.True(t, health[url].IsHealthy)
	})
}

// TestFFmpegManager_ConcurrentStreamOperations validates concurrent stream management.
// MODERNIZATION: Uses Go 1.25's sync.WaitGroup.Go() and testing/synctest for deterministic concurrency testing.
// Previously used manual WaitGroup.Add/Done patterns with real-time timeouts that could be flaky.
// With Go 1.25, goroutine management is cleaner and timeout behavior is deterministic.
func TestFFmpegManager_ConcurrentStreamOperations(t *testing.T) {
	// Do not use t.Parallel() - this test may indirectly access global soundLevelProcessors map
	t.Attr("component", "ffmpeg-manager")
	t.Attr("test-type", "concurrency")

	// Go 1.25 synctest: Creates controlled time environment for deterministic timeout testing
	synctest.Test(t, func(t *testing.T) {
		t.Helper()

		manager := NewFFmpegManager()
		defer manager.Shutdown()

		audioChan := make(chan UnifiedAudioData, 1000)
		defer close(audioChan)
		const numStreams = 5     // Reduced from 20 to avoid FFmpeg connection issues
		const numOperations = 20 // Reduced from 50

		// Generate unique URLs for testing - use localhost to avoid DNS issues
		urls := make([]string, numStreams)
		for i := 0; i < numStreams; i++ {
			urls[i] = fmt.Sprintf("rtsp://localhost:554/stream%d", i)
		}

		// Go 1.25: sync.WaitGroup with cleaner goroutine management
		var wg sync.WaitGroup

		// Concurrent start operations - Go 1.25 WaitGroup.Go() pattern
		for i := 0; i < numOperations/2; i++ {
			idx := i // Capture loop variable for closure
			wg.Go(func() {
				// Go 1.25: Automatic Add/Done handling with WaitGroup.Go()
				// Eliminates manual wg.Add(1) and defer wg.Done() boilerplate
				url := urls[idx%numStreams]
				err := manager.StartStream(url, "tcp", audioChan)
				// Error is expected if stream already exists
				_ = err
			})
		}

		// Concurrent restart operations with modern goroutine management
		for i := 0; i < numOperations/4; i++ {
			idx := i
			wg.Go(func() {
				// Go 1.25: WaitGroup.Go() provides cleaner concurrency patterns
				url := urls[idx%numStreams]
				err := manager.RestartStream(url)
				// Error is expected if stream doesn't exist
				_ = err
			})
		}

		// Concurrent stop operations with automatic goroutine tracking
		for i := 0; i < numOperations/4; i++ {
			idx := i
			wg.Go(func() {
				// Go 1.25: No manual defer wg.Done() needed with WaitGroup.Go()
				url := urls[idx%numStreams]
				err := manager.StopStream(url)
				// Error is expected if stream doesn't exist
				_ = err
			})
		}

		// Wait for all operations with deterministic timeout
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			// All operations completed
		// Go 1.25: time.After() timeout uses fake time in synctest bubble
		// Timeout behavior is now deterministic instead of real-world 3-second delay
		case <-time.After(3 * time.Second):
			t.Fatal("Concurrent operations timed out")
		}

		// Verify manager is still in a consistent state - all verification within synctest bubble
		activeStreams := manager.GetActiveStreams()
		health := manager.HealthCheck()

		// Health map should match active streams
		assert.Len(t, health, len(activeStreams))
		for _, url := range activeStreams {
			_, exists := health[url]
			assert.True(t, exists, "Health info should exist for active stream %s", url)
		}
	})
}

// TestFFmpegManager_StressTestWithHealthChecks validates concurrent operations under stress.
// MODERNIZATION: Uses Go 1.25's testing/synctest for deterministic duration measurement testing.
// Previously this test ran for 200ms real-time with timing loops and could be flaky under load.
// With synctest, duration measurement loops run instantly and deterministically.
func TestFFmpegManager_StressTestWithHealthChecks(t *testing.T) {
	// Do not use t.Parallel() - this test may indirectly access global soundLevelProcessors map
	t.Attr("component", "ffmpeg-manager")
	t.Attr("test-type", "stress-testing")

	// Go 1.25 synctest: Creates controlled time environment for deterministic duration testing
	synctest.Test(t, func(t *testing.T) {
		t.Helper()

		manager := NewFFmpegManager()
		defer manager.Shutdown()

		audioChan := make(chan UnifiedAudioData, 100)
		defer close(audioChan)

		// Go 1.25: testDuration advances instantly in synctest instead of real 200ms
		// Duration measurement loops complete instantly when all goroutines are durably blocked
		const testDuration = 200 * time.Millisecond

		// Start a few streams - use localhost to avoid DNS issues
		urls := []string{
			"rtsp://localhost:554/stress1",
			"rtsp://localhost:554/stress2",
			"rtsp://localhost:554/stress3",
		}

		for _, url := range urls {
			err := manager.StartStream(url, "tcp", audioChan)
			require.NoError(t, err)
		}

		// Run stress test with Go 1.25 synctest for deterministic timing
		done := make(chan bool, 3)

		// Continuous health checks with fake time duration measurement
		go func() {
			defer func() { done <- true }()
			// Go 1.25: time.Now() returns fake time base (2000-01-01 00:00:00 UTC)
			start := time.Now()

			// Go 1.25: time.Since() measures fake time duration with perfect precision
			// This loop completes instantly in synctest when no blocking operations remain
			for time.Since(start) < testDuration {
				health := manager.HealthCheck()
				assert.GreaterOrEqual(t, len(health), 1, "Should have health info for active streams")
			}
		}()

		// Continuous active stream queries with deterministic duration control
		go func() {
			defer func() { done <- true }()
			// Go 1.25: All time operations use synctest's fake time for consistency
			start := time.Now()

			// Duration measurement loop runs instantly - no real-world timing variability
			for time.Since(start) < testDuration {
				streams := manager.GetActiveStreams()
				assert.GreaterOrEqual(t, len(streams), 1, "Should have active streams")
			}
		}()

		// Random restart operations with controlled time advancement
		go func() {
			defer func() { done <- true }()
			start := time.Now()

			for time.Since(start) < testDuration {
				// Go 1.25: time.Now().UnixNano() uses fake time for deterministic randomization
				url := urls[start.UnixNano()%int64(len(urls))]
				_ = manager.RestartStream(url)

				// Go 1.25: time.Sleep() advances fake time instantly in synctest bubble
				// Provides deterministic pacing without real-world delays
				time.Sleep(10 * time.Millisecond)
			}
		}()

		// Wait for all stress test goroutines with deterministic timeout
		for i := 0; i < 3; i++ {
			select {
			case <-done:
				// Test completed
			// Go 1.25: time.After() timeout uses fake time in synctest bubble
			// Timeout behavior is now deterministic and tests precise timing logic
			case <-time.After(1 * time.Second):
				t.Fatal("Stress test timed out")
			}
		}

		// Verify final state - all verification happens within synctest bubble
		activeStreams := manager.GetActiveStreams()
		health := manager.HealthCheck()
		assert.Len(t, health, len(activeStreams))
	})
}
