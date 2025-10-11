package myaudio

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFFmpegManager_TransportFieldAccessible verifies that the transport field
// in FFmpegStream is accessible for configuration comparison.
// This validates that SyncWithConfig can detect transport changes by comparing
// stream.transport against the configuration transport setting.
func TestFFmpegManager_TransportFieldAccessible(t *testing.T) {
	// Do not use t.Parallel() - this test may indirectly access global soundLevelProcessors map

	manager := NewFFmpegManager()
	defer manager.Shutdown()

	audioChan := make(chan UnifiedAudioData, 100)
	defer close(audioChan)

	testURL := "rtsp://test.local/stream_transport"

	// Start stream with TCP transport
	err := manager.StartStream(testURL, "tcp", audioChan)
	require.NoError(t, err)

	// Verify we can read the transport field for comparison
	manager.streamsMu.RLock()
	stream, exists := manager.streams[testURL]
	require.True(t, exists)
	assert.Equal(t, "tcp", stream.transport, "Should be able to read transport field")
	manager.streamsMu.RUnlock()

	// Brief delay
	time.Sleep(50 * time.Millisecond)

	// Verify transport field is immutable during stream lifetime
	// (only changes via Stop + Start cycle)
	manager.streamsMu.RLock()
	stream2, exists := manager.streams[testURL]
	require.True(t, exists)
	assert.Equal(t, "tcp", stream2.transport, "Transport should remain TCP")
	assert.Same(t, stream, stream2, "Should be same stream instance")
	manager.streamsMu.RUnlock()
}

// TestFFmpegManager_TransportPersistsThroughRestart verifies that when a stream
// is restarted via RestartStream(), it maintains its original transport setting
func TestFFmpegManager_TransportPersistsThroughRestart(t *testing.T) {
	// Do not use t.Parallel() - this test may indirectly access global soundLevelProcessors map

	manager := NewFFmpegManager()
	defer manager.Shutdown()

	audioChan := make(chan UnifiedAudioData, 100)
	defer close(audioChan)

	testURL := "rtsp://test.local/stream_restart"

	// Start with UDP
	err := manager.StartStream(testURL, "udp", audioChan)
	require.NoError(t, err)

	manager.streamsMu.RLock()
	originalTransport := manager.streams[testURL].transport
	manager.streamsMu.RUnlock()
	assert.Equal(t, "udp", originalTransport)

	// Restart the stream
	err = manager.RestartStream(testURL)
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	// Verify transport persists through restart
	manager.streamsMu.RLock()
	restartedTransport := manager.streams[testURL].transport
	manager.streamsMu.RUnlock()
	assert.Equal(t, "udp", restartedTransport, "Transport should persist through RestartStream()")
}

// TestFFmpegManager_DifferentTransportsForDifferentStreams verifies that multiple
// streams can have different transport settings simultaneously
func TestFFmpegManager_DifferentTransportsForDifferentStreams(t *testing.T) {
	// Do not use t.Parallel() - this test may indirectly access global soundLevelProcessors map

	manager := NewFFmpegManager()
	defer manager.Shutdown()

	audioChan := make(chan UnifiedAudioData, 100)
	defer close(audioChan)

	tcpURL := "rtsp://test.local/stream_tcp"
	udpURL := "rtsp://test.local/stream_udp"

	// Start one with TCP
	err := manager.StartStream(tcpURL, "tcp", audioChan)
	require.NoError(t, err)

	// Start one with UDP
	err = manager.StartStream(udpURL, "udp", audioChan)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)

	// Verify both have their respective transports
	manager.streamsMu.RLock()
	tcpStream, exists := manager.streams[tcpURL]
	require.True(t, exists)
	assert.Equal(t, "tcp", tcpStream.transport)

	udpStream, exists := manager.streams[udpURL]
	require.True(t, exists)
	assert.Equal(t, "udp", udpStream.transport)
	manager.streamsMu.RUnlock()
}

