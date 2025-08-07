// Package myaudio provides validation tests for implemented circuit breaker fixes.
// This test suite verifies the correctness of fixes for RTSP stream restart issues:
// - Initial health state correctly reports unhealthy for streams without data
// - Circuit breaker failure accumulation without premature resets
// - Conditional failure reset only after proven stable operation
// - Proper handling of zero time values in health checks
// - Grace period implementation for new streams
// These tests confirm that the circuit breaker and health monitoring fixes
// prevent infinite restart loops while maintaining proper stream recovery capabilities.

package myaudio

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestFix_InitialHealthStateCorrect validates that the initial health state
// fix is working - new streams should not be healthy until they receive data.
func TestFix_InitialHealthStateCorrect(t *testing.T) {
	// Store original settings
	originalSettings := conf.GetTestSettings()
	if originalSettings == nil {
		originalSettings = conf.Setting()
	}
	
	// Set up test configuration
	testSettings := *originalSettings
	testSettings.Realtime.RTSP.Health.HealthyDataThreshold = 60 // 60 seconds
	conf.SetTestSettings(&testSettings)
	defer conf.SetTestSettings(originalSettings)
	
	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	
	// Create a new stream
	creationTime := time.Now()
	stream := NewFFmpegStream("rtsp://test.local/fixed", "tcp", audioChan)
	
	// Check health immediately after creation - should NOT be healthy
	health := stream.GetHealth()
	
	// Get the lastDataTime to verify the fix
	lastDataTime := stream.GetLastDataTime()
	
	timeSinceCreation := time.Since(creationTime)
	isZeroTime := lastDataTime.IsZero()
	
	t.Logf("FIXED: Initial health state validation")
	t.Logf("  Stream creation time: %v", creationTime)
	t.Logf("  Last data time: %v", lastDataTime)
	t.Logf("  Is zero time: %v", isZeroTime)
	t.Logf("  Time since creation: %v", timeSinceCreation)
	t.Logf("  Health reported as: %v", health.IsHealthy)
	
	// FIXED: Stream should NOT be healthy without receiving data
	assert.False(t, health.IsHealthy, "FIXED: New stream correctly not healthy without data")
	assert.True(t, isZeroTime, "FIXED: lastDataTime correctly zero for new stream")
	
	// Now simulate receiving data
	stream.updateLastDataTime()
	healthAfterData := stream.GetHealth()
	
	t.Logf("  Health after data: %v", healthAfterData.IsHealthy)
	
	// Should be healthy after receiving data
	assert.True(t, healthAfterData.IsHealthy, "FIXED: Stream healthy after receiving data")
	
	t.Logf("SUCCESS: Initial health state fix validated")
}

// TestFix_CircuitBreakerFailureAccumulation validates that failures now
// properly accumulate without premature resets.
func TestFix_CircuitBreakerFailureAccumulation(t *testing.T) {
	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.local/accumulation_fixed", "tcp", audioChan)
	
	// Record multiple rapid failures WITHOUT manual resets
	// This simulates the fixed behavior where startProcess() no longer resets failures
	rapidFailures := []time.Duration{
		50 * time.Millisecond,   // Immediate failure
		80 * time.Millisecond,   // Very short runtime
		30 * time.Millisecond,   // Instant failure
		60 * time.Millisecond,   // Quick failure
		40 * time.Millisecond,   // Rapid failure
	}
	
	t.Logf("FIXED: Circuit breaker failure accumulation validation")
	
	for i, runtime := range rapidFailures {
		// FIXED: No premature reset - just record the failure
		stream.recordFailure(runtime)
		
		currentFailures := stream.getConsecutiveFailures()
		isOpen := stream.isCircuitOpen()
		
		t.Logf("  After failure %d (runtime: %v): failures=%d, circuit_open=%v", 
			i+1, runtime, currentFailures, isOpen)
		
		// FIXED: Failures should accumulate
		assert.Equal(t, i+1, currentFailures, "FIXED: Failures should accumulate without resets")
		
		// FIXED: Circuit should open after 5 rapid failures (< 5s runtime)
		if i >= 4 { // After 5th rapid failure
			assert.True(t, isOpen, "FIXED: Circuit should open after 5 rapid failures")
			t.Logf("  SUCCESS: Circuit breaker opened after %d rapid failures", i+1)
			break
		}
	}
	
	t.Logf("SUCCESS: Circuit breaker failure accumulation fix validated")
}

