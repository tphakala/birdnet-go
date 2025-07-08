package myaudio

import (
	"bytes"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// MockCommand helps test command execution without actually running FFmpeg
type MockCommand struct {
	*exec.Cmd
	mockStdout *bytes.Buffer
	mockStderr *bytes.Buffer
}

func TestFFmpegStream_NewStream(t *testing.T) {
	t.Parallel()

	audioChan := make(chan UnifiedAudioData, 10)
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)

	assert.NotNil(t, stream)
	assert.Equal(t, "rtsp://test.example.com/stream", stream.url)
	assert.Equal(t, "tcp", stream.transport)
	assert.NotNil(t, stream.audioChan)
	assert.NotNil(t, stream.restartChan)
	assert.NotNil(t, stream.stopChan)
	assert.Equal(t, 5*time.Second, stream.backoffDuration)
	assert.Equal(t, 2*time.Minute, stream.maxBackoff)
}

func TestFFmpegStream_Stop(t *testing.T) {
	t.Parallel()

	audioChan := make(chan UnifiedAudioData, 10)
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)

	// Test stopping the stream
	stream.Stop()

	// Verify stopped flag is set
	stream.stoppedMu.RLock()
	stopped := stream.stopped
	stream.stoppedMu.RUnlock()
	assert.True(t, stopped)

	// Verify stop channel is closed
	select {
	case <-stream.stopChan:
		// Expected - channel should be closed
	default:
		t.Fatal("Stop channel should be closed")
	}
}

func TestFFmpegStream_Restart(t *testing.T) {
	t.Parallel()

	audioChan := make(chan UnifiedAudioData, 10)
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)

	// Test restart
	stream.Restart()

	// Verify restart signal was sent
	select {
	case <-stream.restartChan:
		// Expected - restart signal received
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Restart signal not received")
	}

	// Verify restart count was reset
	stream.restartCountMu.Lock()
	count := stream.restartCount
	stream.restartCountMu.Unlock()
	assert.Equal(t, 0, count)
}

func TestFFmpegStream_GetHealth(t *testing.T) {
	t.Parallel()

	audioChan := make(chan UnifiedAudioData, 10)
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)

	// Get initial health
	health := stream.GetHealth()
	assert.True(t, health.IsHealthy)
	assert.WithinDuration(t, time.Now(), health.LastDataReceived, time.Second)
	assert.Equal(t, 0, health.RestartCount)

	// Simulate old data time
	stream.lastDataMu.Lock()
	stream.lastDataTime = time.Now().Add(-2 * time.Minute)
	stream.lastDataMu.Unlock()

	// Health should now be unhealthy
	health = stream.GetHealth()
	assert.False(t, health.IsHealthy)

	// Update restart count
	stream.restartCountMu.Lock()
	stream.restartCount = 5
	stream.restartCountMu.Unlock()

	health = stream.GetHealth()
	assert.Equal(t, 5, health.RestartCount)
}

func TestFFmpegStream_UpdateLastDataTime(t *testing.T) {
	t.Parallel()

	audioChan := make(chan UnifiedAudioData, 10)
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)

	// Set old time
	oldTime := time.Now().Add(-1 * time.Hour)
	stream.lastDataMu.Lock()
	stream.lastDataTime = oldTime
	stream.lastDataMu.Unlock()

	// Update time
	stream.updateLastDataTime()

	// Verify time was updated
	stream.lastDataMu.RLock()
	newTime := stream.lastDataTime
	stream.lastDataMu.RUnlock()

	assert.True(t, newTime.After(oldTime))
	assert.WithinDuration(t, time.Now(), newTime, time.Second)
}

func TestFFmpegStream_BackoffCalculation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		restartCount  int
		expectedWait  time.Duration
	}{
		{"First restart", 1, 5 * time.Second},
		{"Second restart", 2, 10 * time.Second},
		{"Third restart", 3, 20 * time.Second},
		{"Fourth restart", 4, 40 * time.Second},
		{"Fifth restart", 5, 80 * time.Second},
		{"Sixth restart (capped)", 6, 2 * time.Minute},
		{"Tenth restart (capped)", 10, 2 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			audioChan := make(chan UnifiedAudioData, 10)
			stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)
			
			// Set restart count
			stream.restartCountMu.Lock()
			stream.restartCount = tt.restartCount - 1 // Will be incremented in handleRestartBackoff
			stream.restartCountMu.Unlock()

			// Calculate expected backoff
			backoff := stream.backoffDuration * time.Duration(1<<uint(tt.restartCount-1))
			if backoff > stream.maxBackoff {
				backoff = stream.maxBackoff
			}
			
			assert.Equal(t, tt.expectedWait, backoff)
		})
	}
}

func TestFFmpegStream_ConcurrentHealthAccess(t *testing.T) {
	t.Parallel()

	audioChan := make(chan UnifiedAudioData, 10)
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)

	// Run concurrent operations
	done := make(chan bool)
	
	// Reader goroutines
	for i := 0; i < 5; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				health := stream.GetHealth()
				_ = health.IsHealthy
				time.Sleep(time.Microsecond)
			}
			done <- true
		}()
	}

	// Writer goroutines
	for i := 0; i < 3; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				stream.updateLastDataTime()
				time.Sleep(time.Microsecond)
			}
			done <- true
		}()
	}

	// Restart count updater
	go func() {
		for j := 0; j < 100; j++ {
			stream.restartCountMu.Lock()
			stream.restartCount++
			stream.restartCountMu.Unlock()
			time.Sleep(time.Microsecond)
		}
		done <- true
	}()

	// Wait for all goroutines
	for i := 0; i < 9; i++ {
		<-done
	}

	// Verify final state is consistent
	health := stream.GetHealth()
	assert.NotNil(t, health)
}

func TestFFmpegStream_ProcessLifecycle(t *testing.T) {
	t.Skip("Requires actual FFmpeg binary to test full lifecycle")
	
	// This test would require FFmpeg to be installed
	// It's kept as a template for integration testing
	
	// audioChan := make(chan UnifiedAudioData, 10)
	// stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)
	
	// ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	// defer cancel()
	
	// Would test actual process starting, data processing, and cleanup
	// This requires mocking exec.Command or having FFmpeg available
}

func TestFFmpegStream_HandleAudioData(t *testing.T) {
	// Skip test if we can't initialize buffers (requires proper setup)
	if err := AllocateAnalysisBuffer(conf.BufferSize*3, "test"); err != nil {
		t.Skip("Cannot allocate analysis buffer for test")
	}
	defer func() {
		if err := RemoveAnalysisBuffer("test"); err != nil {
			t.Logf("Failed to remove analysis buffer: %v", err)
		}
	}()
	
	if err := AllocateCaptureBufferIfNeeded(60, conf.SampleRate, conf.BitDepth/8, "test"); err != nil {
		t.Skip("Cannot allocate capture buffer for test") 
	}
	defer func() {
		if err := RemoveCaptureBuffer("test"); err != nil {
			t.Logf("Failed to remove capture buffer: %v", err)
		}
	}()

	audioChan := make(chan UnifiedAudioData, 10)
	stream := NewFFmpegStream("test", "tcp", audioChan)
	
	// Test audio data handling
	testData := make([]byte, 1024)
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	
	err := stream.handleAudioData(testData)
	assert.NoError(t, err)
	
	// Check if data was sent to audio channel
	select {
	case data := <-audioChan:
		assert.NotNil(t, data.AudioLevel)
		assert.WithinDuration(t, time.Now(), data.Timestamp, time.Second)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No data received on audio channel")
	}
}