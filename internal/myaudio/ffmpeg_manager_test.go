// ffmpeg_manager_test.go
// Comprehensive tests for FFmpegManager functionality and edge cases
// These tests validate the manager's ability to handle multiple streams,
// concurrent operations, and health checks.

package myaudio

import (
	"fmt"
	"sync"
	"testing"
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

func TestFFmpegManager_HealthCheck(t *testing.T) {
	// Do not use t.Parallel() - this test may indirectly access global soundLevelProcessors map

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
			time.Sleep(1 * time.Millisecond)
		}
		streamInitialized <- false
	}()

	// Wait for stream initialization
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
	assert.WithinDuration(t, time.Now(), streamHealth.LastDataReceived, 2*time.Second)
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

func TestFFmpegManager_MonitoringIntegration(t *testing.T) {
	// Do not use t.Parallel() - this test may indirectly access global soundLevelProcessors map

	manager := NewFFmpegManager()
	defer manager.Shutdown()

	// Start monitoring with short interval for testing
	manager.StartMonitoring(50 * time.Millisecond)

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	url := "rtsp://test.example.com/stream"

	// Start a stream
	err := manager.StartStream(url, "tcp", audioChan)
	require.NoError(t, err)

	// Use deterministic synchronization - check health multiple times with short delays
	// until we get a stable health reading
	monitoringComplete := make(chan bool, 1)
	go func() {
		// Check health multiple times to ensure monitoring has had a chance to run
		for i := 0; i < 5; i++ {
			health := manager.HealthCheck()
			if len(health) > 0 {
				monitoringComplete <- true
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		monitoringComplete <- false
	}()

	// Wait for monitoring completion or timeout
	select {
	case completed := <-monitoringComplete:
		assert.True(t, completed, "Monitoring should have completed")
	case <-time.After(1 * time.Second):
		t.Fatal("Monitoring test timed out")
	}

	// Simulate data reception to make the stream healthy
	manager.streamsMu.RLock()
	stream, exists := manager.streams[url]
	manager.streamsMu.RUnlock()
	require.True(t, exists, "Stream should exist in manager")
	
	// Update the stream's last data time to simulate receiving data
	stream.updateLastDataTime()

	// Stream should still be healthy
	health := manager.HealthCheck()
	assert.True(t, health[url].IsHealthy)
}

func TestFFmpegManager_ConcurrentStreamOperations(t *testing.T) {
	// Do not use t.Parallel() - this test may indirectly access global soundLevelProcessors map

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

	// Use sync.WaitGroup for better synchronization
	var wg sync.WaitGroup

	// Concurrent start operations
	for i := 0; i < numOperations/2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			url := urls[idx%numStreams]
			err := manager.StartStream(url, "tcp", audioChan)
			// Error is expected if stream already exists
			_ = err
		}(i)
	}

	// Concurrent restart operations
	for i := 0; i < numOperations/4; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			url := urls[idx%numStreams]
			err := manager.RestartStream(url)
			// Error is expected if stream doesn't exist
			_ = err
		}(i)
	}

	// Concurrent stop operations
	for i := 0; i < numOperations/4; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			url := urls[idx%numStreams]
			err := manager.StopStream(url)
			// Error is expected if stream doesn't exist
			_ = err
		}(i)
	}

	// Wait for all operations with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All operations completed
	case <-time.After(3 * time.Second):
		t.Fatal("Concurrent operations timed out")
	}

	// Verify manager is still in a consistent state
	activeStreams := manager.GetActiveStreams()
	health := manager.HealthCheck()

	// Health map should match active streams
	assert.Len(t, health, len(activeStreams))
	for _, url := range activeStreams {
		_, exists := health[url]
		assert.True(t, exists, "Health info should exist for active stream %s", url)
	}
}

func TestFFmpegManager_StressTestWithHealthChecks(t *testing.T) {
	// Do not use t.Parallel() - this test may indirectly access global soundLevelProcessors map

	manager := NewFFmpegManager()
	defer manager.Shutdown()

	audioChan := make(chan UnifiedAudioData, 100)
	defer close(audioChan)
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

	// Run stress test
	done := make(chan bool, 3)

	// Continuous health checks
	go func() {
		defer func() { done <- true }()
		start := time.Now()
		for time.Since(start) < testDuration {
			health := manager.HealthCheck()
			assert.GreaterOrEqual(t, len(health), 1, "Should have health info for active streams")
		}
	}()

	// Continuous active stream queries
	go func() {
		defer func() { done <- true }()
		start := time.Now()
		for time.Since(start) < testDuration {
			streams := manager.GetActiveStreams()
			assert.GreaterOrEqual(t, len(streams), 1, "Should have active streams")
		}
	}()

	// Random restart operations
	go func() {
		defer func() { done <- true }()
		start := time.Now()
		for time.Since(start) < testDuration {
			url := urls[start.UnixNano()%int64(len(urls))]
			_ = manager.RestartStream(url)
			time.Sleep(10 * time.Millisecond)
		}
	}()

	// Wait for all stress test goroutines
	for i := 0; i < 3; i++ {
		select {
		case <-done:
			// Test completed
		case <-time.After(1 * time.Second):
			t.Fatal("Stress test timed out")
		}
	}

	// Verify final state
	activeStreams := manager.GetActiveStreams()
	health := manager.HealthCheck()
	assert.Len(t, health, len(activeStreams))
}