// TestFix_ConditionalFailureReset validates that failures are only reset
// after proven stable operation.
func TestFix_ConditionalFailureReset(t *testing.T) {
	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.local/conditional_reset", "tcp", audioChan)
	
	// Set up some initial failures
	initialFailures := 5
	stream.setConsecutiveFailures(initialFailures)
	
	// Set up process state (simulating a running process)
	stream.cmdMu.Lock()
	stream.processStartTime = time.Now().Add(-35 * time.Second) // 35 seconds runtime
	stream.cmdMu.Unlock()
	
	t.Logf("FIXED: Conditional failure reset validation")
	t.Logf("  Initial failures: %d", initialFailures)
	t.Logf("  Process runtime: 35 seconds")
	
	// Test scenarios
	testCases := []struct {
		name         string
		bytesReceived int64
		shouldReset  bool
		description  string
	}{
		{
			name:         "insufficient_data",
			bytesReceived: 50 * 1024, // 50KB - below 100KB threshold
			shouldReset:  false,
			description:  "Should not reset with insufficient data despite long runtime",
		},
		{
			name:         "sufficient_data",
			bytesReceived: 200 * 1024, // 200KB - above 100KB threshold
			shouldReset:  true,
			description:  "Should reset with sufficient data and runtime",
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset to initial state
			stream.setConsecutiveFailures(initialFailures)
			
			// Call conditional reset with test data
			stream.conditionalFailureReset(tc.bytesReceived)
			
			finalFailures := stream.getConsecutiveFailures()
			
			t.Logf("  %s:", tc.description)
			t.Logf("    Bytes received: %d", tc.bytesReceived)
			t.Logf("    Failures before: %d", initialFailures)
			t.Logf("    Failures after: %d", finalFailures)
			t.Logf("    Expected reset: %v", tc.shouldReset)
			
			if tc.shouldReset {
				assert.Equal(t, 0, finalFailures, "FIXED: Failures should reset with stable operation")
			} else {
				assert.Equal(t, initialFailures, finalFailures, "FIXED: Failures should not reset without stability")
			}
		})
	}
	
	t.Logf("SUCCESS: Conditional failure reset fix validated")
}

// TestFix_HealthStateTransitions validates the complete health state
// transition behavior after fixes.
func TestFix_HealthStateTransitions(t *testing.T) {
	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.local/transitions_fixed", "tcp", audioChan)
	
	t.Logf("FIXED: Health state transition validation")
	
	// Phase 1: Initial state - should NOT be healthy
	health1 := stream.GetHealth()
	t.Logf("  Phase 1 - Initial: healthy=%v (should be false)", health1.IsHealthy)
	assert.False(t, health1.IsHealthy, "FIXED: Initial state correctly not healthy")
	
	// Phase 2: After receiving data - should be healthy
	stream.updateLastDataTime()
	health2 := stream.GetHealth()
	t.Logf("  Phase 2 - After data: healthy=%v (should be true)", health2.IsHealthy)
	assert.True(t, health2.IsHealthy, "FIXED: Healthy after receiving data")
	
	// Phase 3: After data becomes old - should be unhealthy
	stream.setLastDataTimeForTest(time.Now().Add(-70 * time.Second)) // Older than 60s threshold
	
	health3 := stream.GetHealth()
	t.Logf("  Phase 3 - Old data: healthy=%v (should be false)", health3.IsHealthy)
	assert.False(t, health3.IsHealthy, "FIXED: Unhealthy with old data")
	
	// Phase 4: Fresh data again - should be healthy
	stream.updateLastDataTime()
	health4 := stream.GetHealth()
	t.Logf("  Phase 4 - Fresh data: healthy=%v (should be true)", health4.IsHealthy)
	assert.True(t, health4.IsHealthy, "FIXED: Healthy with fresh data")
	
	t.Logf("SUCCESS: Health state transitions fix validated")
}

