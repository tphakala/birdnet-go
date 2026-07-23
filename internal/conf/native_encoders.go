package conf

import (
	"os"
	"strings"
)

// Temporary runtime opt-in for the native Go encoders.
//
// AAC and Opus clip export, and HLS live streaming, still run through FFmpeg by
// default. Setting BIRDNET_AAC_ENCODER=native, BIRDNET_OPUS_ENCODER=native or
// BIRDNET_HLS_ENCODER=native switches the matching path to the pure-Go encoder
// (go-aac plus go-m4a for .m4a and for HLS, go-opus for .opus) so it can be
// exercised in the field before it becomes the default. The gates are
// independent, so one path can be promoted while another is still proving
// itself.
//
// This lives in conf rather than in a package of its own so that every consumer
// reaches it without a new dependency edge: the export-format validation here,
// the encoder dispatch in the analysis processor, and the HLS handler in the v2
// API already depend on conf. A dedicated package under audiocore would make
// conf import audiocore, which inverts the layering and widens the deliberately
// exact internal closure that internal/diagnostics guards.
//
// REMOVAL: this file is scaffolding with a planned end of life. Once a native
// encoder has earned field confidence, delete its gate along with the branch
// that reads it; the native path becomes unconditional and the FFmpeg branch
// goes away with it. The call sites are, per gate:
//
//	AAC:  exportFormatNeedsFFmpeg and SaveAudioAction.encodeClip
//	Opus: exportFormatNeedsFFmpeg and SaveAudioAction.encodeClip
//	HLS:  createHLSStream in internal/api/v2/audio/audio_hls.go, which routes to
//	      createNativeHLSStream in audio_hls_native.go. Removing the gate means
//	      deleting audio_hls.go's FFmpeg branch and the whole FFmpeg half of
//	      that package, not just the conditional.
//
// Nothing else depends on this file, and it deliberately holds no other logic
// so that each removal stays a mechanical edit.
const (
	// EnvNativeAACEncoder selects the native AAC encoder for .m4a clip export.
	EnvNativeAACEncoder = "BIRDNET_AAC_ENCODER"
	// EnvNativeOpusEncoder selects the native Opus encoder for .opus clip export.
	EnvNativeOpusEncoder = "BIRDNET_OPUS_ENCODER"
	// EnvNativeHLSEncoder selects the native encoder and muxer for HLS live
	// streaming, replacing the FFmpeg process that would otherwise encode,
	// segment and write the playlist for a live stream.
	EnvNativeHLSEncoder = "BIRDNET_HLS_ENCODER"

	// NativeEncoderValue is the only value that enables a native encoder.
	// Anything else, including an unset variable, keeps the FFmpeg path. It is
	// exported so a caller logging which encoder it selected can name the value
	// rather than restating the literal.
	NativeEncoderValue = "native"
)

// NativeAACEncoderEnabled reports whether AAC clip export should use the native
// encoder.
func NativeAACEncoderEnabled() bool { return nativeEncoderSelected(EnvNativeAACEncoder) }

// NativeOpusEncoderEnabled reports whether Opus clip export should use the
// native encoder.
func NativeOpusEncoderEnabled() bool { return nativeEncoderSelected(EnvNativeOpusEncoder) }

// NativeHLSEncoderEnabled reports whether HLS live streaming should use the
// native encoder and muxer instead of spawning an FFmpeg process.
func NativeHLSEncoderEnabled() bool { return nativeEncoderSelected(EnvNativeHLSEncoder) }

// nativeEncoderSelected reads env and reports whether it opts into the native
// encoder. Matching is case-insensitive and tolerates surrounding whitespace,
// because these are hand-edited in compose files and systemd unit drop-ins where
// a stray space is easy to introduce and hard to spot.
//
// The value is read per call rather than cached at startup. A clip export
// happens once per detection, so the lookup cost is irrelevant, and reading it
// live keeps the gate consistent with the rest of BirdNET-Go's settings, which
// take effect without a restart.
func nativeEncoderSelected(env string) bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(env)), NativeEncoderValue)
}
