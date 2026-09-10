package stream

import (
	"crypto/x509"
	"fmt"
	"net"
	"testing"
	"time"

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

// TestAggregateTrackStats verifies that the per-track go-audio-stream counters
// are summed across the live tracks, that SourceFiltered (added with the v0.5.0
// bump) is aggregated like its siblings, and that the newest valid RTCP Sender
// Report wins for the SenderClock age.
func TestAggregateTrackStats(t *testing.T) {
	t.Parallel()

	base := time.Now()
	stats := audiostream.Stats{
		CapturedAt: base.Add(10 * time.Second),
		Tracks: map[int]audiostream.TrackStats{
			0: {
				Packets:        100,
				PayloadBytes:   4000,
				WireBytes:      5000,
				SeqGaps:        2,
				Duplicates:     1,
				Malformed:      3,
				SSRCResets:     1,
				SourceFiltered: 7,
				LastFrameAt:    base.Add(5 * time.Second),
				SenderClock:    audiostream.SenderClock{Valid: true, ReceivedAt: base.Add(2 * time.Second)},
			},
			1: {
				Packets:        50,
				PayloadBytes:   2000,
				WireBytes:      2500,
				SeqGaps:        4,
				Duplicates:     0,
				Malformed:      1,
				SSRCResets:     2,
				SourceFiltered: 11,
				LastFrameAt:    base.Add(8 * time.Second),
				SenderClock:    audiostream.SenderClock{Valid: true, ReceivedAt: base.Add(6 * time.Second)},
			},
			2: {
				// A newer but INVALID sender report must not win: the .Valid guard
				// keeps the newest VALID report (track 1) as the clock source, so
				// senderClockAge stays 4s rather than following this 9s report.
				SenderClock: audiostream.SenderClock{Valid: false, ReceivedAt: base.Add(9 * time.Second)},
			},
		},
	}

	agg := aggregateTrackStats(stats)

	assert.Equal(t, uint64(150), agg.packets, "packets summed across tracks")
	assert.Equal(t, uint64(6000), agg.payload, "payload bytes summed across tracks")
	assert.Equal(t, uint64(7500), agg.wire, "wire bytes summed across tracks")
	assert.Equal(t, uint64(6), agg.seqGaps, "seq gaps summed across tracks")
	assert.Equal(t, uint64(1), agg.duplicates, "duplicates summed across tracks")
	assert.Equal(t, uint64(4), agg.malformed, "malformed summed across tracks")
	assert.Equal(t, uint64(3), agg.ssrcResets, "ssrc resets summed across tracks")
	assert.Equal(t, uint64(18), agg.sourceFiltered, "source-filtered datagrams summed across tracks")
	assert.WithinDuration(t, base.Add(8*time.Second), agg.lastFrameAt, time.Millisecond, "newest frame time wins")
	assert.True(t, agg.senderClockValid, "sender clock is valid when any track reports one")
	assert.Equal(t, 4*time.Second, agg.senderClockAge, "sender clock age measured from the newest report against CapturedAt")
}

// TestAggregateTrackStats_SenderClockGuards exercises the guard branches of the
// sender-clock age calculation: a report newer than the capture time (the age>0
// guard), a zero capture time (the !CapturedAt.IsZero() guard), and a newer valid
// report that wins the clock while carrying a non-positive age (which must not
// retain an earlier report's age). Every case must mark the clock valid yet leave
// the age at zero rather than producing a negative or stale value.
func TestAggregateTrackStats_SenderClockGuards(t *testing.T) {
	t.Parallel()

	base := time.Now()
	const (
		shortOffset   = 2 * time.Second
		captureOffset = 10 * time.Second
		// newerOffset lands after the capture (derived from it), so its report is the
		// newest yet carries a non-positive age against the capture time.
		newerOffset = captureOffset + shortOffset
	)

	tests := []struct {
		name       string
		capturedAt time.Time
		tracks     map[int]audiostream.TrackStats
	}{
		{
			name:       "report newer than capture yields no age",
			capturedAt: base,
			tracks: map[int]audiostream.TrackStats{
				0: {SenderClock: audiostream.SenderClock{Valid: true, ReceivedAt: base.Add(shortOffset)}},
			},
		},
		{
			name:       "zero capture time yields no age",
			capturedAt: time.Time{},
			tracks: map[int]audiostream.TrackStats{
				0: {SenderClock: audiostream.SenderClock{Valid: true, ReceivedAt: base}},
			},
		},
		{
			// The older valid report (before capture) is selected first; the newer
			// valid report then wins newestSR but sits after the capture. Computing the
			// age once after the loop keeps it at zero rather than the older report's
			// positive age, so the result does not depend on map iteration order.
			name:       "newer selected report with no positive age does not retain an older age",
			capturedAt: base.Add(captureOffset),
			tracks: map[int]audiostream.TrackStats{
				0: {SenderClock: audiostream.SenderClock{Valid: true, ReceivedAt: base.Add(shortOffset)}},
				1: {SenderClock: audiostream.SenderClock{Valid: true, ReceivedAt: base.Add(newerOffset)}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			agg := aggregateTrackStats(audiostream.Stats{CapturedAt: tt.capturedAt, Tracks: tt.tracks})
			assert.True(t, agg.senderClockValid, "a valid report still marks the clock valid")
			assert.Zero(t, agg.senderClockAge, "a guard case must leave the age at zero, not stale or negative")
		})
	}
}
