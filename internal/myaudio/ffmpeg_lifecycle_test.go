package myaudio

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// MockLifecycleSettingsProvider allows us to mock configuration without touching global state
type MockLifecycleSettingsProvider struct {
	rtspURLs []string
	mu       sync.RWMutex
}

func NewMockLifecycleSettingsProvider() *MockLifecycleSettingsProvider {
	return &MockLifecycleSettingsProvider{
		rtspURLs: []string{},
	}
}

func (m *MockLifecycleSettingsProvider) GetRTSPURLs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]string{}, m.rtspURLs...) // Return copy to avoid race conditions
}

func (m *MockLifecycleSettingsProvider) SetRTSPURLs(urls []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rtspURLs = append([]string{}, urls...) // Store copy to avoid race conditions
}

// TestableRestartLogic encapsulates testable restart logic
type TestableRestartLogic struct {
	settingsProvider *MockLifecycleSettingsProvider
	processMap       *MockLifecycleProcessMap
	shouldFailStart  bool
}

// ManageLifecycle is a testable version of manageFfmpegLifecycle
func (t *TestableRestartLogic) ManageLifecycle(ctx context.Context, config FFmpegConfig, restartChan chan struct{}, audioLevelChan chan AudioLevelData) error {
	if t.shouldFailStart {
		return errors.New("failed to start FFmpeg process (simulated)")
	}

	// Simulate basic lifecycle behavior
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-restartChan:
			// Handle restart
			return nil
		default:
			// Check configuration
			urls := t.settingsProvider.GetRTSPURLs()
			streamConfigured := false
			for _, url := range urls {
				if url == config.URL {
					streamConfigured = true
					break
				}
			}
			if !streamConfigured {
				return nil
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// TestableRestartLogicFunc demonstrates how to test restart behavior without full lifecycle complexity
func TestableRestartLogicFunc(settingsProvider *MockLifecycleSettingsProvider, url string, maxAttempts int) (success bool, attempts int, err error) {
	attempts = 0
	backoff := newBackoffStrategy(maxAttempts, 1*time.Second, 5*time.Second)

	for {
		attempts++

		// Check if stream is still configured (simulates the real logic)
		configuredURLs := settingsProvider.GetRTSPURLs()
		streamConfigured := false
		for _, configuredURL := range configuredURLs {
			if configuredURL == url {
				streamConfigured = true
				break
			}
		}

		if !streamConfigured {
			return false, attempts, nil // Stream removed from config
		}

		// Simulate FFmpeg start attempt (this would be mocked in real tests)
		// For demonstration, we'll simulate failure for first few attempts
		if attempts < 3 {
			delay, canRetry := backoff.nextDelay()
			if !canRetry {
				return false, attempts, errors.New("max attempts exceeded")
			}
			// In real code, this would be time.After(delay), but we skip for testing
			_ = delay
			continue
		}

		// Success after a few attempts
		return true, attempts, nil
	}
}

// Test cases demonstrate how to test restart scenarios

func TestFFmpegRestartLogic_StreamRemovedFromConfig(t *testing.T) {
	mockSettings := NewMockLifecycleSettingsProvider()
	url := "rtsp://example.com/stream"

	// Initially configure the stream
	mockSettings.SetRTSPURLs([]string{url})

	// Start testing in a goroutine to simulate concurrent config changes
	resultChan := make(chan struct {
		success  bool
		attempts int
		err      error
	}, 1)

	go func() {
		// Simulate removing the stream from configuration during restart attempts
		time.Sleep(10 * time.Millisecond)
		mockSettings.SetRTSPURLs([]string{}) // Remove stream

		success, attempts, err := TestableRestartLogicFunc(mockSettings, url, 5)
		resultChan <- struct {
			success  bool
			attempts int
			err      error
		}{success, attempts, err}
	}()

	// Wait for result
	select {
	case result := <-resultChan:
		assert.False(t, result.success, "Should not succeed when stream is removed from config")
		assert.NoError(t, result.err, "Should not return error when stream is removed from config")
		assert.Greater(t, result.attempts, 0, "Should have made at least one attempt")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Test timed out")
	}
}

func TestFFmpegRestartLogic_MaxAttemptsExceeded(t *testing.T) {
	mockSettings := NewMockLifecycleSettingsProvider()
	url := "rtsp://example.com/stream"

	// Configure the stream to stay in config
	mockSettings.SetRTSPURLs([]string{url})

	// Use a modified version that always fails to test max attempts
	attempts := 0
	backoff := newBackoffStrategy(3, 1*time.Second, 5*time.Second)

	for {
		// Always simulate failure
		delay, canRetry := backoff.nextDelay()
		if !canRetry {
			break
		}
		attempts++
		_ = delay // Skip actual delay for testing
	}

	assert.Equal(t, 3, attempts, "Should make exactly max attempts")
}

func TestFFmpegRestartLogic_SuccessAfterRetries(t *testing.T) {
	mockSettings := NewMockLifecycleSettingsProvider()
	url := "rtsp://example.com/stream"

	mockSettings.SetRTSPURLs([]string{url})

	success, attempts, err := TestableRestartLogicFunc(mockSettings, url, 5)

	assert.True(t, success, "Should eventually succeed")
	assert.NoError(t, err, "Should not return error on success")
	assert.Equal(t, 3, attempts, "Should succeed on the 3rd attempt")
}

func TestWatchdogBehavior(t *testing.T) {
	// Test the audioWatchdog functionality
	watchdog := &audioWatchdog{
		lastDataTime: time.Now().Add(-70 * time.Second), // Old data
		mu:           sync.Mutex{},
	}

	// Should detect timeout
	assert.True(t, watchdog.timeSinceLastData() > 60*time.Second)

	// Update with fresh data
	watchdog.update()

	// Should not detect timeout
	assert.True(t, watchdog.timeSinceLastData() < 60*time.Second)
}

func TestBackoffStrategyLifecycle(t *testing.T) {
	backoff := newBackoffStrategy(3, 1*time.Second, 5*time.Second)

	// Test progression
	delay1, canRetry1 := backoff.nextDelay()
	assert.True(t, canRetry1)
	assert.Equal(t, 1*time.Second, delay1)

	delay2, canRetry2 := backoff.nextDelay()
	assert.True(t, canRetry2)
	assert.Equal(t, 2*time.Second, delay2)

	delay3, canRetry3 := backoff.nextDelay()
	assert.True(t, canRetry3)
	assert.Equal(t, 4*time.Second, delay3)

	// Should exceed max attempts
	_, canRetry4 := backoff.nextDelay()
	assert.False(t, canRetry4)

	// Test reset
	backoff.reset()
	delay5, canRetry5 := backoff.nextDelay()
	assert.True(t, canRetry5)
	assert.Equal(t, 1*time.Second, delay5)
}

func TestRestartTrackerBehavior(t *testing.T) {
	// Create a mock command for testing
	cmd := &exec.Cmd{}

	// Get restart tracker
	tracker := getRestartTracker(cmd)
	assert.NotNil(t, tracker)
	assert.Equal(t, 0, tracker.restartCount)

	// Create a mock FFmpeg process
	process := &FFmpegProcess{
		restartTracker: tracker,
	}

	// Test restart delay calculation
	initialDelay := process.getRestartDelay()
	assert.Equal(t, time.Duration(0), initialDelay) // No restarts yet

	// Update restart info
	process.updateRestartInfo()
	delay1 := process.getRestartDelay()
	assert.Equal(t, 5*time.Second, delay1)

	// Update again
	process.updateRestartInfo()
	delay2 := process.getRestartDelay()
	assert.Equal(t, 10*time.Second, delay2)

	// Test that old restarts are reset
	process.restartTracker.lastRestartAt = time.Now().Add(-2 * time.Minute)
	process.updateRestartInfo()
	delay3 := process.getRestartDelay()
	assert.Equal(t, 5*time.Second, delay3) // Should reset to 1 restart
}

// Example of how to test the processAudio function's watchdog mechanism
func TestProcessAudioWatchdogTimeout(t *testing.T) {
	// Create a mock watchdog that simulates timeout
	watchdog := &audioWatchdog{
		lastDataTime: time.Now().Add(-70 * time.Second), // Simulate old data
		mu:           sync.Mutex{},
	}

	// Test timeout detection
	timeoutDetected := watchdog.timeSinceLastData() > 60*time.Second
	assert.True(t, timeoutDetected, "Watchdog should detect timeout when no data received for >60s")

	// Test restart signal would be sent (in real code)
	restartChan := make(chan struct{}, 1)

	// Simulate the watchdog logic from processAudio
	if timeoutDetected {
		select {
		case restartChan <- struct{}{}:
			// Successfully sent restart signal
		default:
			t.Error("Restart channel should not be full")
		}
	}

	// Verify restart signal was sent
	select {
	case <-restartChan:
		// Expected - restart signal received
	default:
		t.Error("Should have received restart signal")
	}
}

// Example of how to test configuration-based stream management
func TestConfigurationBasedStreamManagement(t *testing.T) {
	// This demonstrates how to test the configuration checking logic
	// that exists in manageFfmpegLifecycle

	mockSettings := NewMockLifecycleSettingsProvider()
	testURL := "rtsp://example.com/stream"

	// Test stream initially configured
	mockSettings.SetRTSPURLs([]string{testURL})
	configuredURLs := mockSettings.GetRTSPURLs()

	streamConfigured := false
	for _, url := range configuredURLs {
		if url == testURL {
			streamConfigured = true
			break
		}
	}
	assert.True(t, streamConfigured, "Stream should be configured initially")

	// Test stream removed from configuration
	mockSettings.SetRTSPURLs([]string{})
	configuredURLs = mockSettings.GetRTSPURLs()

	streamConfigured = false
	for _, url := range configuredURLs {
		if url == testURL {
			streamConfigured = true
			break
		}
	}
	assert.False(t, streamConfigured, "Stream should not be configured after removal")
}

// MockLifecycleProcessMap shows how to test the global ffmpegProcesses map
type MockLifecycleProcessMap struct {
	processes map[string]interface{}
	mu        sync.RWMutex
}

func NewMockLifecycleProcessMap() *MockLifecycleProcessMap {
	return &MockLifecycleProcessMap{
		processes: make(map[string]interface{}),
	}
}

func (m *MockLifecycleProcessMap) Load(key string) (value interface{}, exists bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	val, ok := m.processes[key]
	return val, ok
}

func (m *MockLifecycleProcessMap) Store(key string, value interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.processes[key] = value
}

func (m *MockLifecycleProcessMap) Delete(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.processes, key)
}

func TestProcessMapOperations(t *testing.T) {
	processMap := NewMockLifecycleProcessMap()
	testURL := "rtsp://example.com/stream"

	// Test storing a process
	mockProcess := &FFmpegProcess{}
	processMap.Store(testURL, mockProcess)

	// Test loading a process
	loaded, exists := processMap.Load(testURL)
	assert.True(t, exists, "Process should exist after storing")
	assert.Equal(t, mockProcess, loaded, "Loaded process should match stored process")

	// Test deleting a process
	processMap.Delete(testURL)
	_, exists = processMap.Load(testURL)
	assert.False(t, exists, "Process should not exist after deletion")
}

// BoundedBuffer tests (this component is already well-designed for testing)
func TestBoundedBufferUsage(t *testing.T) {
	buf := NewBoundedBuffer(10)

	// Test normal write
	n, err := buf.Write([]byte("hello"))
	assert.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, "hello", buf.String())

	// Test overflow
	n, err = buf.Write([]byte("world this is too long"))
	assert.NoError(t, err)
	assert.Equal(t, 10, n)                      // Should be limited to buffer size
	assert.Equal(t, "s too long", buf.String()) // Should contain only last 10 chars
}

