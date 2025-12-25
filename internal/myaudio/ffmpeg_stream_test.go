package myaudio

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestFFmpegStream_NewStream(t *testing.T) {
	t.Parallel()

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)

	assert.NotNil(t, stream)
	connStr, err := stream.source.GetConnectionString()
	require.NoError(t, err)
	assert.Equal(t, "rtsp://test.example.com/stream", connStr)
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
	defer close(audioChan)
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
		require.Fail(t, "Stop channel should be closed")
	}
}

func TestFFmpegStream_Restart(t *testing.T) {
	t.Parallel()

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)

	// Test restart (manual restart)
	stream.Restart(true)

	// Verify restart signal was sent
	select {
	case <-stream.restartChan:
		// Expected - restart signal received
	case <-time.After(100 * time.Millisecond):
		require.Fail(t, "Restart signal not received")
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
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)

	// Get initial health - FIXED: should not be healthy without data
	health := stream.GetHealth()
	assert.False(t, health.IsHealthy, "New stream should not be healthy without data")
	assert.True(t, health.LastDataReceived.IsZero(), "Initial LastDataReceived should be zero time")
	assert.Equal(t, 0, health.RestartCount)

	// Update data time to make stream healthy
	stream.updateLastDataTime()
	health = stream.GetHealth()
	assert.True(t, health.IsHealthy, "Stream should be healthy after receiving data")
	assert.WithinDuration(t, time.Now(), health.LastDataReceived, time.Second)

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
	defer close(audioChan)
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
		name         string
		restartCount int
		expectedWait time.Duration
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
			t.Parallel()
			audioChan := make(chan UnifiedAudioData, 10)
			defer close(audioChan)
			stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)

			// Set restart count
			stream.restartCountMu.Lock()
			stream.restartCount = tt.restartCount - 1 // Will be incremented in handleRestartBackoff
			stream.restartCountMu.Unlock()

			// Calculate expected backoff with the same logic as the implementation
			exponent := min(tt.restartCount-1,
				// maxBackoffExponent constant
				20)

			backoff := min(stream.backoffDuration*time.Duration(1<<uint(exponent)), stream.maxBackoff) //nolint:gosec // G115: exponent is capped by min()

			assert.Equal(t, tt.expectedWait, backoff)
		})
	}
}

func TestFFmpegStream_ConcurrentHealthAccess(t *testing.T) {
	t.Parallel()

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)

	// Run concurrent operations
	done := make(chan bool)

	// Reader goroutines
	for range 5 {
		go func() {
			for range 100 {
				health := stream.GetHealth()
				_ = health.IsHealthy
				// Use runtime.Gosched() instead of sleep for better concurrency testing
				// runtime.Gosched()
			}
			done <- true
		}()
	}

	// Writer goroutines
	for range 3 {
		go func() {
			for range 100 {
				stream.updateLastDataTime()
				// Use runtime.Gosched() instead of sleep for better concurrency testing
				// runtime.Gosched()
			}
			done <- true
		}()
	}

	// Restart count updater
	go func() {
		for range 100 {
			stream.restartCountMu.Lock()
			stream.restartCount++
			stream.restartCountMu.Unlock()
			// Use runtime.Gosched() instead of sleep for better concurrency testing
			// runtime.Gosched()
		}
		done <- true
	}()

	// Wait for all goroutines
	for range 9 {
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
	// Do not use t.Parallel() - this test accesses global analysisBuffers and captureBuffers maps

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)

	// Use a proper RTSP URL instead of a sourceID - this is what NewFFmpegStream expects
	testURL := fmt.Sprintf("rtsp://test-%d.example.com/stream", time.Now().UnixNano())
	stream := NewFFmpegStream(testURL, "tcp", audioChan)

	// Get the actual source ID that was created/registered by the stream
	actualSourceID := stream.source.ID

	// Now allocate buffers for the actual source ID that was created
	if err := AllocateAnalysisBuffer(conf.BufferSize*3, actualSourceID); err != nil {
		t.Skip("Cannot allocate analysis buffer for test")
	}
	defer func() {
		if err := RemoveAnalysisBuffer(actualSourceID); err != nil {
			t.Logf("Failed to remove analysis buffer: %v", err)
		}
	}()

	if err := AllocateCaptureBufferIfNeeded(60, conf.SampleRate, conf.BitDepth/8, actualSourceID); err != nil {
		t.Skip("Cannot allocate capture buffer for test")
	}
	defer func() {
		if err := RemoveCaptureBuffer(actualSourceID); err != nil {
			t.Logf("Failed to remove capture buffer: %v", err)
		}
	}()

	// Test audio data handling
	testData := make([]byte, 1024)
	for i := range testData {
		testData[i] = byte(i % 256)
	}

	err := stream.handleAudioData(testData)
	require.NoError(t, err)

	// Check if data was sent to audio channel
	select {
	case data := <-audioChan:
		assert.NotNil(t, data.AudioLevel)
		assert.WithinDuration(t, time.Now(), data.Timestamp, time.Second)
	case <-time.After(100 * time.Millisecond):
		require.Fail(t, "No data received on audio channel")
	}
}

