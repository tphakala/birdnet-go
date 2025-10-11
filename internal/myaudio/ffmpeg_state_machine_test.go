package myaudio

import (
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/privacy"
)

// createTestStream creates a test FFmpegStream instance
func createTestStream(url, transport string) *FFmpegStream {
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
			if got := tt.state.String(); got != tt.want {
				t.Errorf("ProcessState.String() = %v, want %v", got, tt.want)
			}
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
			if got := isValidTransition(tt.from, tt.to); got != tt.want {
				t.Errorf("isValidTransition(%s, %s) = %v, want %v",
					tt.from.String(), tt.to.String(), got, tt.want)
			}
		})
	}
}

// TestStateTransitionRecording tests that state transitions are recorded correctly
func TestStateTransitionRecording(t *testing.T) {
	stream := createTestStream("rtsp://test.local/stream", "tcp")

	// Initial state should be Idle
	if got := stream.GetProcessState(); got != StateIdle {
		t.Errorf("Initial state = %v, want %v", got, StateIdle)
	}

	// Transition to Starting
	stream.transitionState(StateStarting, "test transition")

	// Verify state changed
	if got := stream.GetProcessState(); got != StateStarting {
		t.Errorf("After transition, state = %v, want %v", got, StateStarting)
	}

	// Verify transition was recorded
	history := stream.GetStateHistory()
	if len(history) != 1 {
		t.Fatalf("Expected 1 transition in history, got %d", len(history))
	}

	transition := history[0]
	if transition.From != StateIdle {
		t.Errorf("Transition.From = %v, want %v", transition.From, StateIdle)
	}
	if transition.To != StateStarting {
		t.Errorf("Transition.To = %v, want %v", transition.To, StateStarting)
	}
	if transition.Reason != "test transition" {
		t.Errorf("Transition.Reason = %v, want %v", transition.Reason, "test transition")
	}
	if time.Since(transition.Timestamp) > time.Second {
		t.Errorf("Transition timestamp too old: %v", transition.Timestamp)
	}
}

// TestStateTransitionHistoryBounded tests that state history is bounded to 100 entries
func TestStateTransitionHistoryBounded(t *testing.T) {
	stream := createTestStream("rtsp://test.local/stream", "tcp")

	// Create more than 100 transitions
	for i := 0; i < 150; i++ {
		// Alternate between two states to avoid invalid transition warnings
		if i%2 == 0 {
			stream.transitionState(StateStarting, "test transition")
		} else {
			stream.transitionState(StateRunning, "test transition")
		}
	}

	// Verify history is bounded to 100
	history := stream.GetStateHistory()
	if len(history) != 100 {
		t.Errorf("History length = %d, want 100", len(history))
	}
}

// TestGetStateHistoryConcurrency tests that GetStateHistory returns a copy
func TestGetStateHistoryConcurrency(t *testing.T) {
	stream := createTestStream("rtsp://test.local/stream", "tcp")

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
	if len(history1) != 2 {
		t.Errorf("history1 length = %d, want 2 (should not be modified)", len(history1))
	}
	if len(history2) != 3 {
		t.Errorf("history2 length = %d, want 3", len(history2))
	}
}

// TestInvalidTransitionLogged tests that invalid transitions are logged but still applied
func TestInvalidTransitionLogged(t *testing.T) {
	stream := createTestStream("rtsp://test.local/stream", "tcp")

	// Set initial state to Running
	stream.processState = StateRunning

	// Try invalid transition: Running -> Idle (should be logged but applied)
	stream.transitionState(StateIdle, "invalid transition test")

	// State should still change despite being invalid
	if got := stream.GetProcessState(); got != StateIdle {
		t.Errorf("State after invalid transition = %v, want %v", got, StateIdle)
	}

	// Transition should still be recorded
	history := stream.GetStateHistory()
	if len(history) == 0 {
		t.Fatal("Expected transition to be recorded even if invalid")
	}
}

// TestStreamHealthIncludesState tests that StreamHealth includes process state
func TestStreamHealthIncludesState(t *testing.T) {
	stream := createTestStream("rtsp://test.local/stream", "tcp")

	// Set state to Running
	stream.transitionState(StateStarting, "starting")
	stream.transitionState(StateRunning, "running")

	// Get health
	health := stream.GetHealth()

	// Verify state is included
	if health.ProcessState != StateRunning {
		t.Errorf("Health.ProcessState = %v, want %v", health.ProcessState, StateRunning)
	}

	// Verify state history is included (last 10)
	if len(health.StateHistory) != 2 {
		t.Errorf("Health.StateHistory length = %d, want 2", len(health.StateHistory))
	}
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
			stream := createTestStream("rtsp://test.local/stream", "tcp")
			stream.processState = tt.state

			if got := stream.IsRestarting(); got != tt.want {
				t.Errorf("IsRestarting() with state %s = %v, want %v",
					tt.state.String(), got, tt.want)
			}
		})
	}
}

// TestStateTransitionConcurrency tests concurrent state transitions
func TestStateTransitionConcurrency(t *testing.T) {
	stream := createTestStream("rtsp://test.local/stream", "tcp")

	// Set initial state
	stream.processState = StateRunning

	// Perform concurrent transitions
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			stream.transitionState(StateRestarting, "concurrent test")
			stream.transitionState(StateStarting, "concurrent test")
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify we have transitions recorded (should be 20 total)
	history := stream.GetStateHistory()
	if len(history) != 20 {
		t.Errorf("History length = %d, want 20", len(history))
	}
}
