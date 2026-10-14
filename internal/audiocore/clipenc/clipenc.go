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
// This is a leaf package with no imports on purpose: internal/audiocore/ffmpeg
// already exports the sibling format constants but transitively pulls in
// datastore, telemetry, notification and alerting, none of which
// internal/birdweather depends on today.
//
// Deliberately limited to the tags. The routing that picks between them
// (selectEncoder in the processor) stays in the processor.
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
	// NativeMP3 is the in-tree go-mp3 encoder.
	NativeMP3 = "native-mp3"
)