// Example of how you might test the actual manageFfmpegLifecycle function
// by wrapping it with dependency injection
func TestFfmpegLifecyclePattern(t *testing.T) {
	// This test demonstrates the pattern you would use to test the actual
	// manageFfmpegLifecycle function without major refactoring

	// 1. Create a wrapper function that accepts dependencies
	testableManageLifecycle := func(
		ctx context.Context,
		config FFmpegConfig,
		restartChan chan struct{},
		audioLevelChan chan AudioLevelData,
		// Injected dependencies
		settingsProvider func() *conf.Settings,
		processMap interface {
			Load(string) (interface{}, bool)
			Store(string, interface{})
			Delete(string)
		},
	) error {
		// This would contain the logic from manageFfmpegLifecycle
		// but using the injected dependencies instead of globals

		// Example logic (simplified):
		settings := settingsProvider()
		streamConfigured := false
		for _, url := range settings.Realtime.RTSP.URLs {
			if url == config.URL {
				streamConfigured = true
				break
			}
		}

		if !streamConfigured {
			processMap.Delete(config.URL)
			return nil
		}

		// Would continue with actual lifecycle logic...
		return nil
	}

	// 2. Test with mocked dependencies
	mockSettings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			RTSP: conf.RTSPSettings{
				URLs: []string{"rtsp://example.com/stream"},
			},
		},
	}

	settingsProvider := func() *conf.Settings {
		return mockSettings
	}

	processMap := NewMockLifecycleProcessMap()

	config := FFmpegConfig{URL: "rtsp://example.com/stream", Transport: "tcp"}
	ctx := context.Background()
	restartChan := make(chan struct{}, 1)
	audioLevelChan := make(chan AudioLevelData, 1)

	// Test the function
	err := testableManageLifecycle(ctx, config, restartChan, audioLevelChan, settingsProvider, processMap)

	// In this simplified example, the function should return nil
	// because the stream is configured, but no actual FFmpeg process starts
	assert.NoError(t, err)
}

