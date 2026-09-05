package stream

import (
	"crypto/x509"
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	audiostream "github.com/tphakala/go-audio-stream"
	"github.com/tphakala/go-audio-stream/rtsp"
	"github.com/tphakala/go-audio-stream/supervisor"

	"github.com/tphakala/birdnet-go/internal/audiocore"
)

func TestMapState(t *testing.T) {
	tests := []struct {
		name         string
		sc           supervisor.StateChange
		wantState    audiocore.StreamState
		wantDetail   string
		wantRecovery audiocore.RecoveryState
	}{
		{
			name:         "first connect is starting",
			sc:           supervisor.StateChange{State: supervisor.StateConnecting, Attempt: 0},
			wantState:    audiocore.StreamStateStarting,
			wantDetail:   detailStarting,
			wantRecovery: audiocore.RecoveryInProgress,
		},
		{
			name:         "reconnect attempt is restarting",
			sc:           supervisor.StateChange{State: supervisor.StateConnecting, Attempt: 2},
			wantState:    audiocore.StreamStateReconnecting,
			wantDetail:   detailRestarting,
			wantRecovery: audiocore.RecoveryInProgress,
		},
		{
			name:         "connected is running and idle",
			sc:           supervisor.StateChange{State: supervisor.StateConnected},
			wantState:    audiocore.StreamStateConnected,
			wantDetail:   detailRunning,
			wantRecovery: audiocore.RecoveryIdle,
		},
		{
			name:         "reconnecting is backoff and in-progress",
			sc:           supervisor.StateChange{State: supervisor.StateReconnecting, Attempt: 1},
			wantState:    audiocore.StreamStateReconnecting,
			wantDetail:   detailBackoff,
			wantRecovery: audiocore.RecoveryInProgress,
		},
		{
			name:         "closed is stopped and unknown",
			sc:           supervisor.StateChange{State: supervisor.StateClosed},
			wantState:    audiocore.StreamStateStopped,
			wantDetail:   detailStopped,
			wantRecovery: audiocore.RecoveryUnknown,
		},
		{
			name:         "failed is failed and given-up",
			sc:           supervisor.StateChange{State: supervisor.StateFailed},
			wantState:    audiocore.StreamStateFailed,
			wantDetail:   detailFailed,
			wantRecovery: audiocore.RecoveryGivenUp,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, detail, recovery := mapState(tt.sc)
			assert.Equal(t, tt.wantState, state, "state")
			assert.Equal(t, tt.wantDetail, detail, "detail")
			assert.Equal(t, tt.wantRecovery, recovery, "recovery")
		})
	}
}

func TestClassifyError(t *testing.T) {
	const host, port = "cam.local", 554

	t.Run("nil error yields nil context", func(t *testing.T) {
		assert.Nil(t, classifyError(nil, host, port))
	})

	tests := []struct {
		name     string
		err      error
		wantType string
		wantHTTP int
	}{
		{name: "no audio track", err: fmt.Errorf("setup: %w", ErrNoAudioTrack), wantType: errTypeNoAudioStream},
		{name: "unsupported codec", err: fmt.Errorf("decode: %w", ErrUnsupportedCodec), wantType: errTypeUnsupportedCodec},
		{name: "rtsp auth", err: fmt.Errorf("describe: %w", rtsp.ErrAuthFailed), wantType: errTypeAuthFailed},
		{name: "read timeout", err: fmt.Errorf("wait: %w", audiostream.ErrReadTimeout), wantType: errTypeReadTimeout},
		{name: "udp rejected", err: fmt.Errorf("setup: %w", rtsp.ErrUDPSetupRejected), wantType: errTypeUDPTransportRejected},
		{name: "rtsp 404 response", err: fmt.Errorf("describe: %w", &rtsp.ResponseError{Code: 404, Reason: "Not Found"}), wantType: "rtsp_404", wantHTTP: 404},
		{name: "redirect", err: fmt.Errorf("describe: %w", &audiostream.RedirectError{Location: "rtsp://other.host/s"}), wantType: errTypeRedirect},
		{name: "request timeout", err: fmt.Errorf("dial: %w", rtsp.ErrRequestTimeout), wantType: errTypeConnectionTimeout},
		{name: "tls verify failed", err: fmt.Errorf("tls: %w", x509.UnknownAuthorityError{}), wantType: errTypeTLSVerifyFailed},
		{name: "invalid url", err: fmt.Errorf("dial: %w", rtsp.ErrInvalidURL), wantType: errTypeInvalidURL},
		{name: "connection closed maps to reset", err: fmt.Errorf("wait: %w", rtsp.ErrConnectionClosed), wantType: errTypeConnectionReset},
		{name: "server teardown maps to reset", err: fmt.Errorf("wait: %w", rtsp.ErrServerTeardown), wantType: errTypeConnectionReset},
		{name: "generic error", err: fmt.Errorf("something broke"), wantType: errTypeStreamError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := classifyError(tt.err, host, port)
			require.NotNil(t, ctx)
			assert.Equal(t, tt.wantType, ctx.ErrorType)
			assert.Equal(t, host, ctx.TargetHost)
			assert.Equal(t, port, ctx.TargetPort)
			assert.NotEmpty(t, ctx.UserFacingMsg)
			assert.False(t, ctx.Timestamp.IsZero())
			if tt.wantHTTP != 0 {
				assert.Equal(t, tt.wantHTTP, ctx.HTTPStatus)
			}
		})
	}
}

func TestClassifyConnClosed(t *testing.T) {
	// A dial-phase net.OpError means the peer refused the connection.
	dialErr := &net.OpError{Op: "dial", Err: fmt.Errorf("connection refused")}
	assert.Equal(t, errTypeConnectionRefused, classifyConnClosed(dialErr), "dial OpError is a refusal")

	// A read/teardown with no dial OpError is a reset.
	assert.Equal(t, errTypeConnectionReset, classifyConnClosed(fmt.Errorf("peer went away")), "non-dial close is a reset")
	readErr := &net.OpError{Op: "read", Err: fmt.Errorf("reset by peer")}
	assert.Equal(t, errTypeConnectionReset, classifyConnClosed(readErr), "read OpError is a reset")
}
