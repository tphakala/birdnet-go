// Package loudness holds BirdNET-Go's loudness normalization policy: the
// planning options each audio path hands to go-audio-dsp.
//
// It returns loudnorm.PlanOptions and carries no planning code. Callers run
// loudnorm.PlanBytes themselves, so error handling and logging stay with the
// caller and nothing here wraps the library. There are two policies:
//
//   - clip: detection clip export and on-demand transcode, with the gate
//     fallback on and a 60 dB clamp (ClipPlanOptions);
//   - soundscape: the BirdWeather upload, with no gate fallback and a 30 dB
//     clamp (SoundscapePlanOptions).
package loudness

import (
	"github.com/tphakala/go-audio-dsp/loudnorm"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// MaxClipGainDB is the gain clamp for detection clips and on-demand clips. It is
// the widest lift any valid clip target can ask for: the most aggressive
// permitted target (conf.MaxTargetLUFS, -10 LUFS) minus the EBU R128 absolute
// gate (loudnorm.AbsoluteGateLUFS, -70 LUFS), which is exactly 60 dB. It is
// deliberately not loudnorm.DefaultMaxGainDB, the BirdWeather upload bound. The
// on-demand request validation range (transcode.MaxGainDB) uses the same value.
const MaxClipGainDB = conf.MaxTargetLUFS - loudnorm.AbsoluteGateLUFS

// SoundscapeTargetLUFS is the integrated loudness the BirdWeather upload targets,
// the EBU R128 value the earlier FFmpeg path used.
const SoundscapeTargetLUFS = -23.0

// ClipPlanOptions returns the planning options for a detection clip or an
// on-demand clip. The target and ceiling come from the caller: the export path
// passes the hot-reloaded settings, on-demand transcode passes constants. The
// gate fallback is on, so a clip whose every 400 ms block sits under the
// absolute gate is still lifted instead of staying inaudible, and the gain is
// clamped to MaxClipGainDB.
func ClipPlanOptions(sampleRate, channels int, targetLUFS, truePeakDBTP float64) loudnorm.PlanOptions {
	return loudnorm.PlanOptions{
		SampleRate:   sampleRate,
		Channels:     channels,
		TargetLUFS:   targetLUFS,
		TruePeakDBTP: truePeakDBTP,
		MaxGainDB:    MaxClipGainDB,
		GateFallback: true,
	}
}

// SoundscapePlanOptions returns the planning options for the BirdWeather
// soundscape upload: capture format from conf, SoundscapeTargetLUFS, the
// library's default -1 dBTP ceiling (inter-sample-peak protection the earlier
// volume filter lacked), and a loudnorm.DefaultMaxGainDB clamp, the bound the
// FFmpeg path used so a near-silent clip is not amplified into loud static.
// The gate fallback is off by design: a sub-gate soundscape is left untouched
// rather than boosted into noise.
func SoundscapePlanOptions() loudnorm.PlanOptions {
	return loudnorm.PlanOptions{
		SampleRate:   conf.SampleRate,
		Channels:     conf.NumChannels,
		TargetLUFS:   SoundscapeTargetLUFS,
		TruePeakDBTP: loudnorm.DefaultTruePeakDBTP,
		MaxGainDB:    loudnorm.DefaultMaxGainDB,
		GateFallback: false,
	}
}