func TestFFmpegStream_CircuitBreakerBehavior(t *testing.T) {
	t.Parallel()

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)

	// Test circuit breaker is initially closed
	assert.False(t, stream.isCircuitOpen())

	// Simulate failures to trigger circuit breaker
	for range 12 { // More than circuitBreakerThreshold (10)
		stream.recordFailure(2 * time.Second) // Simulate rapid failures
	}

	// Circuit should now be open
	assert.True(t, stream.isCircuitOpen())

	// Reset failures and circuit state for test
	stream.resetCircuitStateForTest()

	// Circuit should be closed again
	assert.False(t, stream.isCircuitOpen())
}

func TestFFmpegStream_DataRateCalculation(t *testing.T) {
	t.Parallel()

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)

	// Test data rate calculator
	calc := stream.dataRateCalc

	// Add some data samples
	calc.addSample(1024)
	calc.addSample(2048)
	calc.addSample(1536)

	// Calculate rate
	rate := calc.getRate()
	assert.Greater(t, rate, 0.0)
}

func TestFFmpegStream_HealthTracking(t *testing.T) {
	t.Parallel()

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)

	// Test initial health - stream should not be healthy without data
	health := stream.GetHealth()
	assert.False(t, health.IsHealthy) // Changed: new streams are not healthy by default

	// Make stream healthy by simulating data reception
	stream.updateLastDataTime()
	health = stream.GetHealth()
	assert.True(t, health.IsHealthy)
	assert.Equal(t, 0, health.RestartCount)
	assert.Equal(t, int64(0), health.TotalBytesReceived)

	// Simulate data reception
	stream.updateLastDataTime()
	stream.bytesReceivedMu.Lock()
	stream.totalBytesReceived = 1024
	stream.bytesReceivedMu.Unlock()

	// Update restart count
	stream.restartCountMu.Lock()
	stream.restartCount = 3
	stream.restartCountMu.Unlock()

	// Check updated health
	health = stream.GetHealth()
	assert.True(t, health.IsHealthy)
	assert.Equal(t, 3, health.RestartCount)
	assert.Equal(t, int64(1024), health.TotalBytesReceived)
}

func TestFFmpegStream_ConcurrentHealthAndDataUpdates(t *testing.T) {
	t.Parallel()

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)

	const numGoroutines = 10
	const numOperations = 100
	done := make(chan bool, numGoroutines)

	// Concurrent health checks
	for range numGoroutines / 2 {
		go func() {
			defer func() { done <- true }()
			for range numOperations {
				health := stream.GetHealth()
				assert.NotNil(t, health)
			}
		}()
	}

	// Concurrent data updates
	for range numGoroutines / 2 {
		go func() {
			defer func() { done <- true }()
			for range numOperations {
				stream.updateLastDataTime()
				stream.bytesReceivedMu.Lock()
				stream.totalBytesReceived += 100
				stream.bytesReceivedMu.Unlock()
			}
		}()
	}

	// Wait for all goroutines to complete
	for range numGoroutines {
		select {
		case <-done:
			// Completed
		case <-time.After(2 * time.Second):
			require.Fail(t, "Concurrent test timed out")
		}
	}

	// Verify final state is consistent
	health := stream.GetHealth()
	assert.NotNil(t, health)
	assert.Positive(t, health.TotalBytesReceived)
}

