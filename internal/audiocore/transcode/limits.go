package transcode

import (
	"math"
	"slices"
	"time"

	"github.com/tphakala/go-audio-dsp/loudnorm"

	"github.com/tphakala/birdnet-go/internal/audiocore/audioformat"
	"github.com/tphakala/birdnet-go/internal/audiocore/loudness"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// Request validation limits.
const (
	// MaxClipDurationSec is the maximum allowed clip duration in seconds.
	// Prevents memory exhaustion from very long extraction requests.
	MaxClipDurationSec = 300 // 5 minutes

	// MaxGainDB is the maximum allowed gain adjustment in dB, the same bound the
	// loudness normalization gain is clamped to.
	MaxGainDB = loudness.MaxClipGainDB

	// AudibleBatsOutputSampleRate is the sample rate (Hz) of the derived "audible
	// bats" review audio. Ultrasonic clips are time-expanded and then resampled to
	// this standard playback rate so any browser can play the result.
	AudibleBatsOutputSampleRate = 48000
)

// Resource bounds, checked before the buffers they protect are allocated.
const (
	// maxDecodedPCMBytes bounds decoded PCM, time-expanded PCM and PCM resampled
	// for an encoder, each counted in 16-bit output bytes. It covers the longest
	// configurable extended capture at 48 kHz mono (1200 s, 115.2 MB) and a 262 s
	// window at 256 kHz.
	maxDecodedPCMBytes = 128 << 20

	// maxSourceFileBytes bounds the stored source file, checked with Stat before
	// it is opened. It leaves room for a 16-bit WAV that holds maxDecodedPCMBytes
	// of audio, plus headroom.
	maxSourceFileBytes = 256 << 20

	// minSourceSampleRate is the lowest source rate accepted. It is the lowest
	// rate the loudness meter supports, so normalization cannot fail on it.
	minSourceSampleRate = loudnorm.MinSampleRate

	// maxSourceSampleRate is the highest source rate accepted. It sits above every
	// bat recorder rate in use and stops a corrupt header from sizing per-second
	// scratch, denoiser frames or loudness meters by gigabytes.
	maxSourceSampleRate = 1_000_000

	// maxM4ABoxBytes caps a single MP4 box body the M4A reader will buffer. The
	// library default is 256 MiB.
	maxM4ABoxBytes = 32 << 20

	// maxSourceChannels is the highest channel count accepted. Clips are mono.
	maxSourceChannels = 1
)

// Pipeline timeouts, kept at their values from the FFmpeg-based implementation.
const (
	clipMinTimeout    = 30 * time.Second
	clipMaxTimeout    = 10 * time.Minute
	clipTimeoutFactor = 2

	// processingTimeout bounds a whole-file process request.
	processingTimeout = 60 * time.Second

	// audibleBatsTimeout bounds the time expansion and the normalize and gain
	// pass together (60 s each before).
	audibleBatsTimeout = 120 * time.Second
)

// Normalization targets, matching the loudnorm settings the FFmpeg path used:
// the default integrated loudness (-23 LUFS) and the default -2 dBTP ceiling.
// On-demand clips use these defaults, not the hot-reloadable export settings,
// because the processing cache key (internal/api/v2/media/process_cache.go)
// does not include the target, so a settings change would serve stale cached
// audio. The gain clamp is loudness.MaxClipGainDB.
const (
	normalizeTargetLUFS   = conf.DefaultNormalizationTargetLUFS
	normalizeTruePeakDBTP = conf.DefaultNormalizationTruePeak
)

// Streaming chunk size, in frames, for the decode, denoise and resample loops.
// The context is checked once per chunk.
const chunkFrames = 1 << 16

// clipBitrateKbps are the clip-specific bitrates for the lossy formats.
var clipBitrateKbps = map[string]int{
	audioformat.MP3:  128,
	audioformat.Opus: 64,
	audioformat.AAC:  96,
}

// validBatExpansionFactors is the set of time-expansion factors offered for
// audible-bats playback. A larger factor slows the audio more, lowering
// ultrasonic calls further into the human hearing range.
var validBatExpansionFactors = []int{5, 10, 16, 20}

// BatExpansionFactors returns the offered time-expansion factors in ascending order.
func BatExpansionFactors() []int {
	return slices.Clone(validBatExpansionFactors)
}

// IsSupportedClipFormat reports whether format is a supported clip export format.
func IsSupportedClipFormat(format string) bool {
	_, ok := fileEncoders[format]
	return ok || format == audioformat.WAV
}

// IsValidDenoisePreset returns true if the preset name is valid (including empty for "off").
func IsValidDenoisePreset(preset string) bool {
	if preset == "" {
		return true
	}
	_, ok := denoiseStrengths[preset]
	return ok
}

// IsValidGainDB returns true if the gain value is within the allowed range and not NaN/Inf.
func IsValidGainDB(gainDB float64) bool {
	return !math.IsNaN(gainDB) && !math.IsInf(gainDB, 0) && gainDB >= -MaxGainDB && gainDB <= MaxGainDB
}

// IsValidBatExpansionFactor reports whether n is an offered time-expansion factor.
func IsValidBatExpansionFactor(n int) bool {
	return slices.Contains(validBatExpansionFactors, n)
}
