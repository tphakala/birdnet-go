// Package nativeenc holds the temporary runtime opt-in that selects BirdNET-Go's
// native Go encoders over FFmpeg for the lossy clip export formats.
//
// AAC and Opus clip export still runs through FFmpeg by default. Setting
// BIRDNET_AAC_ENCODER=native or BIRDNET_OPUS_ENCODER=native switches the
// matching format to the pure-Go encoder (go-aac plus go-m4a for .m4a, go-opus
// for .opus) so it can be exercised in the field before it becomes the default.
// The two gates are independent, so one codec can be promoted while the other
// is still proving itself.
//
// REMOVAL: this package is scaffolding with a planned end of life. Once both
// native encoders have earned field confidence, delete the package and the
// gate checks at the call sites in SaveAudioAction.encodeClip; the native
// branches become unconditional and the FFmpeg branches for AAC and Opus go
// away with them. Nothing else in the tree depends on this package, and it
// deliberately holds no other logic so that removal stays a mechanical edit.
package nativeenc

import (
	"os"
	"strings"
)

// Environment variables that opt a format into its native encoder.
const (
	// EnvAACEncoder selects the native AAC encoder for .m4a clip export.
	EnvAACEncoder = "BIRDNET_AAC_ENCODER"
	// EnvOpusEncoder selects the native Opus encoder for .opus clip export.
	EnvOpusEncoder = "BIRDNET_OPUS_ENCODER"

	// valueNative is the only value that enables a native encoder. Anything
	// else, including an unset variable, keeps the FFmpeg path.
	valueNative = "native"
)

// AACEnabled reports whether AAC clip export should use the native encoder.
func AACEnabled() bool { return nativeSelected(EnvAACEncoder) }

// OpusEnabled reports whether Opus clip export should use the native encoder.
func OpusEnabled() bool { return nativeSelected(EnvOpusEncoder) }

// nativeSelected reads env and reports whether it opts into the native encoder.
// Matching is case-insensitive and tolerates surrounding whitespace, because
// these are hand-edited in compose files and systemd unit drop-ins where a
// stray space is easy to introduce and hard to spot.
//
// The value is read per call rather than cached at startup. A clip export
// happens once per detection, so the lookup cost is irrelevant, and reading it
// live keeps the gate consistent with the rest of BirdNET-Go's settings, which
// take effect without a restart.
func nativeSelected(env string) bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(env)), valueNative)
}
