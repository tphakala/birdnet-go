package stream

import (
	"crypto/x509"
	"fmt"
	"net"
	"time"

	audiostream "github.com/tphakala/go-audio-stream"
	"github.com/tphakala/go-audio-stream/rtsp"
	"github.com/tphakala/go-audio-stream/supervisor"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// Legacy process_state sub-state names carried in StreamHealth.StateDetail so the
// health API and frontend keep switching on the same strings the FFmpeg producer
// emitted. The frontend already styles each of these.
const (
	detailStarting   = "starting"
	detailRunning    = "running"
	detailRestarting = "restarting"
	detailBackoff    = "backoff"
	detailStopped    = "stopped"
	detailFailed     = "failed"
)

// Error type tags mirror the FFmpeg stderr classifier so the health API and any
// error-type searches keep working across the producer switch. The tags that
// have a shared audiocore.ErrType* constant reference it directly, so the two
// producers cannot drift on those values; the rest are native-specific vocabulary
// with no FFmpeg equivalent.
const (
	errTypeNoAudioStream        = "no_audio_stream"
	errTypeUnsupportedCodec     = "unsupported_codec"
	errTypeAuthFailed           = audiocore.ErrTypeAuthFailed
	errTypeReadTimeout          = "read_timeout"
	errTypeConnectionTimeout    = audiocore.ErrTypeConnectionTimeout
	errTypeConnectionReset      = "connection_reset"
	errTypeConnectionRefused    = audiocore.ErrTypeConnectionRefused
	errTypeRedirect             = "redirect"
	errTypeTLSVerifyFailed      = "tls_verify_failed"
	errTypeUDPTransportRejected = "udp_transport_rejected"
	errTypeInvalidURL           = "invalid_url"
	errTypeStreamError          = "stream_error"
)

// trackAggregate is the per-session RTP stats summed across the live tracks for
// the observability snapshot.
type trackAggregate struct {
	packets    uint64
	seqGaps    uint64
	duplicates uint64
	malformed  uint64
	ssrcResets uint64
	wire       uint64
	payload    uint64

	lastFrameAt      time.Time
	senderClockValid bool
	senderClockAge   time.Duration
}

// aggregateTrackStats sums the go-audio-stream per-track counters into one
// per-session view. The newest valid SenderClock across tracks wins; its age is
// measured against the stats capture time.
func aggregateTrackStats(stats audiostream.Stats) trackAggregate {
	var agg trackAggregate
	var newestSR time.Time
	for _, t := range stats.Tracks { //nolint:gocritic // rangeValCopy: TrackStats is a map value (no pointer iteration) and tracks per session are few (1-2)
		agg.packets += t.Packets
		agg.seqGaps += t.SeqGaps
		agg.duplicates += t.Duplicates
		agg.malformed += t.Malformed
		agg.ssrcResets += t.SSRCResets
		agg.wire += t.WireBytes
		agg.payload += t.PayloadBytes
		if t.LastFrameAt.After(agg.lastFrameAt) {
			agg.lastFrameAt = t.LastFrameAt
		}
		if t.SenderClock.Valid && t.SenderClock.ReceivedAt.After(newestSR) {
			newestSR = t.SenderClock.ReceivedAt
			agg.senderClockValid = true
			if !stats.CapturedAt.IsZero() {
				if age := stats.CapturedAt.Sub(t.SenderClock.ReceivedAt); age > 0 {
					agg.senderClockAge = age
				}
			}
		}
	}
	return agg
}

// mapState maps a supervisor lifecycle transition onto the neutral connection
// model: the StreamState, the legacy process_state detail string, and the
// RecoveryState the liveness watchdog consults. FFmpeg never reports these
// values, so under the ffmpeg gate the watchdog keeps its legacy behaviour.
func mapState(sc supervisor.StateChange) (state audiocore.StreamState, detail string, recovery audiocore.RecoveryState) {
	switch sc.State {
	case supervisor.StateConnecting:
		if sc.Attempt > 0 {
			return audiocore.StreamStateReconnecting, detailRestarting, audiocore.RecoveryInProgress
		}
		return audiocore.StreamStateStarting, detailStarting, audiocore.RecoveryInProgress
	case supervisor.StateConnected:
		return audiocore.StreamStateConnected, detailRunning, audiocore.RecoveryIdle
	case supervisor.StateReconnecting:
		return audiocore.StreamStateReconnecting, detailBackoff, audiocore.RecoveryInProgress
	case supervisor.StateClosed:
		return audiocore.StreamStateStopped, detailStopped, audiocore.RecoveryUnknown
	case supervisor.StateFailed:
		return audiocore.StreamStateFailed, detailFailed, audiocore.RecoveryGivenUp
	default:
		return audiocore.StreamStateStarting, detailStarting, audiocore.RecoveryUnknown
	}
}

// classifyError maps a typed session-ending error to a producer-neutral
// StreamErrorContext, the native counterpart of the FFmpeg stderr classifier. It
// returns nil for a nil error. host and port are the parsed target with any
// userinfo already stripped by the caller.
func classifyError(err error, host string, port int) *audiocore.StreamErrorContext {
	if err == nil {
		return nil
	}
	ctx := &audiocore.StreamErrorContext{
		PrimaryMessage: err.Error(),
		TargetHost:     host,
		TargetPort:     port,
		Timestamp:      time.Now(),
	}
	classifyInto(ctx, err)
	if ctx.UserFacingMsg == "" {
		ctx.UserFacingMsg = "The audio stream connection failed."
	}
	return ctx
}

// classifyInto fills ErrorType, HTTPStatus, and the user-facing text on ctx from
// the concrete error. The order matters: the most specific typed errors are
// tested before the broad sentinels.
func classifyInto(ctx *audiocore.StreamErrorContext, err error) {
	var respErr *rtsp.ResponseError
	var unauthErr *rtsp.UnauthorizedError
	var redirectErr *audiostream.RedirectError
	var certErr *x509.CertificateInvalidError
	var authorityErr x509.UnknownAuthorityError
	var netErr net.Error
	isNetTimeout := errors.As(err, &netErr) && netErr.Timeout()

	switch {
	case errors.Is(err, ErrNoAudioTrack):
		ctx.ErrorType = errTypeNoAudioStream
		ctx.UserFacingMsg = "The stream has no supported audio track."
		ctx.TroubleShooting = []string{"Confirm the camera or restreamer publishes an audio track.", "Check that the audio codec is AAC-LC, Opus, G.711, or L16."}
	case errors.Is(err, ErrUnsupportedCodec):
		ctx.ErrorType = errTypeUnsupportedCodec
		ctx.UserFacingMsg = "The stream uses an audio codec native ingest cannot decode."
		ctx.TroubleShooting = []string{"Reconfigure the source to AAC-LC, Opus, G.711, or L16.", "Or unset BIRDNET_STREAM_INGEST to fall back to FFmpeg."}
	case errors.Is(err, rtsp.ErrAuthFailed), errors.Is(err, rtsp.ErrUnauthorized), errors.As(err, &unauthErr):
		ctx.ErrorType = errTypeAuthFailed
		ctx.UserFacingMsg = "Authentication with the stream failed."
		ctx.TroubleShooting = []string{"Check the username and password in the stream URL."}
	case errors.As(err, &respErr):
		ctx.ErrorType = fmt.Sprintf("rtsp_%d", respErr.Code)
		ctx.HTTPStatus = respErr.Code
		ctx.UserFacingMsg = fmt.Sprintf("The stream server returned %d %s.", respErr.Code, respErr.Reason)
	case errors.As(err, &redirectErr):
		ctx.ErrorType = errTypeRedirect
		ctx.UserFacingMsg = "The stream server issued a redirect."
	case errors.Is(err, rtsp.ErrUDPSetupRejected):
		ctx.ErrorType = errTypeUDPTransportRejected
		ctx.UserFacingMsg = "The server rejected the UDP transport."
	case errors.Is(err, audiostream.ErrReadTimeout):
		ctx.ErrorType = errTypeReadTimeout
		ctx.UserFacingMsg = "The stream stopped sending data."
	case errors.Is(err, rtsp.ErrRequestTimeout), isNetTimeout:
		ctx.ErrorType = errTypeConnectionTimeout
		ctx.UserFacingMsg = "The connection to the stream timed out."
	case errors.As(err, &certErr), errors.As(err, &authorityErr):
		ctx.ErrorType = errTypeTLSVerifyFailed
		ctx.UserFacingMsg = "The stream's TLS certificate could not be verified."
	case errors.Is(err, rtsp.ErrServerTeardown), errors.Is(err, rtsp.ErrConnectionClosed):
		ctx.ErrorType = classifyConnClosed(err)
		ctx.UserFacingMsg = "The stream connection was closed."
	case errors.Is(err, rtsp.ErrInvalidURL):
		ctx.ErrorType = errTypeInvalidURL
		ctx.UserFacingMsg = "The stream URL is not valid."
	default:
		ctx.ErrorType = errTypeStreamError
	}
}

// classifyConnClosed distinguishes a refused connection from a reset one by the
// wrapped syscall, defaulting to reset.
func classifyConnClosed(err error) string {
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return errTypeConnectionRefused
	}
	return errTypeConnectionReset
}
