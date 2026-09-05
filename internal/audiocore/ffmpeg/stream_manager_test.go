package ffmpeg

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore"
)

// TestStreamManager_FFmpegConformance asserts that *Manager satisfies the
// producer-neutral audiocore.StreamManager seam the engine drives it through.
// The compile-time guard lives in manager.go; this exercises the assignment at
// runtime so the conformance is covered by the test suite too.
func TestStreamManager_FFmpegConformance(t *testing.T) {
	var mgr audiocore.StreamManager = NewManager(t.Context(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = mgr.Shutdown() })
	require.NotNil(t, mgr)
	assert.Empty(t, mgr.GetActiveStreamIDs())
}

// TestProcessStateToStreamState verifies that every FFmpeg process state maps to
// the intended neutral StreamState and that the process name round-trips through
// StateDetail so the legacy process_state string stays byte-identical.
func TestProcessStateToStreamState(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		ps          ProcessState
		wantState   audiocore.StreamState
		wantProcess string // legacy process_state string carried in StateDetail
	}{
		{"idle", StateIdle, audiocore.StreamStateStarting, ProcessStateIdle},
		{"starting", StateStarting, audiocore.StreamStateStarting, ProcessStateStarting},
		{"running", StateRunning, audiocore.StreamStateConnected, ProcessStateRunning},
		{"restarting", StateRestarting, audiocore.StreamStateReconnecting, ProcessStateRestarting},
		{"backoff", StateBackoff, audiocore.StreamStateReconnecting, ProcessStateBackoff},
		{"circuit_open", StateCircuitOpen, audiocore.StreamStateReconnecting, ProcessStateCircuitOpen},
		{"stopped", StateStopped, audiocore.StreamStateStopped, ProcessStateStopped},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.wantState, processStateToStreamState(tt.ps), "neutral state mapping")
			assert.Equal(t, tt.wantProcess, tt.ps.String(), "process name is preserved")

			// A health snapshot built the way GetHealth builds it must report the
			// original process name through the legacy process_state field.
			h := audiocore.StreamHealth{
				State:       processStateToStreamState(tt.ps),
				StateDetail: tt.ps.String(),
			}
			assert.Equal(t, tt.wantProcess, h.ProcessStateName(), "legacy process_state round-trip")
		})
	}
}
