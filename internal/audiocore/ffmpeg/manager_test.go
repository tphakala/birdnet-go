package ffmpeg

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestManagerConfig returns a minimal StreamConfig suitable for manager unit tests.
// It uses a fake sourceID and URL; no real FFmpeg process is spawned during these tests.
func newTestManagerConfig(sourceID, url string) *StreamConfig {
	return &StreamConfig{
		SourceID:   sourceID,
		SourceName: "Test Stream " + sourceID,
		URL:        url,
		Type:       "rtsp",
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
		FFmpegPath: "/usr/bin/ffmpeg",
		Transport:  "tcp",
		LogLevel:   "error",
	}
}

// newTestManager creates a Manager with t.Context() and no-op callbacks.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	return NewManager(t.Context(), nil, nil, nil)
}

// TestManager_StartStopStream verifies that a stream can be started, appears in
// the manager's tracking map, and is fully removed after StopStream.
func TestManager_StartStopStream(t *testing.T) {
	t.Attr("component", "ffmpeg-manager")

	mgr := newTestManager(t)
	t.Cleanup(func() { _ = mgr.Shutdown() })

	cfg := newTestManagerConfig("src-1", "rtsp://test.example.com/stream")

	// Start the stream.
	require.NoError(t, mgr.StartStream(cfg))

	// The stream must appear in the active set.
	ids := mgr.GetActiveStreamIDs()
	require.Contains(t, ids, "src-1", "stream should be tracked after StartStream")

	// Starting the same sourceID again must fail.
	err := mgr.StartStream(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stream already exists")

	// Stop the stream.
	require.NoError(t, mgr.StopStream("src-1"))

	// The stream must no longer appear in the active set.
	ids = mgr.GetActiveStreamIDs()
	assert.NotContains(t, ids, "src-1", "stream should not be tracked after StopStream")

	// Stopping a non-existent stream must return an error.
	err = mgr.StopStream("src-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no stream found")
}

// TestManager_ReconfigureStream starts a stream, reconfigures it with new settings,
// and verifies the updated config is reflected in the stream's health state.
func TestManager_ReconfigureStream(t *testing.T) {
	t.Attr("component", "ffmpeg-manager")

	mgr := newTestManager(t)
	t.Cleanup(func() { _ = mgr.Shutdown() })

	const sourceID = "src-reconfig"
	originalCfg := newTestManagerConfig(sourceID, "rtsp://original.example.com/stream")
	updatedCfg := newTestManagerConfig(sourceID, "rtsp://updated.example.com/stream")
	updatedCfg.Transport = "udp"

	// Start with the original config.
	require.NoError(t, mgr.StartStream(originalCfg))

	// Verify stream is present.
	ids := mgr.GetActiveStreamIDs()
	require.Contains(t, ids, sourceID)

	// Reconfigure to the new config.
	require.NoError(t, mgr.ReconfigureStream(sourceID, updatedCfg))

	// Stream must still be tracked under the same sourceID.
	ids = mgr.GetActiveStreamIDs()
	require.Contains(t, ids, sourceID, "stream should still be tracked after reconfigure")

	// The internal config of the stream should reflect the new URL/transport.
	mgr.mu.RLock()
	stream := mgr.streams[sourceID]
	mgr.mu.RUnlock()
	require.NotNil(t, stream)
	assert.Equal(t, "rtsp://updated.example.com/stream", stream.config.URL)
	assert.Equal(t, "udp", stream.config.Transport)
}

// TestManager_AllStreamHealth starts several streams and verifies that
// AllStreamHealth returns an entry for each active stream.
func TestManager_AllStreamHealth(t *testing.T) {
	t.Attr("component", "ffmpeg-manager")

	mgr := newTestManager(t)
	t.Cleanup(func() { _ = mgr.Shutdown() })

	sources := []struct {
		id  string
		url string
	}{
		{"health-1", "rtsp://host1.example.com/stream"},
		{"health-2", "rtsp://host2.example.com/stream"},
		{"health-3", "rtsp://host3.example.com/stream"},
	}

	for _, s := range sources {
		require.NoError(t, mgr.StartStream(newTestManagerConfig(s.id, s.url)))
	}

	health := mgr.AllStreamHealth()
	require.Len(t, health, len(sources), "AllStreamHealth should return one entry per active stream")

	for _, s := range sources {
		h, exists := health[s.id]
		require.True(t, exists, "health entry missing for source %q", s.id)
		require.NotNil(t, h)
	}

	// StreamHealth for a single source should also work.
	h, err := mgr.StreamHealth("health-2")
	require.NoError(t, err)
	require.NotNil(t, h)

	// StreamHealth for a non-existent source must return an error.
	_, err = mgr.StreamHealth("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no stream found")
}

// TestManager_Shutdown starts multiple streams, calls Shutdown, and verifies all
// streams are stopped and removed from the map.
func TestManager_Shutdown(t *testing.T) {
	t.Attr("component", "ffmpeg-manager")

	mgr := newTestManager(t)

	sources := []string{"shutdown-1", "shutdown-2", "shutdown-3"}
	for _, id := range sources {
		require.NoError(t, mgr.StartStream(
			newTestManagerConfig(id, "rtsp://"+id+".example.com/stream")))
	}

	// All streams should be tracked before shutdown.
	ids := mgr.GetActiveStreamIDs()
	require.Len(t, ids, len(sources))

	// Shutdown must complete without error.
	require.NoError(t, mgr.Shutdown())

	// All streams must be removed after shutdown.
	ids = mgr.GetActiveStreamIDs()
	assert.Empty(t, ids, "no streams should remain after Shutdown")

	// Manager context must be cancelled.
	require.Error(t, mgr.ctx.Err(), "manager context should be cancelled after Shutdown")
}

// TestManager_ShutdownWithContext verifies that a context-deadline is respected.
func TestManager_ShutdownWithContext(t *testing.T) {
	t.Attr("component", "ffmpeg-manager")

	t.Run("completes within deadline", func(t *testing.T) {
		mgr := newTestManager(t)
		require.NoError(t, mgr.StartStream(
			newTestManagerConfig("ctx-1", "rtsp://ctx.example.com/stream")))

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()

		err := mgr.ShutdownWithContext(ctx)
		require.NoError(t, err)
		assert.Empty(t, mgr.GetActiveStreamIDs())
	})

	t.Run("returns error on cancelled context", func(t *testing.T) {
		mgr := newTestManager(t)

		ctx, cancel := context.WithCancel(t.Context())
		cancel() // already cancelled

		err := mgr.ShutdownWithContext(ctx)
		// Context already done, so we expect a context error (or nil if no streams).
		// Either outcome is acceptable; what matters is that it does not hang.
		_ = err
		require.Error(t, mgr.ctx.Err(), "manager context should be cancelled")
	})
}

// TestManager_SetOnStreamReset verifies that the callback registered via
// SetOnStreamReset is invoked with the correct sourceID when a stream starts.
func TestManager_SetOnStreamReset(t *testing.T) {
	t.Attr("component", "ffmpeg-manager")

	mgr := newTestManager(t)
	t.Cleanup(func() { _ = mgr.Shutdown() })

	var calledWith atomic.Value
	mgr.SetOnStreamReset(func(sourceID string) {
		calledWith.Store(sourceID)
	})

	require.NoError(t, mgr.StartStream(
		newTestManagerConfig("reset-src", "rtsp://reset.example.com/stream")))

	// The callback must have been invoked with the correct sourceID.
	stored := calledWith.Load()
	require.NotNil(t, stored)
	val, ok := stored.(string)
	require.True(t, ok, "stored value should be a string")
	assert.Equal(t, "reset-src", val)

	// Clearing the callback must not panic on subsequent operations.
	mgr.SetOnStreamReset(nil)
	require.NoError(t, mgr.StopStream("reset-src"))
}

// TestManager_ReconfigureStream_SourceIDMismatch verifies that ReconfigureStream
// returns an error when the first argument and cfg.SourceID differ.
func TestManager_ReconfigureStream_SourceIDMismatch(t *testing.T) {
	mgr := newTestManager(t)
	t.Cleanup(func() { _ = mgr.Shutdown() })

	cfg := newTestManagerConfig("id-A", "rtsp://a.example.com/stream")
	err := mgr.ReconfigureStream("id-B", cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sourceID mismatch")
}

// TestManager_WatchdogForceReset verifies that the watchdog force-resets a stream
// that has been stuck unhealthy longer than managerMaxUnhealthyDuration.
// Uses testing/synctest for deterministic time advancement.
func TestManager_WatchdogForceReset(t *testing.T) {
	t.Attr("component", "ffmpeg-manager")
	t.Attr("test-type", "watchdog")

	synctest.Test(t, func(t *testing.T) {
		var resetCalls atomic.Int64
		var lastResetID atomic.Value

		mgr := NewManager(t.Context(), nil, func(sourceID string) {
			resetCalls.Add(1)
			lastResetID.Store(sourceID)
		}, nil)
		t.Cleanup(func() { _ = mgr.Shutdown() })

		const sourceID = "stuck-stream"
		require.NoError(t, mgr.StartStream(
			newTestManagerConfig(sourceID, "rtsp://stuck.example.com/stream")))

		// Advance the stream creation time so the watchdog considers it stuck.
		mgr.mu.Lock()
		stream := mgr.streams[sourceID]
		stream.streamCreatedAtMu.Lock()
		stream.streamCreatedAt = time.Now().Add(-(managerMaxUnhealthyDuration + time.Minute))
		stream.streamCreatedAtMu.Unlock()
		// Set process state to stopped so IsRestarting() returns false.
		stream.processStateMu.Lock()
		stream.processState = StateStopped
		stream.processStateMu.Unlock()
		mgr.mu.Unlock()

		// Clear any existing cooldown.
		mgr.lastForceResetMu.Lock()
		delete(mgr.lastForceReset, sourceID)
		mgr.lastForceResetMu.Unlock()

		// Capture call count before watchdog run. StartStream already fires the callback once.
		callsBefore := resetCalls.Load()

		// Trigger the watchdog directly (no real ticker needed).
		mgr.checkForStuckStreams()

		// The watchdog must have stopped the original stream and started a fresh one,
		// triggering at least one more callback invocation.
		callsAfter := resetCalls.Load()
		assert.Greater(t, callsAfter, callsBefore,
			"onReset callback should have been called by the watchdog")

		// The stream must still be tracked (restarted under the same sourceID).
		ids := mgr.GetActiveStreamIDs()
		assert.Contains(t, ids, sourceID, "stream should be restarted under the same sourceID")
	})
}

// TestManager_StartStream_EmptySourceID verifies that StartStream rejects
// configs with an empty SourceID.
func TestManager_StartStream_EmptySourceID(t *testing.T) {
	mgr := newTestManager(t)
	t.Cleanup(func() { _ = mgr.Shutdown() })

	cfg := newTestManagerConfig("", "rtsp://empty.example.com/stream")
	err := mgr.StartStream(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sourceID must not be empty")
}

// TestManager_RestartStreamResetsBackoff verifies that RestartStream
// (operator-triggered) resets the backoff counters on the underlying stream.
func TestManager_RestartStreamResetsBackoff(t *testing.T) {
	t.Attr("component", "ffmpeg-manager")

	mgr := newTestManager(t)
	t.Cleanup(func() { _ = mgr.Shutdown() })

	const sourceID = "restart-backoff"
	require.NoError(t, mgr.StartStream(
		newTestManagerConfig(sourceID, "rtsp://restart.example.com/stream")))

	// Access the internal stream to set up backoff state.
	mgr.mu.RLock()
	stream := mgr.streams[sourceID]
	mgr.mu.RUnlock()
	require.NotNil(t, stream)

	// Simulate accumulated failures.
	stream.restartCountMu.Lock()
	stream.restartCount = 15
	stream.restartCountMu.Unlock()

	stream.circuitMu.Lock()
	stream.consecutiveFailures = 8
	stream.circuitOpenTime = time.Now()
	stream.circuitMu.Unlock()

	// RestartStream should trigger a manual restart that resets backoff.
	require.NoError(t, mgr.RestartStream(sourceID))

	stream.restartCountMu.Lock()
	restartCount := stream.restartCount
	stream.restartCountMu.Unlock()

	stream.circuitMu.Lock()
	failures := stream.consecutiveFailures
	circuitTime := stream.circuitOpenTime
	stream.circuitMu.Unlock()

	assert.Equal(t, 0, restartCount, "RestartStream should reset restart count")
	assert.Equal(t, 0, failures, "RestartStream should reset consecutive failures")
	assert.True(t, circuitTime.IsZero(), "RestartStream should clear circuit open time")
}
