package audiocore

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestStreamState_String(t *testing.T) {
	t.Parallel()
	tests := []struct {
		state StreamState
		want  string
	}{
		{StreamStateStarting, "starting"},
		{StreamStateConnected, "connected"},
		{StreamStateReconnecting, "reconnecting"},
		{StreamStateStopped, "stopped"},
		{StreamStateFailed, "failed"},
		{StreamState(99), "unknown(99)"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.state.String())
		})
	}
}

func TestStreamState_LegacyProcessName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		state StreamState
		want  string
	}{
		{StreamStateStarting, "starting"},
		{StreamStateConnected, "running"},
		{StreamStateReconnecting, "restarting"},
		{StreamStateStopped, "stopped"},
		{StreamStateFailed, "failed"},
		// An out-of-range value falls back to the neutral String() form.
		{StreamState(99), "unknown(99)"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.state.LegacyProcessName())
		})
	}
}

func TestRecoveryState_String(t *testing.T) {
	t.Parallel()
	tests := []struct {
		state RecoveryState
		want  string
	}{
		{RecoveryUnknown, "unknown"},
		{RecoveryIdle, "idle"},
		{RecoveryInProgress, "in_progress"},
		{RecoveryGivenUp, "given_up"},
		{RecoveryState(99), "unknown(99)"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.state.String())
		})
	}
}

func TestStreamHealth_ProcessStateName(t *testing.T) {
	t.Parallel()

	t.Run("StateDetail overrides the state mapping", func(t *testing.T) {
		t.Parallel()
		// circuit_open has no neutral StreamState; only StateDetail preserves it.
		h := StreamHealth{State: StreamStateReconnecting, StateDetail: "circuit_open"}
		assert.Equal(t, "circuit_open", h.ProcessStateName())
	})

	t.Run("falls back to the legacy state mapping when detail is empty", func(t *testing.T) {
		t.Parallel()
		h := StreamHealth{State: StreamStateConnected}
		assert.Equal(t, "running", h.ProcessStateName())
	})
}

func TestStateTransition_Names(t *testing.T) {
	t.Parallel()

	t.Run("details override the state mapping", func(t *testing.T) {
		t.Parallel()
		tr := StateTransition{
			From:       StreamStateStarting,
			To:         StreamStateReconnecting,
			FromDetail: "starting",
			ToDetail:   "circuit_open",
		}
		assert.Equal(t, "starting", tr.FromName())
		assert.Equal(t, "circuit_open", tr.ToName())
	})

	t.Run("falls back to the legacy state mapping when details are empty", func(t *testing.T) {
		t.Parallel()
		tr := StateTransition{From: StreamStateConnected, To: StreamStateReconnecting}
		assert.Equal(t, "running", tr.FromName())
		assert.Equal(t, "restarting", tr.ToName())
	})
}

func TestDataRateMeter_Calculation(t *testing.T) {
	// Rate divides total bytes by the time span between the first and last
	// sample, returning 0 when that span is zero (div-by-zero guard). On
	// coarse-timer platforms (Windows, ~15ms resolution) three back-to-back
	// AddSample calls can share a timestamp, making the span zero and the rate
	// zero. Use synctest's fake clock advanced by time.Sleep so the samples get
	// distinct timestamps deterministically on every platform.
	synctest.Test(t, func(t *testing.T) {
		meter := NewDataRateMeter(DataRateWindowSize)

		meter.AddSample(1024)
		time.Sleep(time.Millisecond)
		meter.AddSample(2048)
		time.Sleep(time.Millisecond)
		meter.AddSample(1536)

		assert.Greater(t, meter.Rate(), 0.0)
	})
}

func TestDataRateMeter_EmptyRate(t *testing.T) {
	t.Parallel()

	meter := NewDataRateMeter(DataRateWindowSize)
	assert.InDelta(t, 0.0, meter.Rate(), 0.001)
}

func TestDataRateMeter_SingleSampleRate(t *testing.T) {
	t.Parallel()

	meter := NewDataRateMeter(DataRateWindowSize)
	meter.AddSample(1024)

	assert.InDelta(t, 1024.0, meter.Rate(), 0.01, "single recent sample should return its instantaneous rate")
}