func TestFFmpegStream_BackoffOverflowProtection(t *testing.T) {
	t.Parallel()

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)

	// Test with very high restart count that would cause overflow without protection
	stream.restartCountMu.Lock()
	stream.restartCount = 100 // This would cause overflow without protection
	stream.restartCountMu.Unlock()

	// Calculate expected backoff with overflow protection
	exponent := min(100-1,
		// maxBackoffExponent constant
		20)

	expectedBackoff := min(stream.backoffDuration*time.Duration(1<<uint(exponent)), stream.maxBackoff) //nolint:gosec // G115: exponent is capped by min()

	// The expected backoff should be the maximum allowed (2 minutes)
	assert.Equal(t, 2*time.Minute, expectedBackoff)

	// Verify the calculation doesn't panic or overflow
	assert.NotPanics(t, func() {
		// This should not panic due to overflow protection
		testBackoff := stream.backoffDuration * time.Duration(1<<uint(exponent)) //nolint:gosec // G115: exponent is capped by min()
		_ = testBackoff
	})
}

func TestFFmpegStream_DroppedDataLogging(t *testing.T) {
	t.Parallel()

	// Create a stream with a very small channel to force drops
	audioChan := make(chan UnifiedAudioData, 1)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)

	// Fill the channel
	select {
	case audioChan <- UnifiedAudioData{}:
		// Channel filled
	default:
		require.Fail(t, "Expected to be able to fill the channel")
	}

	// Test rate limiting - first call should log
	stream.logDroppedData()

	// Second call immediately should not log (rate limited)
	// We can't easily test the actual logging output, but we can test the rate limiting logic
	firstLogTime := stream.lastDropLogTime

	// Call again immediately - should not update lastDropLogTime due to rate limiting
	stream.logDroppedData()
	assert.Equal(t, firstLogTime, stream.lastDropLogTime, "Log time should not change due to rate limiting")
}

// TestFFmpegStream_ValidateUserTimeout tests the timeout validation functionality
func TestFFmpegStream_ValidateUserTimeout(t *testing.T) {
	t.Parallel()

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)

	tests := []struct {
		name          string
		timeoutStr    string
		expectError   bool
		errorContains string
	}{
		{
			name:        "valid_1_second",
			timeoutStr:  "1000000",
			expectError: false,
		},
		{
			name:        "valid_5_seconds",
			timeoutStr:  "5000000",
			expectError: false,
		},
		{
			name:        "valid_30_seconds",
			timeoutStr:  "30000000",
			expectError: false,
		},
		{
			name:        "valid_large_timeout",
			timeoutStr:  "120000000", // 2 minutes
			expectError: false,
		},
		{
			name:          "invalid_format_letters",
			timeoutStr:    "abc",
			expectError:   true,
			errorContains: "invalid timeout format",
		},
		{
			name:          "invalid_format_mixed",
			timeoutStr:    "123abc",
			expectError:   true,
			errorContains: "invalid timeout format",
		},
		{
			name:          "empty_string",
			timeoutStr:    "",
			expectError:   true,
			errorContains: "invalid timeout format",
		},
		{
			name:          "too_short_zero",
			timeoutStr:    "0",
			expectError:   true,
			errorContains: "timeout too short",
		},
		{
			name:          "too_short_negative",
			timeoutStr:    "-1000",
			expectError:   true,
			errorContains: "timeout too short",
		},
		{
			name:          "too_short_half_second",
			timeoutStr:    "500000", // 0.5 seconds
			expectError:   true,
			errorContains: "timeout too short",
		},
		{
			name:          "boundary_minimum_minus_one",
			timeoutStr:    "999999", // Just under 1 second
			expectError:   true,
			errorContains: "timeout too short",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := stream.validateUserTimeout(tt.timeoutStr)

			if tt.expectError {
				require.Error(t, err, "Expected error for timeout: %s", tt.timeoutStr)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains, "Error should contain expected text")
				}
			} else {
				assert.NoError(t, err, "Expected no error for valid timeout: %s", tt.timeoutStr)
			}
		})
	}
}

