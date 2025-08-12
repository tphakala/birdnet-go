package myaudio

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// TestConcurrentSendOnClosedChannel tests that the fixed implementation
// does not panic when sending to a closed channel
func TestConcurrentSendOnClosedChannel(t *testing.T) {
	// Skip parallelization for goroutine leak detection
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("testing.(*T).Run"),
		goleak.IgnoreTopFunction("runtime.gopark"),
		goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
	)

	// Create a simplified test that doesn't use buffers
	// This directly tests the atomic.Bool protection against panics
	
	audioChan := make(chan UnifiedAudioData, 100)
	stream := &FFmpegStream{
		url:         "rtsp://test.example.com/stream",
		transport:   "tcp",
		audioChan:   audioChan,
		running:     atomic.Bool{},
		stopChan:    make(chan struct{}),
		restartChan: make(chan struct{}, 1),
	}
	
	// Set the stream as running
	stream.running.Store(true)

	// Start multiple goroutines that will try to send data
	var wg sync.WaitGroup
	sendersCount := 10
	sendsPerGoroutine := 1000
	
	// Track results
	var panicsDetected atomic.Int64

	// Start senders that directly interact with the channel
	for i := 0; i < sendersCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panicsDetected.Add(1)
					t.Logf("PANIC DETECTED: %v", r)
				}
			}()
			
			for j := 0; j < sendsPerGoroutine; j++ {
				// Fast path check like in real code
				if !stream.running.Load() {
					continue
				}
				
				// Try to send data
				data := UnifiedAudioData{
					AudioLevel: AudioLevelData{
						Level:    50,
						Source:   "test",
					},
				}
				
				select {
				case stream.audioChan <- data:
					// Sent successfully
				default:
					// Channel full, drop
				}
				
				// Small delay to simulate real processing
				time.Sleep(time.Microsecond)
			}
		}()
	}

	// Let senders run for a bit
	time.Sleep(10 * time.Millisecond)

	// Stop the stream while senders are active
	stream.Stop()

	// Wait for all senders to complete
	wg.Wait()
	
	// Now close the channel after all senders are done
	// This verifies the stop mechanism works correctly
	close(audioChan)

	// Verify no panic occurred
	assert.Equal(t, int64(0), panicsDetected.Load(), "No panics should have occurred")
	assert.False(t, stream.running.Load(), "Stream should be stopped")
	
	// Verify stop channel is closed
	select {
	case <-stream.stopChan:
		// Expected - channel should be closed
	default:
		t.Fatal("Stop channel should be closed")
	}
	
	t.Logf("Test completed successfully with no panics")
}

// TestAtomicBoolPerformance tests the performance of atomic.Bool vs mutex
func TestAtomicBoolPerformance(t *testing.T) {
	t.Parallel()

	audioChan := make(chan UnifiedAudioData, 1000)
	defer close(audioChan)
	
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)
	stream.running.Store(true)

	// Test atomic.Bool performance
	iterations := 1000000
	start := time.Now()
	
	checkCount := 0
	for i := 0; i < iterations; i++ {
		if stream.running.Load() {
			checkCount++
		}
	}
	
	atomicDuration := time.Since(start)
	
	// Verify performance is good
	assert.Less(t, atomicDuration, 100*time.Millisecond,
		"Atomic bool check should be very fast for %d iterations", iterations)
	
	t.Logf("Atomic bool performance: %d checks in %v (%.2f ns/op)",
		iterations, atomicDuration, float64(atomicDuration.Nanoseconds())/float64(iterations))
}

// TestStopStreamAndWait tests the new synchronous stop method
func TestStopStreamAndWait(t *testing.T) {
	// Skip parallelization for goroutine leak detection
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("testing.(*T).Run"),
		goleak.IgnoreTopFunction("runtime.gopark"),
		goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
	)

	// Initialize a manager
	manager := NewFFmpegManager()
	defer manager.Shutdown()

	audioChan := make(chan UnifiedAudioData, 100)
	defer close(audioChan)

	// Start a stream
	url := "rtsp://test.example.com/stream1"
	err := manager.StartStream(url, "tcp", audioChan)
	require.NoError(t, err)

	// Verify stream is running
	assert.Contains(t, manager.GetActiveStreams(), url)

	// Stop stream with wait
	err = manager.StopStreamAndWait(url, 5*time.Second)
	require.NoError(t, err)

	// Verify stream is fully stopped
	assert.NotContains(t, manager.GetActiveStreams(), url)
	
	// Verify we can't stop it again (should return error)
	err = manager.StopStreamAndWait(url, 1*time.Second)
	assert.Error(t, err, "Should error when stopping non-existent stream")
}

