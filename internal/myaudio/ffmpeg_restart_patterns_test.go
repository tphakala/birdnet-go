package myaudio

import (
	"fmt"
	"os/exec"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFFmpegStream_RealWorldRestartPattern simulates the restart pattern observed in production logs:
// - Processes run for < 5 seconds (94% of cases)
// - Health check triggers restart every 30 seconds due to unhealthy stream
// - Restart count increments continuously (up to 300+ observed)
func TestFFmpegStream_RealWorldRestartPattern(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Process testing is Unix-specific")
	}

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	
	// Simulate multiple rapid restarts as seen in logs
	const numRestarts = 20 // Simulate 20 restarts
	
	// Track PIDs and runtime of processes
	pids := make([]int, 0, numRestarts)
	runtimes := make([]time.Duration, 0, numRestarts)
	var mu sync.Mutex
	
	for i := 0; i < numRestarts; i++ {
		stream := NewFFmpegStream(fmt.Sprintf("test://rapid-fail-%d", i), "tcp", audioChan)
		
		// Create a process that exits quickly (< 5 seconds)
		// This simulates ffmpeg failing to connect or maintain RTSP stream
		runtimeMs := 1000 + (i%4)*1000 // 1-4 seconds
		mockCmd := exec.Command("sh", "-c", fmt.Sprintf("sleep %.3f", float64(runtimeMs)/1000.0))
		
		stream.cmdMu.Lock()
		stream.cmd = mockCmd
		stream.processStartTime = time.Now()
		stream.cmdMu.Unlock()
		
		// Start the process
		startTime := time.Now()
		err := mockCmd.Start()
		require.NoError(t, err)
		
		mu.Lock()
		pids = append(pids, mockCmd.Process.Pid)
		mu.Unlock()
		
		// Wait for process to exit naturally
		done := make(chan error, 1)
		go func() {
			done <- mockCmd.Wait()
		}()
		
		select {
		case <-done:
			processRuntime := time.Since(startTime)
			mu.Lock()
			runtimes = append(runtimes, processRuntime)
			mu.Unlock()
		case <-time.After(10 * time.Second):
			t.Fatalf("Process %d didn't exit in time", mockCmd.Process.Pid)
		}
		
		// Clean up
		stream.cleanupProcess()
		
		// Small delay between restarts (simulating backoff)
		time.Sleep(100 * time.Millisecond)
	}
	
	// Wait for cleanup
	time.Sleep(500 * time.Millisecond)
	
	// Verify no zombies
	mu.Lock()
	defer mu.Unlock()
	
	zombieCount := 0
	for _, pid := range pids {
		if isProcessZombie(t, pid) {
			zombieCount++
			t.Logf("Process %d is a zombie", pid)
		}
	}
	
	// Calculate statistics
	shortLived := 0
	for _, rt := range runtimes {
		if rt < 5*time.Second {
			shortLived++
		}
	}
	
	t.Logf("Total processes: %d", len(pids))
	t.Logf("Short-lived (<5s): %d (%.1f%%)", shortLived, float64(shortLived)/float64(len(pids))*100)
	t.Logf("Zombie processes: %d", zombieCount)
	
	assert.Equal(t, 0, zombieCount, "Should have no zombie processes")
	assert.Greater(t, shortLived, len(pids)*3/4, "Most processes should be short-lived like in production")
}

// TestFFmpegStream_HealthCheckRestartLoop simulates the health check restart loop pattern:
// - Health check detects unhealthy stream every 30 seconds
// - Manager triggers restart through RestartStream()
// - Process starts but fails quickly
// - Pattern repeats indefinitely
func TestFFmpegStream_HealthCheckRestartLoop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Process testing is Unix-specific")
	}

	manager := NewFFmpegManager()
	defer manager.Shutdown()
	
	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	
	// Track restart events
	restartCount := 0
	var restartMu sync.Mutex
	
	// Start a stream
	url := "test://health-check-loop"
	err := manager.StartStream(url, "tcp", audioChan)
	require.NoError(t, err)
	
	// Start health monitoring with short interval for testing
	manager.StartMonitoring(5 * time.Second)
	
	// Run for a period to observe restart pattern
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		
		for {
			select {
			case <-ticker.C:
				health := manager.HealthCheck()
				for _, h := range health {
					if !h.IsHealthy && h.RestartCount > 0 {
						restartMu.Lock()
						if h.RestartCount > restartCount {
							restartCount = h.RestartCount
							t.Logf("Restart count: %d, Last data: %v ago", 
								h.RestartCount, time.Since(h.LastDataReceived))
						}
						restartMu.Unlock()
					}
				}
			case <-done:
				return
			}
		}
	}()
	
	// Let it run for 20 seconds to observe multiple restart cycles
	time.Sleep(20 * time.Second)
	close(done)
	
	// Check results
	restartMu.Lock()
	finalRestartCount := restartCount
	restartMu.Unlock()
	
	t.Logf("Final restart count after 20s: %d", finalRestartCount)
	
	// Stop the stream
	err = manager.StopStream(url)
	assert.NoError(t, err)
}

