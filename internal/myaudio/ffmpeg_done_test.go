package myaudio

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFFmpegStream_DoneChannelClosed tests that the done channel is properly closed when the stream stops
func TestFFmpegStream_DoneChannelClosed(t *testing.T) {
	t.Parallel()

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)

	// Start the stream
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		stream.Run(ctx)
		close(done)
	}()

	// Give the stream time to start
	time.Sleep(100 * time.Millisecond)

	// Stop the stream
	stream.Stop()

	// The done channel should be closed within a reasonable time
	select {
	case <-stream.doneChan:
		// Success - done channel was closed
	case <-time.After(2 * time.Second):
		t.Fatal("Done channel was not closed within timeout")
	}

	// Verify the Run function also exited
	select {
	case <-done:
		// Success - Run function exited
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Run function did not exit after done channel closed")
	}
}

// TestStopStreamAndWait_UsesDoneChannel tests that StopStreamAndWait uses the done channel efficiently
func TestStopStreamAndWait_UsesDoneChannel(t *testing.T) {
	// Create a manager
	manager := NewFFmpegManager()

	// Add a test stream
	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	
	err := manager.StartStream("rtsp://test.example.com/stream", "tcp", audioChan)
	require.NoError(t, err)

	// Start timing
	start := time.Now()

	// Stop the stream and wait
	err = manager.StopStreamAndWait("rtsp://test.example.com/stream", 5*time.Second)
	require.NoError(t, err)

	// Should complete quickly (not polling every 10ms)
	elapsed := time.Since(start)
	assert.Less(t, elapsed, 1*time.Second, "StopStreamAndWait took too long, might be polling instead of using done channel")
}

// TestStopStreamAndWait_Timeout tests that StopStreamAndWait properly times out
func TestStopStreamAndWait_Timeout(t *testing.T) {
	t.Parallel()

	// Create a manager
	manager := NewFFmpegManager()

	// Create a stream that won't stop (by not running it)
	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)
	
	// Manually add the stream to the manager without starting Run
	manager.streamsMu.Lock()
	manager.streams["rtsp://test.example.com/stream"] = stream
	manager.streamsMu.Unlock()

	// Try to stop with a short timeout
	err := manager.StopStreamAndWait("rtsp://test.example.com/stream", 500*time.Millisecond)
	
	// Should timeout
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timeout")
}

// TestDoneChannel_MultipleStops tests that multiple calls to Stop don't panic
func TestDoneChannel_MultipleStops(t *testing.T) {
	t.Parallel()

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)

	// Start the stream
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go stream.Run(ctx)

	// Give the stream time to start
	time.Sleep(100 * time.Millisecond)

	// Stop multiple times - should not panic
	stream.Stop()
	stream.Stop()
	stream.Stop()

	// The done channel should still be closed only once
	select {
	case <-stream.doneChan:
		// Success - done channel was closed
	case <-time.After(1 * time.Second):
		t.Fatal("Done channel was not closed")
	}
}