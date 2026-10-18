// Package audioformat defines the audio container and codec names shared by
// clip export and on-demand transcoding, plus the bitrate rule both apply.
// It depends on the standard library only, so any audio package can import it.
package audioformat

import (
	"strconv"
	"strings"
)

// Supported output format names.
const (
	WAV  = "wav"
	FLAC = "flac"
	MP3  = "mp3"
	AAC  = "aac"
	Opus = "opus"
)

// FileExtension returns the file extension, without the dot, that clip export
// gives format: the format name itself, except AAC, which is stored in an MP4
// container as .m4a.
func FileExtension(format string) string {
	if format == AAC {
		return "m4a"
	}
	return format
}

// exportFormats lists every format clip export can write, in the order
// FormatForExtension tries them. FormatForExtension compares each against FileExtension, but the list itself
// is kept by hand: a format added to FileExtension must be added here too.
//
//nolint:gochecknoglobals // immutable lookup table, never modified
var exportFormats = [...]string{WAV, FLAC, MP3, AAC, Opus}

// FormatForExtension is the inverse of FileExtension: it maps a clip file
// extension, without the leading dot, back to the export format name that
// writes it. The comparison ignores case. It returns ok=false for an empty or
// unknown extension, including "aac", which FileExtension never produces
// (AAC is stored as .m4a).
func FormatForExtension(ext string) (format string, ok bool) {
	for _, f := range exportFormats {
		if strings.EqualFold(ext, FileExtension(f)) {
			return f, true
		}
	}
	return "", false
}

// MinBatSampleRate is the minimum source capture rate in Hz for bat detection and
// audible bats playback: below it a recording cannot carry ultrasonic content.
const MinBatSampleRate = 96000

// Format-specific maximum bitrate limits (kbps). Requested bitrates above
// these values are clamped to prevent encoder errors or bloated output.
const (
	// maxBitrateOpusKbps is the maximum bitrate for Opus encoding.
	// Opus specification caps useful bitrate at 256 kbps for stereo.
	maxBitrateOpusKbps = 256

	// maxBitrateMP3Kbps is the maximum bitrate for MP3 encoding.
	// MPEG-1 Layer III maximum for 44.1/48 kHz stereo.
	maxBitrateMP3Kbps = 320
)

// parseBitrateKbps extracts the numeric portion of a bitrate string like "192k"
// and returns it as an integer (kbps). Returns 0 if the string cannot be parsed.
func parseBitrateKbps(bitrate string) int {
	s := strings.TrimSuffix(strings.ToLower(bitrate), "k")
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// EffectiveBitrateKbps returns the bitrate in kbit/s that an export of format
// actually uses: the configured string parsed numerically and, where the
// format defines a ceiling (Opus and MP3 do; AAC does not), clamped to it. It
// returns 0 when the string cannot be parsed, which every encoder reads as "use
// the codec default".
//
// Precondition: bitrate is the "NNNk" form that conf.validateExportBitrate
// enforces (32k..320k, suffix required, startup fails otherwise). A bare
// integer is read here as kbit/s.
func EffectiveBitrateKbps(format, bitrate string) int {
	kbps := parseBitrateKbps(bitrate)
	switch format {
	case Opus:
		return min(kbps, maxBitrateOpusKbps)
	case MP3:
		return min(kbps, maxBitrateMP3Kbps)
	}
	return kbps
}
