package conf

import (
	"os"
	"strings"
)

// Temporary runtime opt-in for the native Go AAC encoder.
//
// AAC clip export still runs through FFmpeg by default. Setting
// BIRDNET_AAC_ENCODER=native switches it to the pure-Go encoder and muxer
// (go-aac plus go-m4a for .m4a) so it can be exercised in the field before it
// becomes the default.
//
// Opus clip export and HLS live streaming have already earned that confidence.
// go-opus is the unconditional encoder for .opus (FFmpeg is used only as a
// fallback when go-opus cannot carry a clip's shape; see nativeOpusSelected in
// the analysis processor). HLS live streaming is served unconditionally by the
// in-process go-hls muxer; its BIRDNET_HLS_ENCODER gate and the entire FFmpeg
// HLS output path have both been removed.
//
// This lives in conf rather than in a package of its own so that every consumer
// reaches it without a new dependency edge: the export-format validation here
// and the encoder dispatch in the analysis processor already depend on conf. A
// dedicated package under audiocore would make conf import audiocore, which
// inverts the layering and widens the deliberately exact internal closure that
// internal/diagnostics guards.
//
// REMOVAL: this file is scaffolding with a planned end of life. Once the native
// AAC encoder has earned field confidence, delete its gate along with the branch
// that reads it (exportFormatNeedsFFmpeg and SaveAudioAction.encodeClip); the
// native path becomes unconditional and the FFmpeg branch goes away with it.
//
// Nothing else depends on this file, and it deliberately holds no other logic
// so that each removal stays a mechanical edit.
const (
	// EnvNativeAACEncoder selects the native AAC encoder for .m4a clip export.
	EnvNativeAACEncoder = "BIRDNET_AAC_ENCODER"

	// nativeEncoderValue is the only value that enables a native encoder.
	// Anything else, including an unset variable, keeps the FFmpeg path.
	nativeEncoderValue = "native"
)

// NativeAACEncoderEnabled reports whether AAC clip export should use the native
// encoder.
func NativeAACEncoderEnabled() bool { return nativeEncoderSelected(EnvNativeAACEncoder) }

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
	return strings.EqualFold(strings.TrimSpace(os.Getenv(env)), nativeEncoderValue)
}
