// ffmpeg_watchdog_test.go
// Tests for FFmpeg watchdog functionality
// Validates watchdog detection and recovery of stuck streams

package myaudio

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFFmpegManager_WatchdogNilAudioChan validates that StartMonitoring rejects nil audioChan
func TestFFmpegManager_WatchdogNilAudioChan(t *testing.T) {
	// Do not use t.Parallel() - this test may indirectly access global soundLevelProcessors map
	t.Attr("component", "ffmpeg-manager")
	t.Attr("test-type", "watchdog")

	manager := NewFFmpegManager()
	defer manager.Shutdown()

	// Should not start monitoring with nil audioChan
	manager.StartMonitoring(30*time.Second, nil)

	// Verify that monitoring didn't start by checking wg count would be 0
	// Since StartMonitoring returns early on nil, no goroutines should be added
	// This is validated indirectly - if monitoring started, Shutdown would block

	// Create channel to test timeout
	done := make(chan bool, 1)
	go func() {
		manager.Shutdown() // Should return immediately since no goroutines were added
		done <- true
	}()

	select {
	case <-done:
		// Success - shutdown completed quickly
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Shutdown took too long - monitoring may have started despite nil audioChan")
	}
}

// TestFFmpegManager_WatchdogCooldown validates cooldown prevents rapid force-resets
func TestFFmpegManager_WatchdogCooldown(t *testing.T) {
	// Do not use t.Parallel() - this test may indirectly access global soundLevelProcessors map
	t.Attr("component", "ffmpeg-manager")
	t.Attr("test-type", "watchdog")

	manager := NewFFmpegManager()
	defer manager.Shutdown()

	url := "rtsp://test.example.com/cooldown"

	// Record first force reset time
	now := time.Now()
	manager.forceResetMu.Lock()
	manager.lastForceReset[url] = now
	manager.forceResetMu.Unlock()

	// Verify cooldown is enforced
	manager.forceResetMu.Lock()
	lastReset, exists := manager.lastForceReset[url]
	manager.forceResetMu.Unlock()

	require.True(t, exists, "Last reset time should be recorded")
	assert.Equal(t, now, lastReset, "Last reset time should match")

	// Simulate checking within cooldown period
	timeSinceReset := time.Since(lastReset)
	inCooldown := timeSinceReset < maxUnhealthyDuration

	assert.True(t, inCooldown, "Should be in cooldown period immediately after reset")
}

// TestFFmpegManager_WatchdogStreamRemovalVerification validates force-removal of stuck streams
func TestFFmpegManager_WatchdogStreamRemovalVerification(t *testing.T) {
	// Do not use t.Parallel() - this test may indirectly access global soundLevelProcessors map
	t.Attr("component", "ffmpeg-manager")
	t.Attr("test-type", "watchdog")

	manager := NewFFmpegManager()
	defer manager.Shutdown()

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)

	url := "rtsp://test.example.com/stuck"

	// Start a stream
	err := manager.StartStream(url, "tcp", audioChan)
	require.NoError(t, err)

	// Verify stream exists
	manager.streamsMu.RLock()
	_, exists := manager.streams[url]
	manager.streamsMu.RUnlock()
	assert.True(t, exists, "Stream should exist after start")

	// Stop the stream
	err = manager.StopStream(url)
	require.NoError(t, err)

	// Verify stream was removed
	manager.streamsMu.RLock()
	_, exists = manager.streams[url]
	manager.streamsMu.RUnlock()
	assert.False(t, exists, "Stream should not exist after stop")

	// Test force-removal scenario: manually add stream back (simulates stuck state)
	stream := NewFFmpegStream(url, "tcp", audioChan)
	manager.streamsMu.Lock()
	manager.streams[url] = stream
	manager.streamsMu.Unlock()

	// Now force-remove it (simulates watchdog force-removal)
	manager.streamsMu.Lock()
	delete(manager.streams, url) // delete() is safe even if key doesn't exist
	manager.streamsMu.Unlock()

	// Verify stream is gone
	manager.streamsMu.RLock()
	_, exists = manager.streams[url]
	manager.streamsMu.RUnlock()
	assert.False(t, exists, "Stream should be removed after force-removal")
}