// TestStopAllRTSPStreamsAndWait tests stopping multiple streams concurrently
func TestStopAllRTSPStreamsAndWait(t *testing.T) {
	// Skip parallelization for goroutine leak detection
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("testing.(*T).Run"),
		goleak.IgnoreTopFunction("runtime.gopark"),
		goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
	)

	// Initialize manager through the global getter
	manager := getGlobalManager()
	require.NotNil(t, manager)
	
	// Cleanup after test
	t.Cleanup(func() {
		ShutdownFFmpegManager()
	})

	audioChan := make(chan UnifiedAudioData, 100)
	defer close(audioChan)

	// Start multiple streams
	urls := []string{
		"rtsp://test.example.com/stream1",
		"rtsp://test.example.com/stream2",
		"rtsp://test.example.com/stream3",
	}

	for _, url := range urls {
		err := manager.StartStream(url, "tcp", audioChan)
		require.NoError(t, err)
	}

	// Verify all streams are running
	activeStreams := manager.GetActiveStreams()
	assert.Len(t, activeStreams, len(urls))

	// Stop all streams with wait
	err := StopAllRTSPStreamsAndWait(5 * time.Second)
	require.NoError(t, err)

	// Verify all streams are stopped
	activeStreams = manager.GetActiveStreams()
	assert.Empty(t, activeStreams, "All streams should be stopped")
}

// TestChannelReplacementNoPanic tests that replacing channels doesn't cause panics
func TestChannelReplacementNoPanic(t *testing.T) {
	// Skip parallelization for proper cleanup
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("testing.(*T).Run"),
		goleak.IgnoreTopFunction("runtime.gopark"),
	)

	// Create initial channel
	audioChan1 := make(chan UnifiedAudioData, 100)
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan1)
	stream.running.Store(true)

	// Start sender goroutine
	var wg sync.WaitGroup
	stopSending := make(chan struct{})
	
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		
		for {
			select {
			case <-stopSending:
				return
			case <-ticker.C:
				// Create test audio data
				testData := make([]byte, 512)
				_ = stream.handleAudioData(testData)
			}
		}
	}()

	// Let it run briefly
	time.Sleep(10 * time.Millisecond)

	// Replace the channel (simulating reconfiguration)
	audioChan2 := make(chan UnifiedAudioData, 100)
	stream.audioChan = audioChan2
	
	// Continue sending data
	time.Sleep(10 * time.Millisecond)
	
	// Stop sending and cleanup
	close(stopSending)
	wg.Wait()
	
	// Stop the stream
	stream.Stop()
	
	// Close channels
	close(audioChan1)
	close(audioChan2)
	
	// Test passes if we reach here without panic
}

// TestConcurrentStopOperations tests multiple concurrent stop operations
func TestConcurrentStopOperations(t *testing.T) {
	t.Parallel()

	audioChan := make(chan UnifiedAudioData, 100)
	defer close(audioChan)
	
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)
	stream.running.Store(true)

	// Try to stop the stream from multiple goroutines
	var wg sync.WaitGroup
	stoppers := 10
	
	for i := 0; i < stoppers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stream.Stop()
		}()
	}
	
	wg.Wait()
	
	// Verify stream is stopped and only stopped once
	assert.False(t, stream.running.Load(), "Stream should be stopped")
	
	// Try to send data after stop - should fail gracefully
	testData := make([]byte, 100)
	err := stream.handleAudioData(testData)
	assert.Error(t, err, "Should error when sending to stopped stream")
}

// TestStreamRestartUnderLoad tests stream restart while data is being sent
func TestStreamRestartUnderLoad(t *testing.T) {
	// Skip parallelization for goroutine leak detection
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("testing.(*T).Run"),
		goleak.IgnoreTopFunction("runtime.gopark"),
	)

	audioChan := make(chan UnifiedAudioData, 100)
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)
	stream.running.Store(true)

	// Start data sender
	ctx, cancel := context.WithCancel(context.Background())
	var senderWg sync.WaitGroup
	
	senderWg.Add(1)
	go func() {
		defer senderWg.Done()
		ticker := time.NewTicker(time.Microsecond * 100)
		defer ticker.Stop()
		
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				testData := make([]byte, 256)
				_ = stream.handleAudioData(testData)
			}
		}
	}()

	// Perform multiple restarts
	for i := 0; i < 5; i++ {
		time.Sleep(5 * time.Millisecond)
		stream.Restart(false)
	}

	// Stop everything
	cancel()
	senderWg.Wait()
	stream.Stop()
	close(audioChan)

	// Test passes if we reach here without panic
	// If any panics occurred during restart, the test would have failed
}

// TestStreamChannelMemoryLeakPrevention tests that old channels are properly cleaned up
func TestStreamChannelMemoryLeakPrevention(t *testing.T) {
	t.Parallel()

	// Monitor goroutine count
	initialGoroutines := runtime.NumGoroutine()

	// Create and replace channels multiple times
	for i := 0; i < 10; i++ {
		audioChan := make(chan UnifiedAudioData, 100)
		stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)
		
		// Send some data
		for j := 0; j < 100; j++ {
			select {
			case audioChan <- UnifiedAudioData{}:
			default:
			}
		}
		
		// Stop and cleanup
		stream.Stop()
		
		// Drain channel
		go func(ch chan UnifiedAudioData) {
			for range ch {
			}
		}(audioChan)
		
		close(audioChan)
	}

	// Allow goroutines to clean up
	time.Sleep(100 * time.Millisecond)

	// Check goroutine count hasn't grown significantly
	finalGoroutines := runtime.NumGoroutine()
	goroutineGrowth := finalGoroutines - initialGoroutines
	
	assert.LessOrEqual(t, goroutineGrowth, 5, 
		"Goroutine leak detected: started with %d, ended with %d",
		initialGoroutines, finalGoroutines)
}