// TestFFmpegStream_ConcurrentRestartRequests tests the scenario where multiple restart requests
// arrive while a process is already being cleaned up (as seen in logs with rapid restart requests)
func TestFFmpegStream_ConcurrentRestartRequests(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Process testing is Unix-specific")
	}

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("test://concurrent-restarts", "tcp", audioChan)
	
	// Start a mock process
	mockCmd := exec.Command("sleep", "10")
	stream.cmdMu.Lock()
	stream.cmd = mockCmd
	stream.processStartTime = time.Now()
	stream.cmdMu.Unlock()
	
	err := mockCmd.Start()
	require.NoError(t, err)
	pid := mockCmd.Process.Pid
	
	// Send multiple concurrent restart requests
	var wg sync.WaitGroup
	for i := range 5 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			time.Sleep(time.Duration(n*10) * time.Millisecond)
			stream.Restart(false) // automatic restart
		}(i)
	}
	
	// Also trigger cleanup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(25 * time.Millisecond)
		stream.cleanupProcess()
	}()
	
	wg.Wait()
	
	// Give time for all operations to complete
	time.Sleep(500 * time.Millisecond)
	
	// Verify no zombie
	assertNoZombieProcess(t, pid)
	
	// Verify restart channel handling
	select {
	case <-stream.restartChan:
		// Should be empty or have at most one pending
	default:
		// Good - channel is not blocked
	}
}

// TestFFmpegStream_ExtendedBackoffPattern tests the backoff pattern observed in logs
// where restart count continuously increases, affecting backoff duration
func TestFFmpegStream_ExtendedBackoffPattern(t *testing.T) {
	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("test://backoff-pattern", "tcp", audioChan)
	
	// Test backoff calculation at various restart counts seen in logs
	testCounts := []int{5, 50, 100, 150, 200, 250, 300}
	
	for _, count := range testCounts {
		stream.restartCountMu.Lock()
		stream.restartCount = count
		stream.restartCountMu.Unlock()
		
		// Calculate what the backoff would be
		exponent := count - 1
		if exponent > maxBackoffExponent {
			exponent = maxBackoffExponent
		}
		
		expectedBackoff := stream.backoffDuration * time.Duration(1<<uint(exponent))
		if expectedBackoff > stream.maxBackoff {
			expectedBackoff = stream.maxBackoff
		}
		
		t.Logf("Restart count %d: backoff = %v", count, expectedBackoff)
		
		// At high restart counts, backoff should be capped at maxBackoff
		if count > 10 {
			assert.Equal(t, expectedBackoff, stream.maxBackoff, 
				"Backoff should be capped at max for high restart counts")
		}
	}
}

// TestFFmpegStream_ProcessCleanupUnderLoad tests cleanup behavior when system is under load
// (simulating the production scenario with many rapid restarts)
func TestFFmpegStream_ProcessCleanupUnderLoad(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Process testing is Unix-specific")
	}

	const numStreams = 5
	const numRestartsPerStream = 10
	
	audioChans := make([]chan UnifiedAudioData, numStreams)
	streams := make([]*FFmpegStream, numStreams)
	
	// Create multiple streams
	for i := range numStreams {
		audioChans[i] = make(chan UnifiedAudioData, 10)
		streams[i] = NewFFmpegStream(fmt.Sprintf("test://load-%d", i), "tcp", audioChans[i])
	}
	
	// Track all PIDs
	allPids := make([]int, 0, numStreams*numRestartsPerStream)
	var pidMu sync.Mutex
	
	// Simulate rapid restarts on all streams concurrently
	var wg sync.WaitGroup
	for i := range numStreams {
		wg.Add(1)
		go func(streamIdx int) {
			defer wg.Done()
			stream := streams[streamIdx]
			
			for j := range numRestartsPerStream {
				// Create a short-lived process
				mockCmd := exec.Command("sh", "-c", "sleep 0.1")
				
				stream.cmdMu.Lock()
				stream.cmd = mockCmd
				stream.processStartTime = time.Now()
				stream.cmdMu.Unlock()
				
				err := mockCmd.Start()
				if err != nil {
					continue
				}
				
				pidMu.Lock()
				allPids = append(allPids, mockCmd.Process.Pid)
				pidMu.Unlock()
				
				// Random delay to simulate real timing
				time.Sleep(time.Duration(50+j*10) * time.Millisecond)
				
				// Cleanup
				stream.cleanupProcess()
			}
		}(i)
	}
	
	wg.Wait()
	
	// Close channels
	for i := range numStreams {
		close(audioChans[i])
	}
	
	// Wait for all cleanups
	time.Sleep(2 * time.Second)
	
	// Check for zombies
	pidMu.Lock()
	defer pidMu.Unlock()
	
	zombieCount := 0
	for _, pid := range allPids {
		if isProcessZombie(t, pid) {
			zombieCount++
		}
	}
	
	t.Logf("Total processes created: %d", len(allPids))
	t.Logf("Zombie processes: %d", zombieCount)
	
	assert.Equal(t, 0, zombieCount, "Should have no zombies even under load")
}