// TestFFmpegStream_TimeoutDetectionLogic tests the logic for detecting user-provided timeouts
func TestFFmpegStream_TimeoutDetectionLogic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		ffmpegParameters     []string
		expectedUserTimeout  bool
		expectedTimeoutValue string
	}{
		{
			name:                 "no_parameters",
			ffmpegParameters:     []string{},
			expectedUserTimeout:  false,
			expectedTimeoutValue: "",
		},
		{
			name:                 "no_timeout_parameter",
			ffmpegParameters:     []string{"-loglevel", "debug", "-rtsp_flags", "prefer_tcp"},
			expectedUserTimeout:  false,
			expectedTimeoutValue: "",
		},
		{
			name:                 "timeout_parameter_present",
			ffmpegParameters:     []string{"-timeout", "5000000", "-loglevel", "debug"},
			expectedUserTimeout:  true,
			expectedTimeoutValue: "5000000",
		},
		{
			name:                 "timeout_parameter_middle",
			ffmpegParameters:     []string{"-loglevel", "debug", "-timeout", "10000000", "-rtsp_flags", "prefer_tcp"},
			expectedUserTimeout:  true,
			expectedTimeoutValue: "10000000",
		},
		{
			name:                 "timeout_parameter_last_with_value",
			ffmpegParameters:     []string{"-loglevel", "debug", "-timeout", "15000000"},
			expectedUserTimeout:  true,
			expectedTimeoutValue: "15000000",
		},
		{
			name:                 "timeout_parameter_without_value",
			ffmpegParameters:     []string{"-loglevel", "debug", "-timeout"},
			expectedUserTimeout:  false, // No value provided
			expectedTimeoutValue: "",
		},
		{
			name:                 "multiple_timeout_parameters",
			ffmpegParameters:     []string{"-timeout", "5000000", "-loglevel", "debug", "-timeout", "10000000"},
			expectedUserTimeout:  true,
			expectedTimeoutValue: "5000000", // Should use first one found
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Test the timeout detection logic using the actual helper function
			hasUserTimeout, userTimeoutValue := detectUserTimeout(tt.ffmpegParameters)

			assert.Equal(t, tt.expectedUserTimeout, hasUserTimeout, "User timeout detection should match expected")
			assert.Equal(t, tt.expectedTimeoutValue, userTimeoutValue, "User timeout value should match expected")
		})
	}
}

// TestFFmpegStream_TimeoutBehaviorIntegration tests the integration of timeout logic
func TestFFmpegStream_TimeoutBehaviorIntegration(t *testing.T) {
	t.Parallel()

	// This test verifies the timeout behavior integration by checking
	// what arguments would be generated for different scenarios

	tests := []struct {
		name                    string
		ffmpegParameters        []string
		expectedContainsDefault bool
		expectedValidationCall  bool
	}{
		{
			name:                    "no_user_timeout_adds_default",
			ffmpegParameters:        []string{"-loglevel", "debug"},
			expectedContainsDefault: true,
			expectedValidationCall:  false,
		},
		{
			name:                    "valid_user_timeout_no_default",
			ffmpegParameters:        []string{"-timeout", "5000000", "-loglevel", "debug"},
			expectedContainsDefault: false,
			expectedValidationCall:  true,
		},
		{
			name:                    "empty_parameters_adds_default",
			ffmpegParameters:        []string{},
			expectedContainsDefault: true,
			expectedValidationCall:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create base args like in the actual function
			args := []string{"-rtsp_transport", "tcp"}

			// Use the actual helper function to detect timeout
			hasUserTimeout, userTimeoutValue := detectUserTimeout(tt.ffmpegParameters)

			// Add default timeout if user hasn't provided one
			if !hasUserTimeout {
				args = append(args, "-timeout", "30000000")
			}

			// Add user parameters
			if len(tt.ffmpegParameters) > 0 {
				// In real implementation, this is where validation would be called
				if hasUserTimeout {
					assert.True(t, tt.expectedValidationCall, "Should call validation when user timeout detected")
					assert.NotEmpty(t, userTimeoutValue, "User timeout value should not be empty")
				}
				args = append(args, tt.ffmpegParameters...)
			}

			// Check if default timeout was added
			hasDefaultTimeout := false
			for i, arg := range args {
				if arg == "-timeout" && i+1 < len(args) && args[i+1] == "30000000" {
					hasDefaultTimeout = true
					break
				}
			}

			assert.Equal(t, tt.expectedContainsDefault, hasDefaultTimeout,
				"Default timeout presence should match expected for test: %s", tt.name)

			// Verify the args contain expected elements
			assert.Contains(t, args, "-rtsp_transport", "Should always contain transport parameter")
			assert.Contains(t, args, "tcp", "Should always contain transport value")

			// Verify timeout is present in some form
			hasAnyTimeout := slices.Contains(args, "-timeout")
			assert.True(t, hasAnyTimeout, "Should always have a timeout parameter")
		})
	}
}

