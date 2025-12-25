package myaudio

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// createTestStream creates a minimal test FFmpegStream instance for unit testing.
// It initializes only state-related fields required for state machine testing.
// Omits circuit breaker, manager, and other components not needed for state transitions.
// Safe for unit tests that don't start actual FFmpeg processes.
func createTestStream(tb testing.TB, url, transport string) *FFmpegStream {
	tb.Helper() // Mark as test helper for better error reporting

	// Create a minimal test source
	source := &AudioSource{
		ID:               "test-stream",
		DisplayName:      "Test Stream",
		Type:             SourceTypeRTSP,
		connectionString: url,
		SafeString:       privacy.SanitizeRTSPUrl(url),
		RegisteredAt:     time.Now(),
		IsActive:         true,
	}

	audioChan := make(chan UnifiedAudioData, 100)

	return &FFmpegStream{
		source:           source,
		transport:        transport,
		audioChan:        audioChan,
		restartChan:      make(chan struct{}, 1),
		stopChan:         make(chan struct{}),
		backoffDuration:  defaultBackoffDuration,
		maxBackoff:       maxBackoffDuration,
		lastDataTime:     time.Time{},
		dataRateCalc:     newDataRateCalculator(source.SafeString, dataRateWindowSize),
		lastDropLogTime:  time.Now(),
		streamCreatedAt:  time.Now(),
		processState:     StateIdle,
		stateTransitions: make([]StateTransition, 0, 100),
	}
}