// TestFFmpegManager_TransportChangeViaStopStart tests the transport change mechanism
// that SyncWithConfig uses: Stop the old stream, verify removal, Start with new transport.
// This simulates the critical path of transport change detection without requiring
// configuration mocking.
func TestFFmpegManager_TransportChangeViaStopStart(t *testing.T) {
	// Do not use t.Parallel() - this test may indirectly access global soundLevelProcessors map

	manager := NewFFmpegManager()
	defer manager.Shutdown()

	audioChan := make(chan UnifiedAudioData, 100)
	defer close(audioChan)

	testURL := "rtsp://test.local/stream_change_transport"

	// Step 1: Start stream with TCP transport
	err := manager.StartStream(testURL, "tcp", audioChan)
	require.NoError(t, err, "should start stream with TCP")

	// Verify stream exists with TCP transport
	manager.streamsMu.RLock()
	stream, exists := manager.streams[testURL]
	require.True(t, exists, "stream should exist after start")
	assert.Equal(t, "tcp", stream.transport, "should have TCP transport")
	originalStreamPtr := stream
	manager.streamsMu.RUnlock()

	time.Sleep(50 * time.Millisecond)

	// Step 2: Stop the stream (simulating SyncWithConfig detecting transport change)
	err = manager.StopStream(testURL)
	require.NoError(t, err, "should stop stream successfully")

	// Step 3: Verify stream is fully removed from manager
	// This is the critical verification that SyncWithConfig performs
	manager.streamsMu.RLock()
	_, stillExists := manager.streams[testURL]
	assert.False(t, stillExists, "stream should be removed after StopStream")
	manager.streamsMu.RUnlock()

	// Step 4: Start stream with new UDP transport (simulating transport change)
	err = manager.StartStream(testURL, "udp", audioChan)
	require.NoError(t, err, "should start stream with UDP")

	time.Sleep(50 * time.Millisecond)

	// Step 5: Verify new stream exists with UDP transport
	manager.streamsMu.RLock()
	newStream, exists := manager.streams[testURL]
	require.True(t, exists, "new stream should exist")
	assert.Equal(t, "udp", newStream.transport, "new stream should have UDP transport")
	assert.NotSame(t, originalStreamPtr, newStream, "should be a different stream instance")
	manager.streamsMu.RUnlock()

	// Step 6: Verify the new stream's state is correct
	state := newStream.GetProcessState()
	// State can be StateIdle, StateStarting, or StateRunning depending on timing
	assert.Contains(t, []ProcessState{StateIdle, StateStarting, StateRunning}, state,
		"new stream should be in valid startup state")
}

// TestFFmpegManager_StopStreamRemovesFromMap verifies that StopStream properly
// removes streams from the manager's map, which is critical for transport changes
// to work correctly.
func TestFFmpegManager_StopStreamRemovesFromMap(t *testing.T) {
	// Do not use t.Parallel() - this test may indirectly access global soundLevelProcessors map

	manager := NewFFmpegManager()
	defer manager.Shutdown()

	audioChan := make(chan UnifiedAudioData, 100)
	defer close(audioChan)

	testURL := "rtsp://test.local/stream_removal_test"

	// Start a stream
	err := manager.StartStream(testURL, "tcp", audioChan)
	require.NoError(t, err)

	// Verify it exists
	manager.streamsMu.RLock()
	_, exists := manager.streams[testURL]
	require.True(t, exists, "stream should exist before stop")
	streamCount := len(manager.streams)
	manager.streamsMu.RUnlock()
	assert.Equal(t, 1, streamCount, "should have exactly 1 stream")

	// Stop the stream
	err = manager.StopStream(testURL)
	require.NoError(t, err, "StopStream should succeed")

	// Verify it's removed immediately (no race condition)
	manager.streamsMu.RLock()
	_, exists = manager.streams[testURL]
	assert.False(t, exists, "stream should be removed immediately after StopStream")
	streamCount = len(manager.streams)
	manager.streamsMu.RUnlock()
	assert.Equal(t, 0, streamCount, "streams map should be empty")

	// Verify we can start a new stream with the same URL (no lingering state)
	err = manager.StartStream(testURL, "udp", audioChan)
	require.NoError(t, err, "should be able to start new stream with same URL")

	manager.streamsMu.RLock()
	newStream, exists := manager.streams[testURL]
	require.True(t, exists, "new stream should exist")
	assert.Equal(t, "udp", newStream.transport, "new stream should have different transport")
	manager.streamsMu.RUnlock()
}

// Note: Full integration testing of SyncWithConfig with actual configuration changes
// is not included here because SyncWithConfig directly reads from the global
// conf.Setting() singleton, which would require complex test setup with temporary
// configuration files. The tests above cover the critical path that SyncWithConfig
// uses for transport changes:
// 1. Detect transport mismatch (tested via TransportFieldAccessible)
// 2. Stop old stream (tested via StopStreamRemovesFromMap)
// 3. Verify removal (tested via TransportChangeViaStopStart)
// 4. Start new stream with new transport (tested via TransportChangeViaStopStart)
// 5. Verify new stream state (tested via TransportChangeViaStopStart)