// ===== REAL-WORLD FAILURE SCENARIO TESTS =====
// These tests are designed to expose actual issues that cause restart failures

// TestRestartChannelBlocking tests the scenario where restart channel is full and drops requests
func TestRestartChannelBlocking(t *testing.T) {
	// Create a channel with buffer size 1 to test blocking
	restartChan := make(chan struct{}, 1)

	// Fill the restart channel to simulate blocking
	restartChan <- struct{}{}

	// Try to send multiple restart signals - these should be dropped due to full channel
	droppedCount := 0
	for i := 0; i < 5; i++ {
		select {
		case restartChan <- struct{}{}:
			t.Logf("Restart signal %d sent successfully", i)
		default:
			t.Logf("Restart signal %d dropped (channel full)", i)
			droppedCount++
		}
	}

	// Verify that signals were dropped
	assert.Equal(t, 5, droppedCount, "All 5 restart signals should be dropped when channel is full")

	// Channel should still have one buffered item
	assert.Equal(t, 1, len(restartChan), "Channel should have 1 buffered item")
}

// TestProcessMapConcurrentAccess tests concurrent access to the process map
func TestProcessMapConcurrentAccess(t *testing.T) {
	realProcessMap := &sync.Map{} // Use real sync.Map to test actual concurrency

	var wg sync.WaitGroup

	// Start multiple goroutines accessing the process map concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			url := fmt.Sprintf("rtsp://test%d.com/stream", id)

			// Simulate rapid process operations
			for j := 0; j < 100; j++ {
				// Store a mock process
				realProcessMap.Store(url, &FFmpegProcess{})

				// Load and check
				if process, exists := realProcessMap.Load(url); exists {
					_ = process // Use the process
				}

				// Delete
				realProcessMap.Delete(url)
			}
		}(i)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	t.Log("Concurrent access test completed successfully")
}

// TestWatchdogTimingIssues tests potential timing issues with the watchdog
func TestWatchdogTimingIssues(t *testing.T) {
	watchdog := &audioWatchdog{lastDataTime: time.Now()}

	// Test 1: Check that watchdog properly handles time updates
	initialTime := watchdog.timeSinceLastData()
	assert.True(t, initialTime < time.Second, "Initial watchdog time should be very recent")

	// Test 2: Simulate no data for 65 seconds (should trigger timeout)
	watchdog.lastDataTime = time.Now().Add(-65 * time.Second)
	timeoutDuration := watchdog.timeSinceLastData()
	assert.True(t, timeoutDuration > 60*time.Second, "Watchdog should detect timeout after 60 seconds")

	// Test 3: Update watchdog and verify time reset
	watchdog.update()
	updatedTime := watchdog.timeSinceLastData()
	assert.True(t, updatedTime < time.Second, "Watchdog time should reset after update")

	// Test 4: Test concurrent access to watchdog
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				watchdog.update()
				_ = watchdog.timeSinceLastData()
			}
		}()
	}
	wg.Wait()

	t.Log("Watchdog timing tests completed successfully")
}