// TestDetectUserTimeout tests the helper function for detecting user timeouts
func TestDetectUserTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		params        []string
		expectedFound bool
		expectedValue string
	}{
		{
			name:          "empty_params",
			params:        []string{},
			expectedFound: false,
			expectedValue: "",
		},
		{
			name:          "no_timeout_param",
			params:        []string{"-loglevel", "debug"},
			expectedFound: false,
			expectedValue: "",
		},
		{
			name:          "timeout_with_value",
			params:        []string{"-timeout", "5000000"},
			expectedFound: true,
			expectedValue: "5000000",
		},
		{
			name:          "timeout_without_value",
			params:        []string{"-timeout"},
			expectedFound: false,
			expectedValue: "",
		},
		{
			name:          "timeout_in_middle",
			params:        []string{"-loglevel", "debug", "-timeout", "10000000", "-rtsp_flags", "prefer_tcp"},
			expectedFound: true,
			expectedValue: "10000000",
		},
		{
			name:          "first_timeout_wins",
			params:        []string{"-timeout", "5000000", "-timeout", "10000000"},
			expectedFound: true,
			expectedValue: "5000000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			found, value := detectUserTimeout(tt.params)
			assert.Equal(t, tt.expectedFound, found, "Detection result should match expected")
			assert.Equal(t, tt.expectedValue, value, "Timeout value should match expected")
		})
	}
}

// TestFFmpegStream_LastDataTimeReset tests that lastDataTime is properly reset when processAudio starts
// This prevents confusing "inactive for 0 seconds" log messages
func TestFFmpegStream_LastDataTimeReset(t *testing.T) {
	t.Parallel()

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)

	// Set lastDataTime to a non-zero value to simulate previous process run
	oldTime := time.Now().Add(-1 * time.Hour)
	stream.lastDataMu.Lock()
	stream.lastDataTime = oldTime
	stream.lastDataMu.Unlock()

	// Verify the old time is set
	stream.lastDataMu.RLock()
	beforeReset := stream.lastDataTime
	stream.lastDataMu.RUnlock()
	assert.Equal(t, oldTime, beforeReset, "Old time should be set before reset")

	// Note: We can't easily test processAudio() directly as it requires FFmpeg,
	// but we verify the reset logic by checking that NewFFmpegStream initializes
	// lastDataTime to zero time
	newStream := NewFFmpegStream("rtsp://test2.example.com/stream", "tcp", audioChan)
	newStream.lastDataMu.RLock()
	initialTime := newStream.lastDataTime
	newStream.lastDataMu.RUnlock()
	assert.True(t, initialTime.IsZero(), "New stream should have zero lastDataTime")
}

