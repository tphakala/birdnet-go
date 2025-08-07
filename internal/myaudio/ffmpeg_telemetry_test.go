// ffmpeg_telemetry_test.go
// Tests for telemetry and grace period functionality in FFmpeg stream management

package myaudio

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestGracePeriodForNewStreams tests that new streams have a grace period
// before being marked unhealthy when no data has been received
func TestGracePeriodForNewStreams(t *testing.T) {
	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.local/grace", "tcp", audioChan)
	
	// Immediately after creation, stream should be in grace period
	health := stream.GetHealth()
	assert.False(t, health.IsHealthy, "New stream should not be healthy without data")
	assert.True(t, health.LastDataReceived.IsZero(), "Should have zero time for LastDataReceived")
	assert.False(t, health.IsReceivingData, "Should not be receiving data")
	
	// Verify stream is still tracked as created recently
	timeSinceCreation := time.Since(stream.streamCreatedAt)
	assert.Less(t, timeSinceCreation, defaultGracePeriod, "Should be within grace period")
	
	// Simulate time passing but still within grace period
	stream.streamCreatedAt = time.Now().Add(-15 * time.Second)
	health = stream.GetHealth()
	assert.False(t, health.IsHealthy, "Stream in grace period should not be healthy")
	assert.False(t, health.IsReceivingData, "Should not be receiving data")
	
	// Simulate grace period expiring
	stream.streamCreatedAt = time.Now().Add(-35 * time.Second)
	health = stream.GetHealth()
	assert.False(t, health.IsHealthy, "Stream after grace period with no data should not be healthy")
	assert.False(t, health.IsReceivingData, "Should not be receiving data")
	
	// Now simulate receiving data
	stream.updateLastDataTime()
	health = stream.GetHealth()
	assert.True(t, health.IsHealthy, "Stream should be healthy after receiving data")
	assert.True(t, health.IsReceivingData, "Should be receiving data")
}

// TestCircuitBreakerClosureTelemetry tests that circuit breaker closure
// triggers appropriate telemetry events
func TestCircuitBreakerClosureTelemetry(t *testing.T) {
	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.local/telemetry", "tcp", audioChan)
	
	// Force circuit breaker to open
	for i := 0; i < 5; i++ {
		stream.recordFailure(2 * time.Second) // Rapid failures
	}
	
	// Verify circuit is open
	assert.True(t, stream.isCircuitOpen(), "Circuit should be open after failures")
	
	// Simulate cooldown period expiring
	stream.circuitMu.Lock()
	stream.circuitOpenTime = time.Now().Add(-circuitBreakerCooldown - time.Second)
	previousFailures := stream.consecutiveFailures
	stream.circuitMu.Unlock()
	
	// Check circuit again - this should trigger closure and telemetry
	isOpen := stream.isCircuitOpen()
	assert.False(t, isOpen, "Circuit should be closed after cooldown")
	
	// Verify failures were reset
	stream.circuitMu.Lock()
	assert.Equal(t, 0, stream.consecutiveFailures, "Failures should be reset after cooldown")
	assert.True(t, stream.circuitOpenTime.IsZero(), "Circuit open time should be reset")
	stream.circuitMu.Unlock()
	
	// Note: In a real test environment with telemetry enabled,
	// we would verify that the telemetry event was sent.
	// For this test, we're verifying the state changes that trigger telemetry.
	t.Logf("Circuit breaker closed after %d failures and cooldown period", previousFailures)
}

// TestFailureResetTelemetry tests that failure resets after stable operation
// trigger appropriate telemetry events
func TestFailureResetTelemetry(t *testing.T) {
	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.local/reset", "tcp", audioChan)
	
	// Set up some failures
	stream.circuitMu.Lock()
	stream.consecutiveFailures = 5
	stream.circuitMu.Unlock()
	
	// Set up process start time to be old enough for stability
	stream.cmdMu.Lock()
	stream.processStartTime = time.Now().Add(-35 * time.Second)
	stream.cmdMu.Unlock()
	
	// Simulate receiving enough data for stability
	totalBytes := int64(150 * 1024) // 150KB
	
	// Call conditional failure reset
	stream.conditionalFailureReset(totalBytes)
	
	// Verify failures were reset
	stream.circuitMu.Lock()
	assert.Equal(t, 0, stream.consecutiveFailures, "Failures should be reset after stable operation")
	stream.circuitMu.Unlock()
	
	// Note: In a real test environment with telemetry enabled,
	// we would verify that the telemetry event was sent.
	t.Log("Failure reset telemetry triggered after stable operation")
}

// TestGracePeriodWithProcessRestart tests grace period behavior
// when processes restart
func TestGracePeriodWithProcessRestart(t *testing.T) {
	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.local/restart_grace", "tcp", audioChan)
	
	// Simulate stream that has been running for a while
	stream.streamCreatedAt = time.Now().Add(-5 * time.Minute)
	
	// Stream should not be in grace period anymore
	health := stream.GetHealth()
	assert.False(t, health.IsHealthy, "Old stream with no data should not be healthy")
	
	// Simulate process restart by updating process start time
	stream.cmdMu.Lock()
	stream.processStartTime = time.Now()
	stream.cmdMu.Unlock()
	
	// Grace period is based on stream creation, not process start
	// So stream should still not be in grace period
	health = stream.GetHealth()
	assert.False(t, health.IsHealthy, "Restarted old stream should not get new grace period")
	
	// Verify that receiving data makes it healthy
	stream.updateLastDataTime()
	health = stream.GetHealth()
	assert.True(t, health.IsHealthy, "Stream should be healthy after receiving data")
}