// TestProcessStateString tests the String() method for ProcessState
func TestProcessStateString(t *testing.T) {
	tests := []struct {
		name  string
		state ProcessState
		want  string
	}{
		{"StateIdle", StateIdle, "idle"},
		{"StateStarting", StateStarting, "starting"},
		{"StateRunning", StateRunning, "running"},
		{"StateRestarting", StateRestarting, "restarting"},
		{"StateBackoff", StateBackoff, "backoff"},
		{"StateCircuitOpen", StateCircuitOpen, "circuit_open"},
		{"StateStopped", StateStopped, "stopped"},
		{"Unknown", ProcessState(99), "unknown(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.state.String()
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestIsValidTransition tests valid and invalid state transitions
func TestIsValidTransition(t *testing.T) {
	tests := []struct {
		name string
		from ProcessState
		to   ProcessState
		want bool
	}{
		// Valid transitions from StateIdle
		{"Idle to Starting", StateIdle, StateStarting, true},
		{"Idle to Stopped", StateIdle, StateStopped, true},
		{"Idle to Idle (idempotent)", StateIdle, StateIdle, true},

		// Invalid transitions from StateIdle
		{"Idle to Running", StateIdle, StateRunning, false},
		{"Idle to Backoff", StateIdle, StateBackoff, false},

		// Valid transitions from StateStarting
		{"Starting to Running", StateStarting, StateRunning, true},
		{"Starting to Backoff", StateStarting, StateBackoff, true},
		{"Starting to CircuitOpen", StateStarting, StateCircuitOpen, true},
		{"Starting to Stopped", StateStarting, StateStopped, true},

		// Invalid transitions from StateStarting
		{"Starting to Idle", StateStarting, StateIdle, false},
		{"Starting to Restarting", StateStarting, StateRestarting, false},

		// Valid transitions from StateRunning
		{"Running to Restarting", StateRunning, StateRestarting, true},
		{"Running to Backoff", StateRunning, StateBackoff, true},
		{"Running to CircuitOpen", StateRunning, StateCircuitOpen, true},
		{"Running to Stopped", StateRunning, StateStopped, true},

		// Invalid transitions from StateRunning
		{"Running to Idle", StateRunning, StateIdle, false},
		{"Running to Starting", StateRunning, StateStarting, false},

		// Valid transitions from StateRestarting
		{"Restarting to Starting", StateRestarting, StateStarting, true},
		{"Restarting to Backoff", StateRestarting, StateBackoff, true},
		{"Restarting to CircuitOpen", StateRestarting, StateCircuitOpen, true},
		{"Restarting to Stopped", StateRestarting, StateStopped, true},

		// Invalid transitions from StateRestarting
		{"Restarting to Idle", StateRestarting, StateIdle, false},
		{"Restarting to Running", StateRestarting, StateRunning, false},

		// Valid transitions from StateBackoff
		{"Backoff to Starting", StateBackoff, StateStarting, true},
		{"Backoff to CircuitOpen", StateBackoff, StateCircuitOpen, true},
		{"Backoff to Stopped", StateBackoff, StateStopped, true},

		// Invalid transitions from StateBackoff
		{"Backoff to Idle", StateBackoff, StateIdle, false},
		{"Backoff to Running", StateBackoff, StateRunning, false},
		{"Backoff to Restarting", StateBackoff, StateRestarting, false},

		// Valid transitions from StateCircuitOpen
		{"CircuitOpen to Starting", StateCircuitOpen, StateStarting, true},
		{"CircuitOpen to Stopped", StateCircuitOpen, StateStopped, true},

		// Invalid transitions from StateCircuitOpen
		{"CircuitOpen to Idle", StateCircuitOpen, StateIdle, false},
		{"CircuitOpen to Running", StateCircuitOpen, StateRunning, false},
		{"CircuitOpen to Backoff", StateCircuitOpen, StateBackoff, false},

		// StateStopped is terminal - no transitions allowed
		{"Stopped to anything", StateStopped, StateIdle, false},
		{"Stopped to Starting", StateStopped, StateStarting, false},
		{"Stopped to Stopped (idempotent)", StateStopped, StateStopped, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidTransition(tt.from, tt.to)
			assert.Equal(t, tt.want, got,
				"isValidTransition(%s, %s)", tt.from.String(), tt.to.String())
		})
	}
}

// TestStateTransitionRecording tests that state transitions are recorded correctly
func TestStateTransitionRecording(t *testing.T) {
	stream := createTestStream(t, "rtsp://test.local/stream", "tcp")

	// Initial state should be Idle
	assert.Equal(t, StateIdle, stream.GetProcessState(), "Initial state")

	// Transition to Starting
	stream.transitionState(StateStarting, "test transition")

	// Verify state changed
	assert.Equal(t, StateStarting, stream.GetProcessState(), "After transition")

	// Verify transition was recorded
	history := stream.GetStateHistory()
	require.Len(t, history, 1, "Expected 1 transition in history")

	transition := history[0]
	assert.Equal(t, StateIdle, transition.From, "Transition.From")
	assert.Equal(t, StateStarting, transition.To, "Transition.To")
	assert.Equal(t, "test transition", transition.Reason, "Transition.Reason")
	assert.WithinDuration(t, time.Now(), transition.Timestamp, time.Second,
		"Transition timestamp should be recent")
}

// TestStateTransitionHistoryBounded tests that state history is bounded to 100 entries
func TestStateTransitionHistoryBounded(t *testing.T) {
	stream := createTestStream(t, "rtsp://test.local/stream", "tcp")

	// Create more than 100 transitions
	for i := range 150 {
		// Alternate between two states to avoid invalid transition warnings
		if i%2 == 0 {
			stream.transitionState(StateStarting, "test transition")
		} else {
			stream.transitionState(StateRunning, "test transition")
		}
	}

	// Verify history is bounded to 100
	history := stream.GetStateHistory()
	assert.Len(t, history, 100, "History should be bounded to 100")
}

// TestGetStateHistoryConcurrency tests that GetStateHistory returns a copy
func TestGetStateHistoryConcurrency(t *testing.T) {
	stream := createTestStream(t, "rtsp://test.local/stream", "tcp")

	// Add some transitions
	stream.transitionState(StateStarting, "first")
	stream.transitionState(StateRunning, "second")

	// Get history
	history1 := stream.GetStateHistory()

	// Add more transitions
	stream.transitionState(StateRestarting, "third")

	// Get history again
	history2 := stream.GetStateHistory()

	// Verify first history wasn't modified (it should be a copy)
	assert.Len(t, history1, 2, "history1 should not be modified")
	assert.Len(t, history2, 3, "history2 should have 3 transitions")
}

// TestInvalidTransitionLenient tests that invalid transitions are applied in lenient mode
// This implements Issue #1 fix: lenient approach for robustness
func TestInvalidTransitionLenient(t *testing.T) {
	stream := createTestStream(t, "rtsp://test.local/stream", "tcp")

	// Set initial state to Running
	stream.processState = StateRunning

	// Try invalid transition: Running -> Idle
	// In lenient mode, this should be applied for robustness (logged in debug mode only)
	stream.transitionState(StateIdle, "invalid transition test - lenient recovery")

	// State should change despite being invalid (lenient behavior for user-friendliness)
	assert.Equal(t, StateIdle, stream.GetProcessState(),
		"lenient mode should apply transition")

	// Transition should be recorded in history
	history := stream.GetStateHistory()
	require.Len(t, history, 1,
		"invalid transitions are still recorded")

	// Verify the transition details
	assert.Equal(t, StateRunning, history[0].From, "Transition.From")
	assert.Equal(t, StateIdle, history[0].To, "Transition.To")
}

// TestIdempotentTransitionIgnored tests that idempotent transitions are ignored
// This implements Issue #4 fix: skip idempotent transitions to reduce log noise
func TestIdempotentTransitionIgnored(t *testing.T) {
	stream := createTestStream(t, "rtsp://test.local/stream", "tcp")

	// Set initial state to Running
	stream.transitionState(StateStarting, "first transition")
	stream.transitionState(StateRunning, "second transition")

	// Get initial history count
	initialHistoryLen := len(stream.GetStateHistory())

	// Try idempotent transition: Running -> Running (should be ignored)
	stream.transitionState(StateRunning, "idempotent transition - should be ignored")

	// State should remain the same (obviously)
	assert.Equal(t, StateRunning, stream.GetProcessState(),
		"State after idempotent transition")

	// Idempotent transition should NOT be recorded in history (reduces noise)
	history := stream.GetStateHistory()
	assert.Len(t, history, initialHistoryLen,
		"idempotent transitions should be ignored")
}

// TestStreamHealthIncludesState tests that StreamHealth includes process state
func TestStreamHealthIncludesState(t *testing.T) {
	stream := createTestStream(t, "rtsp://test.local/stream", "tcp")

	// Set state to Running
	stream.transitionState(StateStarting, "starting")
	stream.transitionState(StateRunning, "running")

	// Get health
	health := stream.GetHealth()

	// Verify state is included
	assert.Equal(t, StateRunning, health.ProcessState, "Health.ProcessState")

	// Verify state history is included (last 10)
	assert.Len(t, health.StateHistory, 2, "Health.StateHistory")
}

// TestIsRestartingStates tests that IsRestarting correctly identifies restart-related states
func TestIsRestartingStates(t *testing.T) {
	tests := []struct {
		name  string
		state ProcessState
		want  bool
	}{
		{"StateIdle - not restarting", StateIdle, false},
		{"StateStarting - restarting", StateStarting, true},
		{"StateRunning - not restarting", StateRunning, false},
		{"StateRestarting - restarting", StateRestarting, true},
		{"StateBackoff - restarting", StateBackoff, true},
		{"StateCircuitOpen - restarting", StateCircuitOpen, true},
		{"StateStopped - not restarting", StateStopped, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := createTestStream(t, "rtsp://test.local/stream", "tcp")
			stream.processState = tt.state

			got := stream.IsRestarting()
			assert.Equal(t, tt.want, got,
				"IsRestarting() with state %s", tt.state.String())
		})
	}
}

// TestStateTransitionConcurrency tests concurrent state transitions
func TestStateTransitionConcurrency(t *testing.T) {
	t.Parallel() // Run concurrently with other tests

	stream := createTestStream(t, "rtsp://test.local/stream", "tcp")

	// Set initial state
	stream.processState = StateRunning

	// Perform concurrent transitions using valid state transitions
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			// Valid transitions: Running → Restarting → Starting
			stream.transitionState(StateRestarting, "concurrent test")
			stream.transitionState(StateStarting, "concurrent test")
		})
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// Verify we have transitions recorded
	// Note: Due to concurrent execution, some transitions might be skipped if the state
	// is already at the target (idempotent), or due to race conditions in goroutine scheduling
	history := stream.GetStateHistory()
	assert.GreaterOrEqual(t, len(history), 5,
		"got too few transitions")
	assert.LessOrEqual(t, len(history), 20,
		"history should not exceed 20")

	// Verify thread safety: no panics, state machine still consistent
	currentState := stream.GetProcessState()
	assert.GreaterOrEqual(t, currentState, StateIdle,
		"Invalid final state after concurrency")
	assert.LessOrEqual(t, currentState, StateStopped,
		"Invalid final state after concurrency")
}

