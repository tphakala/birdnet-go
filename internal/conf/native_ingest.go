package conf

import (
	"os"
	"strings"
)

// Runtime opt-in for the native Go network stream ingest path.
//
// Network stream ingest (RTSP today; HLS, HTTP, and UDP in later phases) runs
// through an FFmpeg subprocess by default. Setting BIRDNET_STREAM_INGEST=native
// switches it to the pure-Go go-audio-stream path (depacketize, then decode via
// go-aac, go-opus, or go-mp3, resample, and dispatch), exercised in the field
// behind this gate before it becomes the default.
//
// The gate is read live per call, matching the encoder gates in
// native_encoders.go. An environment variable cannot change inside a running
// process, so a flip takes effect at the next service start (Docker --env,
// systemd drop-in) or the next pipeline restart that rebuilds the audio engine.
//
// REMOVAL: at promotion this inverts to an FFmpegStreamIngestForced() reading
// the value "ffmpeg", native becomes the default, and this doc gets the same
// removal note the encoder gates carry.
const EnvNativeStreamIngest = "BIRDNET_STREAM_INGEST"

// nativeSelectValue is the only value that opts into a native code path. It is
// shared by the ingest gate here and the encoder gates in native_encoders.go.
// Anything else, including an unset variable, keeps the FFmpeg path.
const nativeSelectValue = "native"

// NativeStreamIngestEnabled reports whether network stream ingest should use the
// pure-Go go-audio-stream path instead of the FFmpeg subprocess. Only the value
// "native" (case-insensitive, whitespace trimmed) enables it; unset or any other
// value keeps FFmpeg. Read live per call.
func NativeStreamIngestEnabled() bool { return nativeSelected(EnvNativeStreamIngest) }

// nativeSelected reads env and reports whether it opts into the native path.
// Matching is case-insensitive and tolerates surrounding whitespace, because
// these are hand-edited in compose files and systemd unit drop-ins where a stray
// space is easy to introduce and hard to spot.
//
// The value is read per call rather than cached at startup, so the gate stays
// consistent with the rest of BirdNET-Go's settings, which take effect without a
// restart. It lives here rather than in native_encoders.go so the encoder
// scaffolding stays independently deletable while this shared matcher survives.
func nativeSelected(env string) bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(env)), nativeSelectValue)
}
