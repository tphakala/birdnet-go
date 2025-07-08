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
	t.Parallel()

	manager := NewFFmpegManager()
	defer manager.Shutdown()

	audioChan := make(chan UnifiedAudioData, 10)
	url := "rtsp://test.example.com/stream"
	transport := "tcp"

	// Test starting a stream
	err := manager.StartStream(url, transport, audioChan)
	assert.NoError(t, err)

	// Test that we can't start the same stream twice
	err = manager.StartStream(url, transport, audioChan)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "stream already exists")

	// Verify stream is active
	activeStreams := manager.GetActiveStreams()
	assert.Contains(t, activeStreams, url)

	// Test stopping the stream
	err = manager.StopStream(url)
	assert.NoError(t, err)

	// Test that we can't stop a non-existent stream
	err = manager.StopStream(url)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no stream found")

	// Verify stream is no longer active
	activeStreams = manager.GetActiveStreams()
	assert.NotContains(t, activeStreams, url)
}

func TestFFmpegManager_MultipleStreams(t *testing.T) {
	t.Parallel()

	manager := NewFFmpegManager()
	defer manager.Shutdown()

	audioChan := make(chan UnifiedAudioData, 10)
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
	assert.NoError(t, err)

	// Verify correct streams remain
	activeStreams = manager.GetActiveStreams()
	assert.Len(t, activeStreams, 2)
	assert.Contains(t, activeStreams, urls[0])
	assert.NotContains(t, activeStreams, urls[1])
	assert.Contains(t, activeStreams, urls[2])
}

func TestFFmpegManager_RestartStream(t *testing.T) {
	t.Parallel()

	manager := NewFFmpegManager()
	defer manager.Shutdown()

	audioChan := make(chan UnifiedAudioData, 10)
	url := "rtsp://test.example.com/stream"

	// Start a stream
	err := manager.StartStream(url, "tcp", audioChan)
	require.NoError(t, err)

	// Test restarting the stream
	err = manager.RestartStream(url)
	assert.NoError(t, err)

	// Stream should still be active
	activeStreams := manager.GetActiveStreams()
	assert.Contains(t, activeStreams, url)

	// Test restarting non-existent stream
	err = manager.RestartStream("rtsp://nonexistent.example.com/stream")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no stream found")
}

func TestFFmpegManager_HealthCheck(t *testing.T) {
	t.Parallel()

	manager := NewFFmpegManager()
	defer manager.Shutdown()

	audioChan := make(chan UnifiedAudioData, 10)
	url := "rtsp://test.example.com/stream"

	// Start a stream
	err := manager.StartStream(url, "tcp", audioChan)
	require.NoError(t, err)

	// Give stream a moment to initialize
	time.Sleep(100 * time.Millisecond)

	// Check health
	health := manager.HealthCheck()
	assert.Len(t, health, 1)
	
	streamHealth, exists := health[url]
	assert.True(t, exists)
	assert.True(t, streamHealth.IsHealthy)
	assert.WithinDuration(t, time.Now(), streamHealth.LastDataReceived, 2*time.Second)
}

func TestFFmpegManager_Shutdown(t *testing.T) {
	t.Parallel()

	manager := NewFFmpegManager()
	audioChan := make(chan UnifiedAudioData, 10)

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
	assert.Len(t, activeStreams, 0)

	// Note: Starting new streams after shutdown might succeed
	// The manager doesn't prevent new streams after shutdown in the current implementation
	// This is a design choice to allow reuse of the manager
}

func TestFFmpegManager_ConcurrentOperations(t *testing.T) {
	t.Parallel()

	manager := NewFFmpegManager()
	defer manager.Shutdown()

	audioChan := make(chan UnifiedAudioData, 100)
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
	t.Parallel()

	manager := NewFFmpegManager()
	defer manager.Shutdown()

	// Start monitoring with short interval for testing
	manager.StartMonitoring(100 * time.Millisecond)

	audioChan := make(chan UnifiedAudioData, 10)
	url := "rtsp://test.example.com/stream"

	// Start a stream
	err := manager.StartStream(url, "tcp", audioChan)
	require.NoError(t, err)

	// Wait for monitoring to run at least once
	time.Sleep(200 * time.Millisecond)

	// Stream should still be healthy
	health := manager.HealthCheck()
	assert.True(t, health[url].IsHealthy)
}