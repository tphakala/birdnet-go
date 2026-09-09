package stream

import "github.com/tphakala/birdnet-go/internal/errors"

// Sentinel errors for the native ingest path. They are terminal (non-retryable)
// causes: the Retryable policy in stream.go marks them so the supervisor stops
// rather than reconnecting into the same failure, and the health snapshot then
// reports RecoveryGivenUp (which a liveness watchdog can consult once the
// watchdog-side coordination lands; it is not wired yet).

// ErrNoAudioTrack is returned when a negotiated session exposes no decodable
// audio track. A fresh connection will not grow one, so it is terminal. It maps
// to the "no_audio_stream" error type and replaces FFmpeg's silent stall.
var ErrNoAudioTrack = errors.Newf("stream has no supported audio track").
	Component("native-stream").Category(errors.CategoryAudioSource).Build()

// ErrUnsupportedCodec is returned when a track's codec cannot be decoded by the
// native path (FLAC over RTP, HE-AAC/SBR/PS, or an opaque payload). It maps to
// "unsupported_codec" with the codec named, and is terminal.
var ErrUnsupportedCodec = errors.Newf("stream codec is not supported by native ingest").
	Component("native-stream").Category(errors.CategoryAudioSource).Build()

// ErrUnsupportedType is returned by StartStream for a SourceType the native
// manager does not implement in this phase. RTSP is the only type native ingest
// handles in Phase 2; hls, http, udp, and rtmp hard-fail here.
var ErrUnsupportedType = errors.Newf("source type is not supported by native ingest").
	Component("native-stream").Category(errors.CategoryValidation).Build()

// ErrUDPCodecUnspecified is returned when a udp:// or rtp:// source carries a
// dynamic RTP payload type with no codec description to interpret it. UDP ingest
// arrives in Phase 3; this sentinel reserves the failure mode.
var ErrUDPCodecUnspecified = errors.Newf("udp stream needs an explicit codec description").
	Component("native-stream").Category(errors.CategoryConfiguration).Build()