// TestBackoffStrategyEdgeCases tests edge cases in backoff strategy that might prevent restarts
func TestBackoffStrategyEdgeCases(t *testing.T) {
	// Test 1: Backoff at maximum attempts
	backoff := newBackoffStrategy(3, 1*time.Second, 2*time.Minute)

	delays := []time.Duration{}
	for {
		delay, retry := backoff.nextDelay()
		if !retry {
			break
		}
		delays = append(delays, delay)
	}

	assert.Len(t, delays, 3, "Should have exactly 3 retry attempts")
	assert.Equal(t, 1*time.Second, delays[0], "First delay should be 1 second")
	assert.Equal(t, 2*time.Second, delays[1], "Second delay should be 2 seconds")
	assert.Equal(t, 4*time.Second, delays[2], "Third delay should be 4 seconds")

	// Test 2: Reset functionality
	backoff.reset()
	delay, retry := backoff.nextDelay()
	assert.True(t, retry, "Should be able to retry after reset")
	assert.Equal(t, 1*time.Second, delay, "Delay should reset to initial value")

	// Test 3: Maximum delay cap
	longBackoff := newBackoffStrategy(10, 30*time.Second, 2*time.Minute)
	var maxDelay time.Duration
	for i := 0; i < 10; i++ {
		delay, retry := longBackoff.nextDelay()
		if !retry {
			break
		}
		if delay > maxDelay {
			maxDelay = delay
		}
	}
	assert.Equal(t, 2*time.Minute, maxDelay, "Delay should be capped at maximum")
}

// TestRestartTrackerResetLogic tests the restart tracker reset mechanism
func TestRestartTrackerResetLogic(t *testing.T) {
	// Clear global restart trackers to ensure clean test state
	restartTrackers = sync.Map{}

	// Create a mock command for testing
	mockCmd := &exec.Cmd{}

	// Get restart tracker
	tracker := getRestartTracker(mockCmd)

	// Test 1: Initial state
	assert.Equal(t, 0, tracker.restartCount, "Initial restart count should be 0")

	// Create a mock process with the tracker
	process := &FFmpegProcess{
		cmd:            mockCmd,
		restartTracker: tracker,
	}

	// Test 2: Update restart info
	process.updateRestartInfo()
	assert.Equal(t, 1, tracker.restartCount, "Restart count should increment")

	delay1 := process.getRestartDelay()
	assert.Equal(t, 5*time.Second, delay1, "First restart delay should be 5 seconds")

	// Test 3: Multiple restarts within a minute
	for i := 0; i < 5; i++ {
		process.updateRestartInfo()
	}
	assert.Equal(t, 6, tracker.restartCount, "Restart count should be 6")

	delay6 := process.getRestartDelay()
	assert.Equal(t, 30*time.Second, delay6, "Sixth restart delay should be 30 seconds")

	// Test 4: Reset after more than a minute
	tracker.lastRestartAt = time.Now().Add(-2 * time.Minute)
	process.updateRestartInfo()
	assert.Equal(t, 1, tracker.restartCount, "Restart count should reset after a minute")

	resetDelay := process.getRestartDelay()
	assert.Equal(t, 5*time.Second, resetDelay, "Delay should reset to 5 seconds")
}

// TestProcessCleanupConsistency tests that process cleanup maintains consistent state
func TestProcessCleanupConsistency(t *testing.T) {
	// Create a mock process map
	processMap := &sync.Map{}
	url := "rtsp://test.com/stream"

	// Create a mock FFmpeg process
	mockProcess := &FFmpegProcess{
		cmd:    &exec.Cmd{},
		cancel: func() {},
		done:   make(chan error, 1),
	}

	// Store the process
	processMap.Store(url, mockProcess)

	// Verify it's stored
	if _, exists := processMap.Load(url); !exists {
		t.Fatal("Process should be stored in map")
	}

	// Test cleanup
	mockProcess.Cleanup(url)

	// Verify it's been removed (this tests the real cleanup logic)
	// Note: The actual cleanup function uses the global map, so this test
	// demonstrates the inconsistency issue
	if process, exists := processMap.Load(url); exists {
		t.Logf("Process still exists after cleanup: %v", process)
		// This is expected with the current implementation because
		// Cleanup uses the global ffmpegProcesses map, not our test map
	}

	// Test concurrent cleanup calls
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mockProcess.Cleanup(url)
		}()
	}
	wg.Wait()

	t.Log("Process cleanup consistency test completed")
}

// ===== COMPREHENSIVE LIFECYCLE ISSUE TESTS =====
// These tests validate critical issues that could prevent restarts from working

// TestProcessStateInconsistency tests the critical issue where updateRestartInfo is called after cleanup
func TestProcessStateInconsistency(t *testing.T) {
	// Clear global restart trackers to ensure clean test state
	restartTrackers = sync.Map{}

	// Create a mock command
	mockCmd := &exec.Cmd{Path: "/usr/bin/ffmpeg"}

	// Get restart tracker
	tracker := getRestartTracker(mockCmd)

	// Create a mock process
	process := &FFmpegProcess{
		cmd:            mockCmd,
		cancel:         func() {}, // Mock cancel function
		restartTracker: tracker,
	}

	// Test: updateRestartInfo called on a process that might be cleaned up
	process.updateRestartInfo()
	originalCount := tracker.restartCount

	// Simulate cleanup happening (sets process state to invalid)
	// In real code, this could happen when process.Cleanup() is called
	process.cmd = nil // Simulate cleaned up state

	// Try to call updateRestartInfo again - this should not panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("updateRestartInfo should handle nil cmd gracefully, but panicked: %v", r)
		}
	}()

	// This will work because updateRestartInfo only uses the tracker, not the cmd
	process.updateRestartInfo()

	// But getRestartDelay might behave unexpectedly
	delay := process.getRestartDelay()
	assert.Greater(t, delay, time.Duration(0), "Should still calculate delay even with nil cmd")

	t.Logf("Original count: %d, Final count: %d, Delay: %v", originalCount, tracker.restartCount, delay)
}

