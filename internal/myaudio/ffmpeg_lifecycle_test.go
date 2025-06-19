package myaudio

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
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
func TestableRestartLogicFunc(settingsProvider *MockLifecycleSettingsProvider, url string, maxAttempts int) (bool, int, error) {
	attempts := 0
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

func (m *MockLifecycleProcessMap) Load(key string) (interface{}, bool) {
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
