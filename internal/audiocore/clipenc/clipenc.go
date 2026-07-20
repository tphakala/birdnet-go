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
// This is a leaf package with no imports on purpose. The obvious alternative
// homes both cost more than they are worth for five strings: internal/conf
// documents its native-encoder gate file as scaffolding with a planned
// deletion, and the tags outlive the gate; internal/audiocore/ffmpeg already
// exports the sibling format constants but transitively pulls in datastore,
// telemetry, notification and alerting, none of which internal/birdweather
// depends on today.
//
// Deliberately limited to the tags. The routing that picks between them
// (selectEncoder in the processor) stays where it is until the native AAC/Opus
// gate is removed and the branching simplifies, so it is moved once rather than
// twice. These names survive that removal unchanged.
package clipenc

const (
	// FFmpeg is the external ffmpeg binary.
	FFmpeg = "ffmpeg"
	// NativeWAV is the in-tree WAV writer.
	NativeWAV = "native-wav"
	// NativeFLAC is the in-tree go-flac encoder.
	NativeFLAC = "native-flac"
	// NativeAAC is the in-tree go-aac encoder (with go-m4a for .m4a).
	NativeAAC = "native-aac"
	// NativeOpus is the in-tree go-opus encoder.
	NativeOpus = "native-opus"
)