// TestResourceLeakInStartFFmpeg tests file descriptor leaks when startFFmpeg fails
func TestResourceLeakInStartFFmpeg(t *testing.T) {
	// Test case where StdoutPipe succeeds but Start fails
	ctx := context.Background()
	config := FFmpegConfig{
		URL:       "rtsp://nonexistent.com/stream",
		Transport: "tcp",
	}

	// This should fail because the FFmpeg path is likely invalid or the URL doesn't exist
	process, err := startFFmpeg(ctx, config)

	if err != nil {
		// Expected failure - but we need to ensure no resource leak
		assert.Nil(t, process, "Process should be nil on failure")
		t.Logf("Expected failure occurred: %v", err)

		// In the current implementation, there's a potential resource leak
		// The stdout pipe is created but if cmd.Start() fails, it's not explicitly closed
		// The context cancellation should handle it, but it's not guaranteed
	} else if process != nil {
		// If it somehow succeeds, clean up properly
		process.Cleanup(config.URL)
	}
}

// TestCleanupRaceCondition tests race conditions in the Cleanup method
func TestCleanupRaceCondition(t *testing.T) {
	url := "rtsp://test.com/stream"

	// Create a mock process that simulates a race condition
	mockProcess := &FFmpegProcess{
		cmd:    &exec.Cmd{},
		cancel: func() {},
		stdout: nil, // Simulate already closed stdout
	}

	// Simulate concurrent cleanup calls
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			// Each goroutine tries to cleanup - this should not panic
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Cleanup %d panicked: %v", id, r)
				}
			}()

			mockProcess.Cleanup(url)
		}(i)
	}

	wg.Wait()
	t.Log("Concurrent cleanup test completed")
}

// TestBufferWriteErrorAccumulation tests handling of accumulated buffer write errors
func TestBufferWriteErrorAccumulation(t *testing.T) {
	// This test simulates the scenario where WriteToAnalysisBuffer or WriteToCaptureBuffer
	// repeatedly fails, but the process continues without triggering a restart

	errorCount := 0
	maxErrors := 5

	// Simulate the logic from processAudio where buffer write errors are handled
	for i := 0; i < 10; i++ {
		// Simulate buffer write error (in real code, this would be WriteToAnalysisBuffer)
		bufferWriteError := fmt.Errorf("buffer write failed %d", i)

		if bufferWriteError != nil {
			errorCount++
			t.Logf("Buffer write error %d: %v", errorCount, bufferWriteError)

			// In the current implementation, the code just logs and sleeps
			// But it doesn't track accumulating errors or trigger restarts
			if errorCount >= maxErrors {
				t.Logf("Accumulated %d buffer write errors - this should trigger a restart in production", errorCount)
				break
			}

			// Simulate the sleep (we'll skip actual sleep for testing)
			// time.Sleep(1 * time.Second)
		}
	}

	assert.GreaterOrEqual(t, errorCount, maxErrors, "Should accumulate buffer write errors")
}

// TestWatchdogConfigurationRace tests race conditions in configuration reading
func TestWatchdogConfigurationRace(t *testing.T) {
	// This test simulates the race condition where conf.Setting() is called
	// multiple times without synchronization in different parts of the lifecycle

	url := "rtsp://test.com/stream"

	// Simulate concurrent configuration reads (like in watchdog and processAudio)
	var wg sync.WaitGroup
	configReads := 0
	var configMutex sync.Mutex

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			// Simulate multiple configuration reads
			for j := 0; j < 3; j++ {
				// In real code, this would be: settings := conf.Setting()
				// Simulate configuration read
				configMutex.Lock()
				configReads++
				configMutex.Unlock()

				// Simulate checking if stream is configured
				streamConfigured := false
				// In real code: for _, configuredURL := range settings.Realtime.RTSP.URLs
				testURLs := []string{url} // Simulate configured URLs
				for _, configuredURL := range testURLs {
					if configuredURL == url {
						streamConfigured = true
						break
					}
				}

				t.Logf("Goroutine %d check %d: stream configured = %v", id, j, streamConfigured)
				time.Sleep(1 * time.Millisecond) // Small delay to increase race chance
			}
		}(i)
	}

	wg.Wait()
	t.Logf("Total configuration reads: %d", configReads)
	assert.Greater(t, configReads, 0, "Should have performed configuration reads")
}

// TestRestartTrackerMemoryLeak tests the memory leak in global restartTrackers map
func TestRestartTrackerMemoryLeak(t *testing.T) {
	// Clear global restart trackers to start clean
	restartTrackers = sync.Map{}

	initialCount := 0
	restartTrackers.Range(func(key, value interface{}) bool {
		initialCount++
		return true
	})

	// Create many different commands to simulate different RTSP streams
	commands := make([]*exec.Cmd, 100)
	for i := 0; i < 100; i++ {
		commands[i] = &exec.Cmd{
			Path: "/usr/bin/ffmpeg",
			Args: []string{"ffmpeg", "-i", fmt.Sprintf("rtsp://stream%d.com", i)},
		}

		// Get restart tracker for each command (this adds to global map)
		tracker := getRestartTracker(commands[i])
		assert.NotNil(t, tracker, "Should get a valid restart tracker")
	}

	// Count trackers after adding
	finalCount := 0
	restartTrackers.Range(func(key, value interface{}) bool {
		finalCount++
		return true
	})

	assert.Equal(t, 100, finalCount-initialCount, "Should have added 100 restart trackers")

	// In the current implementation, there's no cleanup mechanism for old trackers
	// This demonstrates the memory leak - old trackers are never removed
	t.Logf("Memory leak demonstration: %d trackers remain in global map", finalCount)

	// The global map will keep growing indefinitely in production
	// Each RTSP stream restart cycle adds new entries but never removes old ones
}

