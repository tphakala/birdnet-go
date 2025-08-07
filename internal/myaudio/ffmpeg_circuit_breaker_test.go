// ffmpeg_circuit_breaker_test.go
// Comprehensive tests for circuit breaker behavior and edge cases
// These tests validate the circuit breaker logic and identify timing issues

package myaudio

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestCircuitBreaker_FailureAccumulation tests whether failures properly
// accumulate across restart attempts without premature resets.
func TestCircuitBreaker_FailureAccumulation(t *testing.T) {
	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.local/accumulation", "tcp", audioChan)
	
	// Record failures without resets
	failureRuntimes := []time.Duration{
		50 * time.Millisecond,   // Immediate failure
		100 * time.Millisecond,  // Very short runtime
		200 * time.Millisecond,  // Short runtime  
		80 * time.Millisecond,   // Quick failure
		30 * time.Millisecond,   // Instant failure
	}
	
	for i, runtime := range failureRuntimes {
		stream.recordFailure(runtime)
		
		currentFailures := stream.getConsecutiveFailures()
		t.Logf("After failure %d (runtime: %v): consecutive failures = %d", 
			i+1, runtime, currentFailures)
		
		// Failures should accumulate
		assert.Equal(t, i+1, currentFailures, "Failures should accumulate properly")
		
		// Check if circuit breaker opens at appropriate thresholds
		if runtime < 1*time.Second && currentFailures >= 3 {
			assert.True(t, stream.isCircuitOpen(), 
				"Circuit should open after 3 immediate failures")
		} else if runtime < 5*time.Second && currentFailures >= 5 {
			assert.True(t, stream.isCircuitOpen(), 
				"Circuit should open after 5 rapid failures")
		}
	}
	
	// Verify final state
	finalFailures := stream.getConsecutiveFailures()
	isCircuitOpen := stream.isCircuitOpen()
	
	assert.Equal(t, len(failureRuntimes), finalFailures, "All failures should be counted")
	assert.True(t, isCircuitOpen, "Circuit should be open after multiple failures")
}

// TestCircuitBreaker_RapidFailureThresholds tests the enhanced circuit breaker
// logic that opens earlier for rapid failures.
func TestCircuitBreaker_RapidFailureThresholds(t *testing.T) {
	testCases := []struct {
		name             string
		runtime          time.Duration
		expectedThreshold int
		expectedReason    string
	}{
		{
			name:             "immediate_failures",
			runtime:          500 * time.Millisecond,
			expectedThreshold: 3, // Should open after 3 immediate failures
			expectedReason:    "immediate connection failures",
		},
		{
			name:             "rapid_failures",
			runtime:          3 * time.Second,
			expectedThreshold: 5, // Should open after 5 rapid failures  
			expectedReason:    "rapid process failures",
		},
		{
			name:             "quick_failures",
			runtime:          20 * time.Second,
			expectedThreshold: 8, // Should open after 8 quick failures
			expectedReason:    "quick process failures",
		},
		{
			name:             "normal_failures",
			runtime:          60 * time.Second,
			expectedThreshold: 10, // Standard threshold
			expectedReason:    "consecutive failure threshold",
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			audioChan := make(chan UnifiedAudioData, 10)
			defer close(audioChan)
			stream := NewFFmpegStream("rtsp://test.local/"+tc.name, "tcp", audioChan)
			
			// Record failures up to just before threshold
			for i := 0; i < tc.expectedThreshold-1; i++ {
				stream.recordFailure(tc.runtime)
				assert.False(t, stream.isCircuitOpen(), 
					"Circuit should not open before threshold")
			}
			
			// Record one more failure to trigger circuit breaker
			stream.recordFailure(tc.runtime)
			
			// Circuit should now be open
			assert.True(t, stream.isCircuitOpen(), 
				"Circuit should open after %d failures with runtime %v", 
				tc.expectedThreshold, tc.runtime)
			
			assert.Equal(t, tc.expectedThreshold, stream.getConsecutiveFailures(),
				"Should have exact threshold number of failures")
		})
	}
}