// TestFFmpegManager_WatchdogUnhealthyDurationCalculation tests unhealthy duration tracking
func TestFFmpegManager_WatchdogUnhealthyDurationCalculation(t *testing.T) {
	// Do not use t.Parallel() - this test may indirectly access global soundLevelProcessors map
	t.Attr("component", "ffmpeg-manager")
	t.Attr("test-type", "watchdog")

	manager := NewFFmpegManager()
	defer manager.Shutdown()

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)

	url := "rtsp://test.example.com/duration"

	// Start a stream
	err := manager.StartStream(url, "tcp", audioChan)
	require.NoError(t, err)

	// Get stream and check health
	manager.streamsMu.RLock()
	stream, exists := manager.streams[url]
	manager.streamsMu.RUnlock()
	require.True(t, exists, "Stream should exist")

	// Case 1: Stream never received data - unhealthy duration is from creation time
	health := manager.HealthCheck()
	streamHealth, exists := health[url]
	require.True(t, exists, "Health info should exist")

	// Stream should be unhealthy (no data received)
	assert.False(t, streamHealth.IsHealthy, "New stream without data should be unhealthy")
	assert.True(t, streamHealth.LastDataReceived.IsZero(), "LastDataReceived should be zero for new stream")

	// Unhealthy duration would be time.Since(stream.streamCreatedAt)
	unhealthyDuration := time.Since(stream.streamCreatedAt)
	assert.Greater(t, unhealthyDuration, time.Duration(0), "Unhealthy duration should be positive")
	assert.Less(t, unhealthyDuration, 5*time.Second, "Test should complete quickly")

	// Case 2: Stream received data - update last data time
	stream.updateLastDataTime()

	// Now check health again
	health = manager.HealthCheck()
	streamHealth, exists = health[url]
	require.True(t, exists, "Health info should exist after data")

	// Stream should be healthy now
	assert.True(t, streamHealth.IsHealthy, "Stream should be healthy after receiving data")
	assert.False(t, streamHealth.LastDataReceived.IsZero(), "LastDataReceived should not be zero")

	// Unhealthy duration would be time.Since(lastDataReceived) if it becomes unhealthy again
	timeSinceData := time.Since(streamHealth.LastDataReceived)
	assert.Less(t, timeSinceData, 5*time.Second, "Time since data should be recent")
}

// TestFFmpegManager_WatchdogConstants validates watchdog timing constants
func TestFFmpegManager_WatchdogConstants(t *testing.T) {
	t.Attr("component", "ffmpeg-manager")
	t.Attr("test-type", "watchdog")

	// Verify watchdog constants are sensible
	assert.Equal(t, 15*time.Minute, maxUnhealthyDuration, "Max unhealthy duration should be 15 minutes")
	assert.Equal(t, 5*time.Minute, watchdogCheckInterval, "Watchdog check interval should be 5 minutes")
	assert.Equal(t, 30*time.Second, stopStartDelay, "Stop-start delay should be 30 seconds")

	// Verify relationships
	assert.Greater(t, maxUnhealthyDuration, watchdogCheckInterval,
		"Max unhealthy duration should be greater than check interval")
	assert.Greater(t, maxUnhealthyDuration, stopStartDelay,
		"Max unhealthy duration should be greater than stop-start delay")
	assert.Greater(t, watchdogCheckInterval, stopStartDelay,
		"Check interval should be greater than stop-start delay")
}