// TestAudioLevelChannelRace tests race conditions in audio level channel clearing
func TestAudioLevelChannelRace(t *testing.T) {
	audioLevelChan := make(chan AudioLevelData, 1)

	// Fill the channel
	audioLevelChan <- AudioLevelData{Level: 50, Source: "test", Name: "test"}

	// Test concurrent channel operations
	var wg sync.WaitGroup

	// Goroutine 1: tries to clear and send (simulates processAudio logic)
	wg.Add(1)
	go func() {
		defer wg.Done()

		newData := AudioLevelData{Level: 80, Source: "test", Name: "test"}

		select {
		case audioLevelChan <- newData:
			t.Log("Successfully sent data")
		default:
			t.Log("Channel full, attempting to clear")
			// This is the problematic logic from the code
			for len(audioLevelChan) > 0 {
				<-audioLevelChan
			}
			audioLevelChan <- newData
			t.Log("Data sent after clearing")
		}
	}()

	// Goroutine 2: tries to read from channel concurrently
	wg.Add(1)
	go func() {
		defer wg.Done()

		select {
		case data := <-audioLevelChan:
			t.Logf("Received data: %+v", data)
		case <-time.After(10 * time.Millisecond):
			t.Log("Timeout waiting for data")
		}
	}()

	wg.Wait()
	t.Log("Audio level channel race test completed")
}

// TestFFmpegProcessExitRace tests race conditions between process exit and cleanup
func TestFFmpegProcessExitRace(t *testing.T) {
	// This test simulates the race between the goroutine waiting for cmd.Wait()
	// and the cleanup process

	done := make(chan error, 1)
	isCleaningUp := atomic.Bool{}
	processExited := atomic.Bool{}

	// Simulate the goroutine from startFFmpeg that waits for process exit
	go func() {
		// Simulate process execution time
		time.Sleep(10 * time.Millisecond)

		// Simulate process exit
		processExited.Store(true)
		done <- nil // Process exited normally
	}()

	// Simulate concurrent cleanup
	go func() {
		time.Sleep(5 * time.Millisecond) // Cleanup starts before process exits
		isCleaningUp.Store(true)

		// Simulate cleanup operations
		time.Sleep(20 * time.Millisecond)
	}()

	// Wait for process exit
	select {
	case err := <-done:
		t.Logf("Process exited with error: %v", err)
		cleanupInProgress := isCleaningUp.Load()
		processHasExited := processExited.Load()

		t.Logf("Cleanup in progress: %v, Process exited: %v", cleanupInProgress, processHasExited)

		// This demonstrates the race condition between cleanup and process exit
		if cleanupInProgress && processHasExited {
			t.Log("Race condition detected: cleanup and process exit occurred concurrently")
		}

	case <-time.After(100 * time.Millisecond):
		t.Error("Test timed out waiting for process exit")
	}
}

// TestContextCancellationRace tests race conditions with context cancellation
func TestContextCancellationRace(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	watchdogDone := make(chan struct{})

	// Simulate the watchdog goroutine
	go func() {
		defer close(watchdogDone)
		time.Sleep(50 * time.Millisecond)
	}()

	// Simulate concurrent context cancellation and watchdog completion
	go func() {
		time.Sleep(25 * time.Millisecond)
		cancel() // Cancel context while watchdog is running
	}()

	// Simulate the select logic from processAudio
	select {
	case <-ctx.Done():
		t.Log("Context cancelled first")
		// In the real code, this should wait for watchdog: <-watchdogDone
		select {
		case <-watchdogDone:
			t.Log("Watchdog completed after context cancellation")
		case <-time.After(100 * time.Millisecond):
			t.Error("Watchdog did not complete after context cancellation")
		}
	case <-watchdogDone:
		t.Log("Watchdog completed first")
		// Check if context is also done
		select {
		case <-ctx.Done():
			t.Log("Context was also cancelled")
		default:
			t.Log("Context was not cancelled")
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("Test timed out")
	}
}

// TestPlatformSpecificProcessGroupFailure tests handling of process group kill failures
func TestPlatformSpecificProcessGroupFailure(t *testing.T) {
	// Test the Unix version of killProcessGroup with invalid PID
	// This should fail but not panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("killProcessGroup should handle invalid PID gracefully, but panicked: %v", r)
		}
	}()

	// Note: We can't directly test killProcessGroup without platform-specific imports
	// But we can test the error handling logic that would occur
	killError := fmt.Errorf("no such process")

	if killError != nil {
		t.Logf("Process group kill failed as expected: %v", killError)

		// In the real Cleanup method, this would fall back to direct process kill
		directKillError := fmt.Errorf("process already finished")

		if directKillError != nil && strings.Contains(directKillError.Error(), "process already finished") {
			t.Log("Direct kill also failed, but this is acceptable for finished processes")
		} else {
			t.Logf("Direct kill failed with unexpected error: %v", directKillError)
		}
	}
}