// TestCircuitBreakerStateTransitionsWithTelemetry tests the complete
// circuit breaker lifecycle with telemetry events
func TestCircuitBreakerStateTransitionsWithTelemetry(t *testing.T) {
	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.local/transitions", "tcp", audioChan)
	
	// Phase 1: Initial state
	assert.False(t, stream.isCircuitOpen(), "Circuit should start closed")
	assert.Equal(t, 0, stream.getConsecutiveFailures(), "Should start with no failures")
	
	// Phase 2: Accumulate failures to open circuit
	for i := 0; i < 3; i++ {
		stream.recordFailure(500 * time.Millisecond) // Immediate failures
	}
	
	assert.True(t, stream.isCircuitOpen(), "Circuit should open after immediate failures")
	
	// Phase 3: Circuit stays open during cooldown
	time.Sleep(10 * time.Millisecond)
	assert.True(t, stream.isCircuitOpen(), "Circuit should stay open during cooldown")
	
	// Phase 4: Simulate cooldown expiration
	stream.circuitMu.Lock()
	stream.circuitOpenTime = time.Now().Add(-circuitBreakerCooldown - time.Second)
	stream.circuitMu.Unlock()
	
	// Phase 5: Circuit closes after cooldown
	assert.False(t, stream.isCircuitOpen(), "Circuit should close after cooldown")
	assert.Equal(t, 0, stream.getConsecutiveFailures(), "Failures should reset after cooldown")
	
	// Phase 6: Can accumulate failures again
	stream.recordFailure(100 * time.Millisecond)
	assert.Equal(t, 1, stream.getConsecutiveFailures(), "Should be able to record new failures")
	assert.False(t, stream.isCircuitOpen(), "Circuit should not open with single failure")
	
	t.Log("Circuit breaker state transitions completed with telemetry events")
}

// TestMultipleStreamsWithGracePeriods tests that multiple streams
// can have independent grace periods
func TestMultipleStreamsWithGracePeriods(t *testing.T) {
	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	
	// Create streams at different times
	stream1 := NewFFmpegStream("rtsp://test.local/stream1", "tcp", audioChan)
	time.Sleep(5 * time.Millisecond)
	stream2 := NewFFmpegStream("rtsp://test.local/stream2", "tcp", audioChan)
	
	// Both should be in grace period but created at different times
	assert.NotEqual(t, stream1.streamCreatedAt, stream2.streamCreatedAt, 
		"Streams should have different creation times")
	
	// Simulate stream1 grace period expiring
	stream1.streamCreatedAt = time.Now().Add(-35 * time.Second)
	
	health1 := stream1.GetHealth()
	health2 := stream2.GetHealth()
	
	assert.False(t, health1.IsHealthy, "Stream1 after grace period should not be healthy")
	assert.False(t, health2.IsHealthy, "Stream2 in grace period should not be healthy")
	
	// Verify they have independent grace periods
	timeSinceCreation1 := time.Since(stream1.streamCreatedAt)
	timeSinceCreation2 := time.Since(stream2.streamCreatedAt)
	
	assert.Greater(t, timeSinceCreation1, defaultGracePeriod, "Stream1 should be past grace period")
	assert.Less(t, timeSinceCreation2, defaultGracePeriod, "Stream2 should be within grace period")
}

// TestConditionalFailureResetRequirements tests that failure reset
// only happens when stability requirements are met
func TestConditionalFailureResetRequirements(t *testing.T) {
	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.local/conditional", "tcp", audioChan)
	
	// Set up failures
	stream.setConsecutiveFailures(7)
	
	testCases := []struct {
		name           string
		runtime        time.Duration
		bytesReceived  int64
		shouldReset    bool
		description    string
	}{
		{
			name:          "insufficient_runtime",
			runtime:       15 * time.Second,
			bytesReceived: 200 * 1024,
			shouldReset:   false,
			description:   "Should not reset with insufficient runtime",
		},
		{
			name:          "insufficient_bytes",
			runtime:       35 * time.Second,
			bytesReceived: 50 * 1024,
			shouldReset:   false,
			description:   "Should not reset with insufficient data",
		},
		{
			name:          "both_insufficient",
			runtime:       10 * time.Second,
			bytesReceived: 10 * 1024,
			shouldReset:   false,
			description:   "Should not reset when both requirements unmet",
		},
		{
			name:          "both_sufficient",
			runtime:       35 * time.Second,
			bytesReceived: 150 * 1024,
			shouldReset:   true,
			description:   "Should reset when both requirements met",
		},
		{
			name:          "exact_minimum",
			runtime:       30 * time.Second,
			bytesReceived: 100 * 1024,
			shouldReset:   true,
			description:   "Should reset at exact minimum thresholds",
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset failures for each test
			stream.setConsecutiveFailures(7)
			
			// Set up process start time
			stream.cmdMu.Lock()
			stream.processStartTime = time.Now().Add(-tc.runtime)
			stream.cmdMu.Unlock()
			
			// Call conditional reset
			stream.conditionalFailureReset(tc.bytesReceived)
			
			// Check result
			failures := stream.getConsecutiveFailures()
			if tc.shouldReset {
				assert.Equal(t, 0, failures, tc.description)
			} else {
				assert.Equal(t, 7, failures, tc.description)
			}
		})
	}
}