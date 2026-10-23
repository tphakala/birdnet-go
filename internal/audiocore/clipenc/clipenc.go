// Package clipenc holds the encoder identity tags recorded in the audio export
// logs, so that "encoder=" means the same thing on every path that writes or
// re-encodes audio and one grep answers the question across all of them.
//
// The tags used to be private constants in internal/analysis/processor, which
// covered the detection clip export only. The sibling paths (the BirdWeather
// soundscape upload and the on-demand clip transcode in the media API) fail
// independently of it, so a report of "BirdWeather uploads stopped working"
// produced no log line naming the encoder involved, and the triage that
// attribution makes possible for saved clips was unavailable for uploads.
//
// This is a leaf package on purpose: it imports only the standard-library-only
// audioformat package, because internal/audiocore/ffmpeg transitively pulls in
// datastore, telemetry, notification and alerting, none of which
// internal/birdweather depends on today.
//
// Deliberately limited to the tags and the format-to-tag lookup. The routing
// that picks between encoders (selectEncoder in the processor) stays in the
// processor.
package clipenc

import "github.com/tphakala/birdnet-go/internal/audiocore/audioformat"

const (
	// NativeWAV is the in-tree WAV writer.
	NativeWAV = "native-wav"
	// NativeFLAC is the in-tree go-flac encoder.
	NativeFLAC = "native-flac"
	// NativeAAC is the in-tree go-aac encoder (with go-m4a for .m4a).
	NativeAAC = "native-aac"
	// NativeOpus is the in-tree go-opus encoder.
	NativeOpus = "native-opus"
	// NativeMP3 is the in-tree go-mp3 encoder.
	NativeMP3 = "native-mp3"
)

// ForFormat returns the native encoder tag for an audioformat constant, and
// false for a format no native encoder handles.
func ForFormat(format string) (tag string, ok bool) {
	switch format {
	case audioformat.WAV:
		return NativeWAV, true
	case audioformat.FLAC:
		return NativeFLAC, true
	case audioformat.AAC:
		return NativeAAC, true
	case audioformat.Opus:
		return NativeOpus, true
	case audioformat.MP3:
		return NativeMP3, true
	default:
		return "", false
	}
}