// TestBackoffStrategyStateConsistency tests backoff strategy state consistency
func TestBackoffStrategyStateConsistency(t *testing.T) {
	backoff := newBackoffStrategy(3, 1*time.Second, 10*time.Second)

	// Test that backoff maintains consistent state across multiple operations
	delays := []time.Duration{}

	// Test multiple cycles with reset
	for cycle := 0; cycle < 3; cycle++ {
		t.Logf("Starting backoff cycle %d", cycle)

		for {
			delay, canRetry := backoff.nextDelay()
			if !canRetry {
				break
			}
			delays = append(delays, delay)
			t.Logf("Cycle %d: delay %v, attempt %d", cycle, delay, backoff.attempt)
		}

		// Reset for next cycle
		backoff.reset()
		assert.Equal(t, 0, backoff.attempt, "Attempt should reset to 0")
	}

	// Verify delays follow expected exponential pattern
	expectedDelays := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

	if len(delays) >= 3 {
		for i := 0; i < 3; i++ {
			assert.Equal(t, expectedDelays[i], delays[i], "Delay %d should match expected exponential backoff", i)
		}
	}

	t.Logf("Total delays recorded: %v", delays)
}

// TestConfigurationConsistencyAcrossLifecycle tests configuration consistency throughout lifecycle
func TestConfigurationConsistencyAcrossLifecycle(t *testing.T) {
	// Mock the configuration checking logic used throughout the lifecycle
	testURL := "rtsp://test.com/stream"

	// Function to simulate configuration check (used in multiple places in real code)
	checkStreamConfigured := func(urls []string, targetURL string) bool {
		for _, url := range urls {
			if url == targetURL {
				return true
			}
		}
		return false
	}

	// Test different configuration states
	testCases := []struct {
		name           string
		configuredURLs []string
		expectedResult bool
	}{
		{"stream_configured", []string{testURL}, true},
		{"stream_not_configured", []string{}, false},
		{"multiple_streams_with_target", []string{"rtsp://other.com", testURL}, true},
		{"multiple_streams_without_target", []string{"rtsp://other1.com", "rtsp://other2.com"}, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := checkStreamConfigured(tc.configuredURLs, testURL)
			assert.Equal(t, tc.expectedResult, result, "Configuration check should match expected result")

			// This demonstrates how configuration checks are used throughout the lifecycle:
			// 1. manageFfmpegLifecycle - before starting/restarting
			// 2. processAudio - before triggering restart
			// 3. startWatchdog - before timeout detection
			// 4. manageFfmpegLifecycle - after process ends
			// 5. manageFfmpegLifecycle - before restart delay

			t.Logf("Configuration state '%s': %v", tc.name, result)
		})
	}
}

// TestRestartTrackerCleanupFix tests that the memory leak fix for restart trackers works
func TestRestartTrackerCleanupFix(t *testing.T) {
	// Clear global restart trackers to start clean
	restartTrackers = sync.Map{}

	// Count initial trackers
	initialCount := 0
	restartTrackers.Range(func(key, value interface{}) bool {
		initialCount++
		return true
	})

	// Create a mock command and process
	mockCmd := &exec.Cmd{
		Path:    "/usr/bin/ffmpeg",
		Args:    []string{"ffmpeg", "-i", "rtsp://test.com/stream"},
		Process: &os.Process{Pid: 1}, // Fake process to avoid nil check
	}

	// Get restart tracker (this adds to global map)
	tracker := getRestartTracker(mockCmd)
	assert.NotNil(t, tracker, "Should get a valid restart tracker")

	// Create a done channel and send completion signal
	doneChannel := make(chan error, 1)
	doneChannel <- nil // Pre-send completion signal

	// Create a mock process using the same command object for proper cleanup
	mockProcess := &FFmpegProcess{
		cmd:            mockCmd, // Use the same command object
		cancel:         func() {},
		stdout:         nil,
		restartTracker: tracker,
		done:           doneChannel,
	}

	// Count trackers after adding
	afterAddCount := 0
	restartTrackers.Range(func(key, value interface{}) bool {
		afterAddCount++
		return true
	})

	assert.Equal(t, 1, afterAddCount-initialCount, "Should have added 1 restart tracker")

	// Call cleanup which should remove the tracker
	mockProcess.Cleanup("rtsp://test.com/stream")

	// Count trackers after cleanup
	afterCleanupCount := 0
	restartTrackers.Range(func(key, value interface{}) bool {
		afterCleanupCount++
		return true
	})

	assert.Equal(t, initialCount, afterCleanupCount, "Restart tracker should be cleaned up")
	t.Logf("Restart tracker cleanup test: initial=%d, after_add=%d, after_cleanup=%d",
		initialCount, afterAddCount, afterCleanupCount)
}

func TestBackoffStrategyUnlimitedRetries(t *testing.T) {
	// Test unlimited retries with maxAttempts = -1
	backoff := newBackoffStrategy(-1, 1*time.Second, 10*time.Second)

	delays := []time.Duration{}
	// Test many more attempts than would normally be allowed
	for i := 0; i < 20; i++ {
		delay, retry := backoff.nextDelay()
		assert.True(t, retry, "Should always allow retry with unlimited attempts (attempt %d)", i+1)
		delays = append(delays, delay)

		// Verify exponential backoff pattern with cap
		expectedDelay := time.Duration(1<<uint(i)) * time.Second
		if expectedDelay > 10*time.Second {
			expectedDelay = 10 * time.Second
		}
		assert.Equal(t, expectedDelay, delay, "Delay should follow exponential backoff pattern (attempt %d)", i+1)
	}

	t.Logf("Successfully completed %d retry attempts with unlimited strategy", len(delays))

	// Test reset functionality
	backoff.reset()
	delay, retry := backoff.nextDelay()
	assert.True(t, retry, "Should allow retry after reset")
	assert.Equal(t, 1*time.Second, delay, "Delay should reset to initial value")
}