// TestFix_ZeroTimeHandling validates that zero time is properly handled
// in all health-related operations.
func TestFix_ZeroTimeHandling(t *testing.T) {
	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.local/zero_time", "tcp", audioChan)
	
	// Verify initial zero time state
	lastDataTime := stream.GetLastDataTime()
	
	t.Logf("FIXED: Zero time handling validation")
	t.Logf("  Initial lastDataTime: %v", lastDataTime)
	t.Logf("  Is zero time: %v", lastDataTime.IsZero())
	
	assert.True(t, lastDataTime.IsZero(), "FIXED: Initial lastDataTime is zero")
	
	// Test health with zero time
	health := stream.GetHealth()
	t.Logf("  Health with zero time: %v", health.IsHealthy)
	t.Logf("  IsReceivingData with zero time: %v", health.IsReceivingData)
	
	assert.False(t, health.IsHealthy, "FIXED: Not healthy with zero time")
	assert.False(t, health.IsReceivingData, "FIXED: Not receiving data with zero time")
	
	// The LastDataReceived in response should handle zero time gracefully
	t.Logf("  LastDataReceived: %v", health.LastDataReceived)
	assert.True(t, health.LastDataReceived.IsZero(), "FIXED: LastDataReceived preserves zero time")
	
	t.Logf("SUCCESS: Zero time handling fix validated")
}

// TestFix_ConfigurationRespected validates that the fixes work correctly
// with different configuration thresholds.
func TestFix_ConfigurationRespected(t *testing.T) {
	originalSettings := conf.GetTestSettings()
	if originalSettings == nil {
		originalSettings = conf.Setting()
	}
	defer conf.SetTestSettings(originalSettings)
	
	// Test with custom threshold
	testSettings := *originalSettings
	testSettings.Realtime.RTSP.Health.HealthyDataThreshold = 30 // 30 seconds
	conf.SetTestSettings(&testSettings)
	
	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.local/config_respected", "tcp", audioChan)
	
	t.Logf("FIXED: Configuration respect validation")
	t.Logf("  Custom threshold: 30 seconds")
	
	// Initial state should still not be healthy
	initialHealth := stream.GetHealth()
	t.Logf("  Initial health: %v", initialHealth.IsHealthy)
	assert.False(t, initialHealth.IsHealthy, "FIXED: Not healthy initially despite custom threshold")
	
	// Set data age to 25 seconds (within 30s threshold)
	stream.setLastDataTimeForTest(time.Now().Add(-25 * time.Second))
	
	health25s := stream.GetHealth()
	t.Logf("  Health with 25s old data: %v (should be true)", health25s.IsHealthy)
	assert.True(t, health25s.IsHealthy, "FIXED: Healthy within custom threshold")
	
	// Set data age to 35 seconds (beyond 30s threshold)
	stream.setLastDataTimeForTest(time.Now().Add(-35 * time.Second))
	
	health35s := stream.GetHealth()
	t.Logf("  Health with 35s old data: %v (should be false)", health35s.IsHealthy)
	assert.False(t, health35s.IsHealthy, "FIXED: Unhealthy beyond custom threshold")
	
	t.Logf("SUCCESS: Configuration respect fix validated")
}

// TestFix_NoPrematureResetInRealScenario validates that in a realistic
// scenario, failures are not reset prematurely.
func TestFix_NoPrematureResetInRealScenario(t *testing.T) {
	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.local/realistic_scenario", "tcp", audioChan)
	
	t.Logf("FIXED: No premature reset in realistic scenario")
	
	// Simulate a realistic failure scenario:
	// 1. Process starts (no automatic reset should happen)
	// 2. Process fails quickly several times
	// 3. Circuit breaker should eventually open
	
	// Simulate several process start/fail cycles
	for i := 0; i < 6; i++ {
		// Simulate process start - set process start time but don't reset failures
		stream.cmdMu.Lock()
		stream.processStartTime = time.Now()
		stream.cmdMu.Unlock()
		
		// Process fails quickly (before any substantial data)
		stream.recordFailure(100 * time.Millisecond)
		
		currentFailures := stream.getConsecutiveFailures()
		isOpen := stream.isCircuitOpen()
		
		t.Logf("  Cycle %d: failures=%d, circuit_open=%v", i+1, currentFailures, isOpen)
		
		// FIXED: Failures should accumulate
		assert.Equal(t, i+1, currentFailures, "FIXED: Failures accumulate across cycles")
		
		// Circuit should open after enough rapid failures
		if currentFailures >= 5 { // Based on rapid failure logic
			assert.True(t, isOpen, "FIXED: Circuit opens after accumulated failures")
			t.Logf("  SUCCESS: Circuit breaker opened after %d failures", currentFailures)
			break
		}
	}
	
	t.Logf("SUCCESS: No premature reset in realistic scenario validated")
}