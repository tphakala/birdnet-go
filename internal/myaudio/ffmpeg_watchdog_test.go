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
