package api

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
)

// TestSendAudioSourceReconfigure_RecoversFromClosedControlChan pins the recover in
// sendAudioSourceReconfigure. During a shutdown race the analysis layer closes the
// control channel while the controller still holds a copy of it, so the non-nil
// guard passes and the send hits a closed channel. That send must be recovered, not
// allowed to crash the process. A live (non-cancelled) context makes the select pick
// the send case deterministically rather than the ctx.Done() branch, so the closed
// channel is exercised on every run.
func TestSendAudioSourceReconfigure_RecoversFromClosedControlChan(t *testing.T) {
	closed := make(chan string)
	close(closed)

	c := &Controller{Core: &apicore.Core{}, controlChan: closed}
	c.SetTestContext(t.Context(), nil)

	require.NotPanics(t, c.sendAudioSourceReconfigure,
		"a send on a closed control channel must be recovered, not propagated")
}

// TestSendAudioSourceReconfigure_NilControlChanReturns confirms the nil-channel guard
// returns without touching the channel (no send, no panic) when no control channel
// was injected.
func TestSendAudioSourceReconfigure_NilControlChanReturns(t *testing.T) {
	c := &Controller{Core: &apicore.Core{}}
	c.SetTestContext(t.Context(), nil)

	require.NotPanics(t, c.sendAudioSourceReconfigure,
		"a nil control channel must return via the guard without panicking")
}