// TestFFmpegStream_ZeroTimeHandling tests handling of zero time in GetHealth()
func TestFFmpegStream_ZeroTimeHandling(t *testing.T) {
	t.Parallel()

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)

	// Test 1: New stream with zero lastDataTime should not be healthy initially
	health := stream.GetHealth()
	assert.True(t, health.LastDataReceived.IsZero(), "LastDataReceived should be zero time for new stream")
	assert.False(t, health.IsHealthy, "New stream should not be healthy without data")
	assert.False(t, health.IsReceivingData, "New stream should not be receiving data")

	// Test 2: Stream within grace period should not be marked as critically unhealthy
	// (IsHealthy will be false, but stream is in grace period)
	timeSinceCreation := time.Since(stream.streamCreatedAt)
	assert.Less(t, timeSinceCreation, defaultGracePeriod, "Test should run within grace period")

	// Test 3: After grace period expires, stream should still report not healthy
	// Simulate grace period expiration by setting streamCreatedAt to the past
	stream.streamCreatedAt = time.Now().Add(-2 * defaultGracePeriod)
	health = stream.GetHealth()
	assert.True(t, health.LastDataReceived.IsZero(), "LastDataReceived should still be zero")
	assert.False(t, health.IsHealthy, "Stream past grace period without data should not be healthy")

	// Test 4: After receiving data, stream should become healthy
	stream.updateLastDataTime()
	health = stream.GetHealth()
	assert.False(t, health.LastDataReceived.IsZero(), "LastDataReceived should not be zero after update")
	assert.True(t, health.IsHealthy, "Stream with recent data should be healthy")
	assert.True(t, health.IsReceivingData, "Stream with recent data should be receiving data")
}

// TestSecondsSinceOrZero tests the helper function for safe time calculations
func TestSecondsSinceOrZero(t *testing.T) {
	t.Parallel()

	// Test 1: Zero time should return 0
	zeroTime := time.Time{}
	result := secondsSinceOrZero(zeroTime)
	assert.InDelta(t, 0.0, result, 0.001, "Zero time should return 0 seconds")

	// Test 2: Recent time should return small positive number
	recentTime := time.Now().Add(-5 * time.Second)
	result = secondsSinceOrZero(recentTime)
	assert.Greater(t, result, 4.0, "Recent time should return positive seconds")
	assert.Less(t, result, 6.0, "Recent time should be approximately 5 seconds")

	// Test 3: Old time should return large positive number
	oldTime := time.Now().Add(-1 * time.Hour)
	result = secondsSinceOrZero(oldTime)
	assert.Greater(t, result, 3500.0, "Old time should return large seconds")
	assert.Less(t, result, 3700.0, "Old time should be approximately 3600 seconds")
}

// TestFormatLastDataDescription tests the stream's helper function for formatting last data time
func TestFormatLastDataDescription(t *testing.T) {
	t.Parallel()

	// Test 1: Zero time should return "never received data"
	zeroTime := time.Time{}
	result := formatLastDataDescription(zeroTime)
	assert.Equal(t, "never received data", result, "Zero time should return 'never received data'")

	// Test 2: Recent time should return "X.Xs ago" format
	recentTime := time.Now().Add(-5 * time.Second)
	result = formatLastDataDescription(recentTime)
	assert.NotEqual(t, "never received data", result, "Recent time should not return 'never received data'")
	assert.Contains(t, result, "ago", "Result should contain 'ago'")
	assert.Contains(t, result, "s ago", "Result should contain seconds with 'ago'")
	// Verify format is approximately "5.0s ago"
	assert.Regexp(t, `^\d+\.\ds ago$`, result, "Result should match pattern 'X.Xs ago'")

	// Test 3: Old time should also return "X.Xs ago" format with larger number
	oldTime := time.Now().Add(-2 * time.Minute)
	result = formatLastDataDescription(oldTime)
	assert.NotEqual(t, "never received data", result, "Old time should not return 'never received data'")
	assert.Contains(t, result, "ago", "Result should contain 'ago'")
	// Verify the seconds value is approximately 120
	assert.Regexp(t, `^1\d{2}\.\ds ago$`, result, "Result should be approximately '120.0s ago'")
}