// TestStoppedIsTerminal tests that StateStopped is truly terminal
// This ensures Stop() remains immutable and prevents inconsistent state
func TestStoppedIsTerminal(t *testing.T) {
	stream := createTestStream(t, "rtsp://test.local/stream", "tcp")

	// Transition to Stopped state
	stream.transitionState(StateStopped, "stop requested")

	// Verify state is Stopped
	require.Equal(t, StateStopped, stream.GetProcessState(),
		"Initial state after stop")

	// Get initial history length
	historyBeforeAttempt := stream.GetStateHistory()
	initialHistoryLen := len(historyBeforeAttempt)

	// Attempt to leave terminal state (should be blocked)
	stream.transitionState(StateStarting, "should be blocked")

	// State should still be Stopped (transition blocked)
	assert.Equal(t, StateStopped, stream.GetProcessState(),
		"transition should be blocked")

	// History should not record a Stopped->Starting transition
	historyAfterAttempt := stream.GetStateHistory()
	assert.Len(t, historyAfterAttempt, initialHistoryLen,
		"blocked transition should not be recorded")

	// Verify no invalid transitions in history
	for _, tr := range historyAfterAttempt {
		if tr.From == StateStopped && tr.To != StateStopped {
			require.FailNow(t, "Terminal transition recorded in history",
				"%s → %s (this should never happen)", tr.From.String(), tr.To.String())
		}
	}

	// Try multiple different target states - all should be blocked
	attemptedStates := []ProcessState{StateIdle, StateStarting, StateRunning, StateRestarting, StateBackoff, StateCircuitOpen}
	for _, targetState := range attemptedStates {
		stream.transitionState(targetState, "attempt to leave stopped state")

		// State should still be Stopped
		assert.Equal(t, StateStopped, stream.GetProcessState(),
			"should remain stopped after attempting transition to %s", targetState.String())
	}

	// Only idempotent transition (Stopped -> Stopped) should be allowed
	stream.transitionState(StateStopped, "idempotent transition")

	// State should still be Stopped (idempotent)
	assert.Equal(t, StateStopped, stream.GetProcessState(),
		"State after idempotent transition")

	// History should not change for idempotent transition (they're ignored)
	finalHistory := stream.GetStateHistory()
	assert.Len(t, finalHistory, initialHistoryLen,
		"idempotent should be ignored")
}