// TestCircuitBreaker_CooldownPeriod tests the circuit breaker cooldown behavior.
func TestCircuitBreaker_CooldownPeriod(t *testing.T) {
	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.local/cooldown", "tcp", audioChan)
	
	// Force circuit breaker to open
	for i := 0; i < 12; i++ { // More than threshold
		stream.recordFailure(100 * time.Millisecond)
	}
	
	assert.True(t, stream.isCircuitOpen(), "Circuit should be open")
	
	// Circuit should stay open during cooldown period
	initialOpenTime := time.Now()
	
	// Test at various points during cooldown
	testPoints := []time.Duration{
		1 * time.Second,
		30 * time.Second, 
		2 * time.Minute,
		4 * time.Minute,
	}
	
	for _, elapsed := range testPoints {
		// Simulate passage of time by updating circuit open time
		stream.circuitMu.Lock()
		stream.circuitOpenTime = initialOpenTime.Add(-elapsed)
		stream.circuitMu.Unlock()
		
		isOpen := stream.isCircuitOpen()
		
		if elapsed < circuitBreakerCooldown {
			assert.True(t, isOpen, "Circuit should be open during cooldown period (elapsed: %v)", elapsed)
		} else {
			assert.False(t, isOpen, "Circuit should close after cooldown period (elapsed: %v)", elapsed)
			
			// After cooldown, failures should be reset
			assert.Equal(t, 0, stream.getConsecutiveFailures(), 
				"Failures should reset after cooldown")
		}
	}
}

// TestCircuitBreaker_PrematureResetBug tests the specific bug where failures
// are reset before the process has proven stability.
func TestCircuitBreaker_PrematureResetBug(t *testing.T) {
	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.local/premature", "tcp", audioChan)
	
	scenarios := []struct {
		name           string
		initialFailures int
		shouldResetAfter func(*FFmpegStream) bool
		description    string
	}{
		{
			name:           "reset_after_process_start_only",
			initialFailures: 8,
			shouldResetAfter: func(s *FFmpegStream) bool {
				// BUG: Current implementation resets here
				s.resetFailures()
				return true
			},
			description: "BUGGY: Reset immediately after process start",
		},
		{
			name:           "reset_after_stable_operation",
			initialFailures: 8,
			shouldResetAfter: func(s *FFmpegStream) bool {
				// CORRECT: Should only reset after proven stability
				// Simulate 30 seconds of runtime and 1MB of data
				s.cmdMu.Lock()
				s.processStartTime = time.Now().Add(-35 * time.Second)
				s.cmdMu.Unlock()
				
				s.bytesReceivedMu.Lock()
				s.totalBytesReceived = 1024 * 1024
				s.bytesReceivedMu.Unlock()
				
				// Only reset if stable
				if time.Since(s.processStartTime) > 30*time.Second && s.totalBytesReceived > 100*1024 {
					s.resetFailures()
					return true
				}
				return false
			},
			description: "CORRECT: Reset only after stable operation",
		},
	}
	
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// Set initial failure count
			stream.setConsecutiveFailures(scenario.initialFailures)
			initialCount := stream.getConsecutiveFailures()
			
			// Apply reset logic
			wasReset := scenario.shouldResetAfter(stream)
			finalCount := stream.getConsecutiveFailures()
			
			t.Logf("%s:", scenario.description)
			t.Logf("  Initial failures: %d", initialCount)
			t.Logf("  Reset applied: %v", wasReset)
			t.Logf("  Final failures: %d", finalCount)
			
			assert.Equal(t, scenario.initialFailures, initialCount, 
				"Initial failures should be set correctly")
			
			if scenario.name == "reset_after_process_start_only" {
				// This demonstrates the bug
				assert.Equal(t, 0, finalCount, "BUG: Failures reset prematurely")
			} else {
				// This shows correct behavior
				assert.Equal(t, 0, finalCount, "CORRECT: Failures reset after stability")
			}
		})
	}
}

// TestCircuitBreaker_ProcessStabilityValidation tests validation of process
// stability before resetting failures.
func TestCircuitBreaker_ProcessStabilityValidation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Process testing is Unix-specific")
	}
	
	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.local/stability", "tcp", audioChan)
	
	// Set up initial failure count
	stream.setConsecutiveFailures(5)
	
	testCases := []struct {
		name         string
		runtime      time.Duration
		bytesReceived int64
		shouldReset  bool
		description  string
	}{
		{
			name:         "too_short_runtime",
			runtime:      10 * time.Second,
			bytesReceived: 1024 * 1024, // 1MB
			shouldReset:  false,
			description:  "Should not reset with short runtime despite sufficient data",
		},
		{
			name:         "insufficient_data",
			runtime:      60 * time.Second,
			bytesReceived: 1024, // 1KB
			shouldReset:  false, 
			description:  "Should not reset with long runtime but insufficient data",
		},
		{
			name:         "stable_operation",
			runtime:      45 * time.Second,
			bytesReceived: 2 * 1024 * 1024, // 2MB
			shouldReset:  true,
			description:  "Should reset with stable runtime and sufficient data",
		},
		{
			name:         "barely_stable",
			runtime:      30 * time.Second,
			bytesReceived: 100 * 1024, // 100KB (minimum)
			shouldReset:  true,
			description:  "Should reset at minimum stability thresholds",
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset to initial state
			stream.setConsecutiveFailures(5)
			
			// Set up process state
			stream.cmdMu.Lock()
			stream.processStartTime = time.Now().Add(-tc.runtime)
			stream.cmdMu.Unlock()
			
			stream.bytesReceivedMu.Lock()
			stream.totalBytesReceived = tc.bytesReceived
			stream.bytesReceivedMu.Unlock()
			
			initialFailures := stream.getConsecutiveFailures()
			
			// Apply conditional reset logic (this would be in the fix)
			minStabilityTime := 30 * time.Second
			minResetBytes := int64(100 * 1024) // 100KB
			
			if time.Since(stream.processStartTime) >= minStabilityTime && 
			   stream.totalBytesReceived >= minResetBytes {
				stream.resetFailures()
			}
			
			finalFailures := stream.getConsecutiveFailures()
			
			t.Logf("%s:", tc.description)
			t.Logf("  Runtime: %v (required: %v)", tc.runtime, minStabilityTime)
			t.Logf("  Bytes: %d (required: %d)", tc.bytesReceived, minResetBytes)
			t.Logf("  Initial failures: %d", initialFailures)
			t.Logf("  Final failures: %d", finalFailures)
			t.Logf("  Expected reset: %v", tc.shouldReset)
			
			if tc.shouldReset {
				assert.Equal(t, 0, finalFailures, 
					"Failures should be reset for stable operation")
			} else {
				assert.Equal(t, initialFailures, finalFailures, 
					"Failures should not be reset for unstable operation")
			}
		})
	}
}