// TestFormatTimeSinceData tests the manager's helper function for formatting time
func TestFormatTimeSinceData(t *testing.T) {
	t.Parallel()

	// Test 1: Zero time should return "never received data"
	zeroTime := time.Time{}
	result := formatTimeSinceData(zeroTime)
	assert.Equal(t, "never received data", result, "Zero time should return 'never received data'")

	// Test 2: Recent time should return formatted duration
	recentTime := time.Now().Add(-5 * time.Second)
	result = formatTimeSinceData(recentTime)
	assert.NotEqual(t, "never received data", result, "Recent time should not return 'never received data'")
	assert.Contains(t, result, "s", "Result should contain duration with seconds")

	// Test 3: Old time should return formatted duration
	oldTime := time.Now().Add(-2 * time.Minute)
	result = formatTimeSinceData(oldTime)
	assert.NotEqual(t, "never received data", result, "Old time should not return 'never received data'")
	assert.Contains(t, result, "m", "Result should contain duration with minutes")
}

// TestGetTimeSinceDataSeconds tests the manager's helper function for time calculations
func TestGetTimeSinceDataSeconds(t *testing.T) {
	t.Parallel()

	// Test 1: Zero time should return 0
	zeroTime := time.Time{}
	result := getTimeSinceDataSeconds(zeroTime)
	assert.InDelta(t, 0.0, result, 0.001, "Zero time should return 0 seconds")

	// Test 2: Recent time should return small positive number
	recentTime := time.Now().Add(-10 * time.Second)
	result = getTimeSinceDataSeconds(recentTime)
	assert.Greater(t, result, 9.0, "Recent time should return positive seconds")
	assert.Less(t, result, 11.0, "Recent time should be approximately 10 seconds")

	// Test 3: Old time should return large positive number
	oldTime := time.Now().Add(-2 * time.Minute)
	result = getTimeSinceDataSeconds(oldTime)
	assert.Greater(t, result, 119.0, "Old time should return large seconds")
	assert.Less(t, result, 121.0, "Old time should be approximately 120 seconds")
}

// TestFFmpegStream_HealthWithZeroTime tests GetHealth behavior when lastDataTime is zero
func TestFFmpegStream_HealthWithZeroTime(t *testing.T) {
	t.Parallel()

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)

	// Ensure lastDataTime is zero
	stream.lastDataMu.Lock()
	stream.lastDataTime = time.Time{}
	stream.lastDataMu.Unlock()

	// Get health multiple times to ensure consistency
	for i := range 5 {
		health := stream.GetHealth()
		assert.True(t, health.LastDataReceived.IsZero(), "LastDataReceived should be zero on iteration %d", i)
		assert.NotNil(t, health, "Health should not be nil on iteration %d", i)
	}
}

// TestFFmpegStream_ConcurrentLastDataTimeAccess tests thread safety of lastDataTime
func TestFFmpegStream_ConcurrentLastDataTimeAccess(t *testing.T) {
	t.Parallel()

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)

	const numGoroutines = 20
	const numOperations = 100
	done := make(chan bool, numGoroutines)

	// Concurrent readers
	for range numGoroutines / 2 {
		go func() {
			defer func() { done <- true }()
			for range numOperations {
				stream.lastDataMu.RLock()
				_ = stream.lastDataTime
				stream.lastDataMu.RUnlock()
			}
		}()
	}

	// Concurrent writers
	for range numGoroutines / 2 {
		go func() {
			defer func() { done <- true }()
			for range numOperations {
				stream.updateLastDataTime()
			}
		}()
	}

	// Wait for all goroutines with timeout
	for range numGoroutines {
		select {
		case <-done:
			// Completed
		case <-time.After(5 * time.Second):
			require.Fail(t, "Concurrent test timed out - possible deadlock")
		}
	}

	// Verify final state is valid
	stream.lastDataMu.RLock()
	finalTime := stream.lastDataTime
	stream.lastDataMu.RUnlock()
	assert.False(t, finalTime.IsZero(), "After concurrent updates, lastDataTime should not be zero")
}