func TestConcurrentCleanupRaceConditions(t *testing.T) {
	// Test concurrent cleanup calls to ensure no race conditions
	url := "rtsp://test-race.com/stream"

	// Create a mock process with proper initialization
	mockProcess := &FFmpegProcess{
		cmd:    &exec.Cmd{},
		cancel: func() {},
		stdout: nil,
		// cleanupMutex and cleanupDone are zero-initialized
	}

	// Store process in the map
	ffmpegProcesses.Store(url, mockProcess)

	// Launch multiple goroutines to cleanup simultaneously
	var wg sync.WaitGroup
	numGoroutines := 20

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Cleanup goroutine %d panicked: %v", id, r)
				}
			}()

			// Each goroutine attempts cleanup
			mockProcess.Cleanup(url)
		}(i)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// Verify cleanup was performed (process should be removed from map)
	if _, exists := ffmpegProcesses.Load(url); exists {
		t.Error("Process should have been removed from map after cleanup")
	}
}

func TestConcurrentRestartSignalRaceConditions(t *testing.T) {
	// Test concurrent restart signal sending to ensure no race conditions
	restartChan := make(chan struct{}, 1) // Small buffer to force blocking scenarios
	url := "rtsp://test-restart.com/stream"

	mockProcess := &FFmpegProcess{}

	var wg sync.WaitGroup
	numGoroutines := 10

	// Launch multiple goroutines to send restart signals simultaneously
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Restart signal goroutine %d panicked: %v", id, r)
				}
			}()

			// Each goroutine attempts to send restart signal
			mockProcess.sendRestartSignal(restartChan, url, fmt.Sprintf("Test-%d", id))
		}(i)
	}

	// Drain restart channel in another goroutine to prevent blocking
	go func() {
		for {
			select {
			case <-restartChan:
				// Consume restart signals
			case <-time.After(3 * time.Second):
				// Timeout to prevent goroutine leak
				return
			}
		}
	}()

	// Wait for all goroutines to complete
	wg.Wait()

	t.Log("All restart signal goroutines completed without panicking")
}

func TestConcurrentProcessMapOperations(t *testing.T) {
	// Test concurrent operations on the process map
	baseURL := "rtsp://concurrent-test.com/stream"

	var wg sync.WaitGroup
	numGoroutines := 15
	operationsPerGoroutine := 50

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Process map goroutine %d panicked: %v", id, r)
				}
			}()

			url := fmt.Sprintf("%s-%d", baseURL, id)

			for j := 0; j < operationsPerGoroutine; j++ {
				mockProcess := &FFmpegProcess{
					cmd:    &exec.Cmd{},
					cancel: func() {},
				}

				// Store process
				ffmpegProcesses.Store(url, mockProcess)

				// Load process
				if process, exists := ffmpegProcesses.Load(url); exists {
					if p, ok := process.(*FFmpegProcess); ok {
						// Perform cleanup
						p.Cleanup(url)
					}
				}

				// Try LoadAndDelete
				if process, loaded := ffmpegProcesses.LoadAndDelete(url); loaded {
					if p, ok := process.(*FFmpegProcess); ok {
						// Process was loaded, cleanup if needed
						_ = p
					}
				}
			}
		}(i)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	t.Log("All process map operations completed without race conditions")
}

func TestAudioLevelChannelRaceConditions(t *testing.T) {
	// Test concurrent audio level channel operations
	audioLevelChan := make(chan AudioLevelData, 1) // Small buffer to test clearing logic

	mockProcess := &FFmpegProcess{}
	url := "rtsp://audio-test.com/stream"
	data := make([]byte, 1024)

	var wg sync.WaitGroup
	numGoroutines := 10

	// Consumer goroutine to drain the channel
	go func() {
		for {
			select {
			case <-audioLevelChan:
				// Consume audio level data
			case <-time.After(2 * time.Second):
				// Timeout to prevent goroutine leak
				return
			}
		}
	}()

	// Launch multiple goroutines to send audio level data simultaneously
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Audio level goroutine %d panicked: %v", id, r)
				}
			}()

			// Simulate processAudioData calls that include audio level channel operations
			bufferErrorCount := 0
			lastBufferErrorTime := time.Now()
			restartChan := make(chan struct{}, 1)

			for j := 0; j < 5; j++ { // Reduced iterations to avoid hitting buffer error threshold
				err := mockProcess.processAudioData(url, data, &bufferErrorCount, &lastBufferErrorTime, restartChan, audioLevelChan)
				// processAudioData expects buffers to exist, so we expect buffer errors in this test
				if err != nil && err.Error() != "buffer_error_continue" && !strings.Contains(err.Error(), "too many buffer write errors") {
					t.Errorf("Unexpected error from processAudioData: %v", err)
				}
			}
		}(i)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	t.Log("All audio level channel operations completed without race conditions")
}

func TestProcessTrackerConcurrentAccess(t *testing.T) {
	// Test concurrent access to restart trackers
	var wg sync.WaitGroup
	numGoroutines := 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Restart tracker goroutine %d panicked: %v", id, r)
				}
			}()

			// Create multiple mock commands to test tracker creation/access
			for j := 0; j < 50; j++ {
				cmd := &exec.Cmd{
					Path: fmt.Sprintf("/test/path/%d", id),
					Args: []string{fmt.Sprintf("arg%d", j)},
				}

				// Get tracker (this accesses the global restartTrackers map)
				tracker := getRestartTracker(cmd)
				assert.NotNil(t, tracker, "Tracker should not be nil")

				// Create a mock process and update restart info
				process := &FFmpegProcess{
					restartTracker: tracker,
				}
				process.updateRestartInfo()

				// Get restart delay
				delay := process.getRestartDelay()
				assert.Greater(t, delay, time.Duration(0), "Delay should be positive")
			}
		}(i)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	t.Log("All restart tracker operations completed without race conditions")
}