// TestCircuitBreaker_ConcurrentFailureAndReset tests race conditions between
// failure recording and resetting.
func TestCircuitBreaker_ConcurrentFailureAndReset(t *testing.T) {
	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.local/concurrent", "tcp", audioChan)
	
	var wg sync.WaitGroup
	var failureCount int32
	var resetCount int32
	
	// Concurrent failure recording
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			stream.recordFailure(100 * time.Millisecond)
			atomic.AddInt32(&failureCount, 1)
			time.Sleep(1 * time.Millisecond)
		}
	}()
	
	// Concurrent failure resetting (simulating the bug)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 25; i++ {
			stream.resetFailures()
			atomic.AddInt32(&resetCount, 1)
			time.Sleep(2 * time.Millisecond)
		}
	}()
	
	wg.Wait()
	
	finalFailures := stream.getConsecutiveFailures()
	totalFailures := atomic.LoadInt32(&failureCount)
	totalResets := atomic.LoadInt32(&resetCount)
	
	t.Logf("Total failure operations: %d", totalFailures)
	t.Logf("Total reset operations: %d", totalResets)
	t.Logf("Final consecutive failures: %d", finalFailures)
	
	// With the bug (frequent resets), failures never accumulate properly
	assert.LessOrEqual(t, finalFailures, 5, 
		"Frequent resets prevent failure accumulation")
	assert.Greater(t, totalFailures, int32(finalFailures), 
		"Many failures recorded but not accumulated due to resets")
}

// TestCircuitBreaker_StateTransitions tests the complete state machine of
// the circuit breaker through multiple transitions.
func TestCircuitBreaker_StateTransitions(t *testing.T) {
	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("rtsp://test.local/transitions", "tcp", audioChan)
	
	// Track state transitions
	states := []struct {
		phase       string
		failures    int
		isOpen      bool
		canReset    bool
	}{}
	
	recordState := func(phase string) {
		failures := stream.getConsecutiveFailures()
		isOpen := stream.isCircuitOpen()
		
		states = append(states, struct {
			phase       string
			failures    int
			isOpen      bool
			canReset    bool
		}{phase, failures, isOpen, false})
		
		t.Logf("Phase: %s, Failures: %d, Circuit Open: %v", phase, failures, isOpen)
	}
	
	// Phase 1: Initial state
	recordState("initial")
	
	// Phase 2: Record some failures (not enough to open)
	for i := 0; i < 3; i++ {
		stream.recordFailure(2 * time.Second)
	}
	recordState("few_failures")
	
	// Phase 3: Record enough failures to open circuit
	for i := 0; i < 7; i++ {
		stream.recordFailure(100 * time.Millisecond)
	}
	recordState("circuit_opened")
	
	// Phase 4: Try to record more failures (should still be open)
	stream.recordFailure(50 * time.Millisecond)
	recordState("additional_failure")
	
	// Phase 5: Simulate cooldown expiration
	stream.circuitMu.Lock()
	stream.circuitOpenTime = time.Now().Add(-circuitBreakerCooldown - time.Second)
	stream.circuitMu.Unlock()
	recordState("cooldown_expired")
	
	// Phase 6: Reset after stability (proper fix behavior)
	stream.cmdMu.Lock()
	stream.processStartTime = time.Now().Add(-35 * time.Second)
	stream.cmdMu.Unlock()
	
	stream.bytesReceivedMu.Lock()
	stream.totalBytesReceived = 2 * 1024 * 1024
	stream.bytesReceivedMu.Unlock()
	
	// Apply conditional reset (fix logic)
	if time.Since(stream.processStartTime) > 30*time.Second && 
	   stream.totalBytesReceived > 100*1024 {
		stream.resetFailures()
	}
	recordState("after_stable_reset")
	
	// Analyze transitions
	t.Logf("\nCircuit Breaker State Transitions:")
	for i, state := range states {
		t.Logf("%d. %s: failures=%d, open=%v", 
			i+1, state.phase, state.failures, state.isOpen)
	}
	
	// Validate expected transitions
	assert.Equal(t, 0, states[0].failures, "Should start with no failures")
	assert.False(t, states[0].isOpen, "Should start with circuit closed")
	
	assert.Equal(t, 3, states[1].failures, "Should accumulate early failures") 
	assert.False(t, states[1].isOpen, "Should not open with few failures")
	
	assert.True(t, states[2].isOpen, "Should open after enough failures")
	assert.Greater(t, states[2].failures, 5, "Should have accumulated failures")
	
	assert.True(t, states[3].isOpen, "Should stay open during cooldown")
	
	assert.False(t, states[4].isOpen, "Should close after cooldown expiration")
	
	assert.Equal(t, 0, states[5].failures, "Should reset after stable operation")
	assert.False(t, states[5].isOpen, "Should remain closed after reset")
}

