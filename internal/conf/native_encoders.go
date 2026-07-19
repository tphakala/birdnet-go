package conf

import (
	"os"
	"strings"
)

// Temporary runtime opt-in for the native Go clip encoders.
//
// AAC and Opus clip export still runs through FFmpeg by default. Setting
// BIRDNET_AAC_ENCODER=native or BIRDNET_OPUS_ENCODER=native switches the
// matching format to the pure-Go encoder (go-aac plus go-m4a for .m4a, go-opus
// for .opus) so it can be exercised in the field before it becomes the default.
// The two gates are independent, so one codec can be promoted while the other is
// still proving itself.
//
// This lives in conf rather than in a package of its own so that both consumers
// reach it without a new dependency edge: the export-format validation here, and
// the encoder dispatch in the analysis processor, already depend on conf. A
// dedicated package under audiocore would make conf import audiocore, which
// inverts the layering and widens the deliberately exact internal closure that
// internal/diagnostics guards.
//
// REMOVAL: this file is scaffolding with a planned end of life. Once both native
// encoders have earned field confidence, delete it along with the gate checks in
// exportFormatNeedsFFmpeg and in SaveAudioAction.encodeClip; the native branches
// become unconditional and the FFmpeg branches for AAC and Opus go away with
// them. Nothing else depends on it, and it deliberately holds no other logic so
// that removal stays a mechanical edit.
const (
	// EnvNativeAACEncoder selects the native AAC encoder for .m4a clip export.
	EnvNativeAACEncoder = "BIRDNET_AAC_ENCODER"
	// EnvNativeOpusEncoder selects the native Opus encoder for .opus clip export.
	EnvNativeOpusEncoder = "BIRDNET_OPUS_ENCODER"

	// nativeEncoderValue is the only value that enables a native encoder.
	// Anything else, including an unset variable, keeps the FFmpeg path.
	nativeEncoderValue = "native"
)

// NativeAACEncoderEnabled reports whether AAC clip export should use the native
// encoder.
func NativeAACEncoderEnabled() bool { return nativeEncoderSelected(EnvNativeAACEncoder) }

// NativeOpusEncoderEnabled reports whether Opus clip export should use the
// native encoder.
func NativeOpusEncoderEnabled() bool { return nativeEncoderSelected(EnvNativeOpusEncoder) }

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
