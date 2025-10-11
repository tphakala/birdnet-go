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