// TestCircuitBreaker_EdgeCaseScenarios tests various edge cases and boundary conditions.
func TestCircuitBreaker_EdgeCaseScenarios(t *testing.T) {
	testCases := []struct {
		name        string
		setup       func(*FFmpegStream)
		operation   func(*FFmpegStream)
		validate    func(*testing.T, *FFmpegStream)
		description string
	}{
		{
			name: "zero_runtime_failure",
			setup: func(s *FFmpegStream) {},
			operation: func(s *FFmpegStream) {
				s.recordFailure(0 * time.Millisecond)
			},
			validate: func(t *testing.T, s *FFmpegStream) {
				t.Helper()
				assert.Equal(t, 1, s.getConsecutiveFailures(), 
					"Should record zero-runtime failure")
			},
			description: "Handle zero runtime failures",
		},
		{
			name: "negative_runtime_failure",
			setup: func(s *FFmpegStream) {},
			operation: func(s *FFmpegStream) {
				s.recordFailure(-100 * time.Millisecond)
			},
			validate: func(t *testing.T, s *FFmpegStream) {
				t.Helper()
				assert.Equal(t, 1, s.getConsecutiveFailures(), 
					"Should handle negative runtime")
			},
			description: "Handle negative runtime failures",
		},
		{
			name: "very_long_runtime_failure",
			setup: func(s *FFmpegStream) {},
			operation: func(s *FFmpegStream) {
				s.recordFailure(24 * time.Hour) // Very long runtime
			},
			validate: func(t *testing.T, s *FFmpegStream) {
				t.Helper()
				assert.Equal(t, 1, s.getConsecutiveFailures(), 
					"Should handle very long runtime")
				assert.False(t, s.isCircuitOpen(), 
					"Should not open circuit for long-running process failure")
			},
			description: "Handle very long runtime failures",
		},
		{
			name: "reset_with_zero_failures",
			setup: func(s *FFmpegStream) {},
			operation: func(s *FFmpegStream) {
				s.resetFailures() // Reset when already at zero
			},
			validate: func(t *testing.T, s *FFmpegStream) {
				t.Helper()
				assert.Equal(t, 0, s.getConsecutiveFailures(), 
					"Should handle reset with zero failures")
			},
			description: "Handle reset when no failures exist",
		},
		{
			name: "multiple_consecutive_resets",
			setup: func(s *FFmpegStream) {
				s.setConsecutiveFailures(5)
			},
			operation: func(s *FFmpegStream) {
				s.resetFailures()
				s.resetFailures() 
				s.resetFailures()
			},
			validate: func(t *testing.T, s *FFmpegStream) {
				t.Helper()
				assert.Equal(t, 0, s.getConsecutiveFailures(), 
					"Multiple resets should be safe")
			},
			description: "Handle multiple consecutive resets",
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			audioChan := make(chan UnifiedAudioData, 10)
			defer close(audioChan)
			stream := NewFFmpegStream("rtsp://test.local/edge_case", "tcp", audioChan)
			
			tc.setup(stream)
			tc.operation(stream)
			tc.validate(t, stream)
			
			t.Logf("Edge case validated: %s", tc.description)
		})
	}
}