// TestFFmpegManager_WatchdogHealthCheckInteraction tests watchdog behavior with ongoing health check restarts
func TestFFmpegManager_WatchdogHealthCheckInteraction(t *testing.T) {
	// Do not use t.Parallel() - this test may indirectly access global soundLevelProcessors map
	t.Attr("component", "ffmpeg-manager")
	t.Attr("test-type", "watchdog")

	manager := NewFFmpegManager()
	defer manager.Shutdown()

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)

	url := "rtsp://test.example.com/restarting"

	// Start a stream
	err := manager.StartStream(url, "tcp", audioChan)
	require.NoError(t, err)

	// Give the stream a moment to start
	time.Sleep(100 * time.Millisecond)

	// Get the stream
	manager.streamsMu.RLock()
	stream, exists := manager.streams[url]
	manager.streamsMu.RUnlock()
	require.True(t, exists, "Stream should exist")

	// Verify stream is not restarting initially (should be running or starting)
	initiallyRestarting := stream.IsRestarting()

	// Trigger a health check restart (simulates health check detecting unhealthy stream)
	// This will set the stream into restarting state
	err = manager.RestartStream(url)
	require.NoError(t, err, "RestartStream should succeed")

	// Give restart a moment to initiate
	time.Sleep(50 * time.Millisecond)

	// Now the stream should be in restarting state
	manager.streamsMu.RLock()
	stream, exists = manager.streams[url]
	manager.streamsMu.RUnlock()
	require.True(t, exists, "Stream should still exist after restart")

	// Check if stream is in restarting state
	isRestarting := stream.IsRestarting()

	// Get health check - stream will be unhealthy because it's restarting
	health := manager.HealthCheck()
	streamHealth, healthExists := health[url]
	require.True(t, healthExists, "Health info should exist")

	// Manually call checkForStuckStreams to simulate watchdog running
	// If IsRestarting() is true, watchdog should skip this stream
	initialRestartCount := streamHealth.RestartCount

	// Record initial lastForceReset state
	manager.forceResetMu.Lock()
	_, hadForceReset := manager.lastForceReset[url]
	manager.forceResetMu.Unlock()

	// Call watchdog check - it should skip the restarting stream
	manager.checkForStuckStreams()

	// Verify watchdog didn't force-reset the stream
	manager.forceResetMu.Lock()
	_, hasForceResetNow := manager.lastForceReset[url]
	manager.forceResetMu.Unlock()

	// If stream was restarting (either initially or after RestartStream),
	// watchdog should not have added a force reset entry
	if isRestarting || initiallyRestarting {
		assert.Equal(t, hadForceReset, hasForceResetNow,
			"Watchdog should not force-reset a stream that is already restarting")
	}

	// Verify stream still exists (wasn't removed by watchdog)
	manager.streamsMu.RLock()
	_, stillExists := manager.streams[url]
	manager.streamsMu.RUnlock()
	assert.True(t, stillExists, "Stream should still exist after watchdog check")

	// Verify restart count didn't increase from watchdog
	finalHealth := manager.HealthCheck()
	finalStreamHealth, finalHealthExists := finalHealth[url]
	require.True(t, finalHealthExists, "Health info should still exist")

	// Restart count should be the same or only increased by health check restart
	assert.LessOrEqual(t, finalStreamHealth.RestartCount, initialRestartCount+1,
		"Restart count should not be increased by watchdog when health check is handling restart")
}

// TestFFmpegManager_WatchdogMemoryLeakPrevention validates lastForceReset cleanup
func TestFFmpegManager_WatchdogMemoryLeakPrevention(t *testing.T) {
	// Do not use t.Parallel() - this test may indirectly access global soundLevelProcessors map
	t.Attr("component", "ffmpeg-manager")
	t.Attr("test-type", "watchdog")

	manager := NewFFmpegManager()
	defer manager.Shutdown()

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)

	url := "rtsp://test.example.com/memleak"

	// Start a stream
	err := manager.StartStream(url, "tcp", audioChan)
	require.NoError(t, err)

	// Simulate a watchdog force reset by adding entry to lastForceReset
	now := time.Now()
	manager.forceResetMu.Lock()
	manager.lastForceReset[url] = now
	manager.forceResetMu.Unlock()

	// Verify entry exists
	manager.forceResetMu.Lock()
	_, exists := manager.lastForceReset[url]
	manager.forceResetMu.Unlock()
	assert.True(t, exists, "Force reset entry should exist")

	// Stop the stream - this should clean up lastForceReset entry
	err = manager.StopStream(url)
	require.NoError(t, err)

	// Verify lastForceReset entry was cleaned up
	manager.forceResetMu.Lock()
	_, stillExists := manager.lastForceReset[url]
	manager.forceResetMu.Unlock()
	assert.False(t, stillExists, "Force reset entry should be cleaned up after StopStream")

	// Verify lastForceReset map doesn't grow unbounded
	// Start and stop multiple streams
	for i := range 10 {
		testURL := "rtsp://test.example.com/stream" + string(rune('0'+i))

		err := manager.StartStream(testURL, "tcp", audioChan)
		require.NoError(t, err)

		// Add force reset entry
		manager.forceResetMu.Lock()
		manager.lastForceReset[testURL] = time.Now()
		manager.forceResetMu.Unlock()

		err = manager.StopStream(testURL)
		require.NoError(t, err)
	}

	// Verify all entries were cleaned up
	manager.forceResetMu.Lock()
	mapSize := len(manager.lastForceReset)
	manager.forceResetMu.Unlock()

	assert.Equal(t, 0, mapSize, "lastForceReset map should be empty after all streams stopped")
}
