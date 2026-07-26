// encode_native.go implements the FFmpeg-free FLAC encoding path for BirdWeather
// soundscape uploads. It is the sole encoder for the FLAC-only upload API (see
// internal/audiocore/flac). Loudness is matched to the historical FFmpeg target
// (-23 LUFS) using the native audionorm library, which additionally applies
// true-peak limiting the old volume-filter path lacked.
package birdweather

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/audiocore/audionorm"
	"github.com/tphakala/birdnet-go/internal/audiocore/clipenc"
	"github.com/tphakala/birdnet-go/internal/audiocore/flac"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// encodeWithNativeFLAC encodes PCM directly to an in-memory FLAC upload buffer
// using the native go-flac encoder and audionorm loudness normalization, with no
// FFmpeg dependency. go-flac writes a finalized STREAMINFO total-samples field up
// front from the PCM length, so the non-seekable buffer encoder yields the
// correct soundscape duration with no temp-file round-trip. Pass 1 measures
// integrated loudness and true peak; the planned gain (clamped to
// +/-audionorm.DefaultMaxGainDB) is then applied in Go while the original bytes
// are streamed into the encoder.
// pcmData is not modified.
func (b *BwClient) encodeWithNativeFLAC(pcmData []byte, timestamp string) (*audioEncodingResult, error) {
	log := GetLogger()

	// Bound the encode pass so a slow host (e.g. a Raspberry Pi on a tired SD
	// card) cannot hang the upload, matching the FFmpeg path's timeout. The
	// pass-1 measurement below is pure in-memory CPU work and needs no deadline.
	ctx, cancel := context.WithTimeout(context.Background(), encodingTimeout)
	defer cancel()

	opts := audionorm.DefaultOptions()
	opts.SampleRate = conf.SampleRate
	opts.Channels = conf.NumChannels
	// Target the same loudness as the FFmpeg path. The default -1.0 dBTP ceiling
	// is kept, adding inter-sample-peak protection the volume filter lacked.
	opts.TargetLUFS = targetIntegratedLoudnessLUFS

	// Pass 1: measure loudness + true peak directly from the PCM bytes (never
	// mutated), plan the gain toward opts.TargetLUFS, and clamp to the same
	// +/-audionorm.DefaultMaxGainDB bound the FFmpeg path used. Silence yields
	// GainDB == 0 (audionorm returns -Inf LUFS), so quiet clips stay quiet instead
	// of being boosted into noise.
	gainDB, meas, res, limited, err := audionorm.PlanClampedGainInt16Bytes(pcmData, opts, audionorm.DefaultMaxGainDB)
	if err != nil {
		return nil, errors.New(err).
			Component("birdweather").
			Category(errors.CategoryAudio).
			Context("operation", "native_flac_measure").
			Context("encoder", clipenc.NativeFLAC).
			Context("timestamp", timestamp).
			Build()
	}

	if limited {
		if res.GainDB > 0 {
			b.logGainLimit(log, "Limiting gain to prevent excessive amplification",
				"calculated_gain", res.GainDB, "max_gain", audionorm.DefaultMaxGainDB)
		} else {
			b.logGainLimit(log, "Limiting gain to prevent excessive attenuation",
				"calculated_gain", res.GainDB, "min_gain", -audionorm.DefaultMaxGainDB)
		}
	}

	log.Debug("Native FLAC loudness analysis",
		logger.Float64("measured_lufs", meas.IntegratedLUFS),
		logger.Float64("true_peak_dbtp", meas.TruePeakDBTP),
		logger.Float64("target_lufs", opts.TargetLUFS),
		logger.Float64("gain_db", gainDB),
		logger.Bool("peak_limited", res.PeakLimited))

	// Pass 2: apply the gain in Go and encode straight to an in-memory FLAC
	// buffer. go-flac finalizes STREAMINFO.total_samples up front from the PCM
	// length, so BirdWeather derives the correct soundscape duration without the
	// temp-file round-trip the encoder needed before.
	buf, err := flac.EncodePCMToBuffer(ctx, &flac.BufferOptions{
		PCMData:    pcmData,
		SampleRate: conf.SampleRate,
		Channels:   conf.NumChannels,
		BitDepth:   conf.BitDepth,
		GainDB:     gainDB,
	})
	if err != nil {
		// logFLACEncodingError downgrades timeout/cancel to WARN to avoid Sentry
		// noise on slow hosts; everything else is logged at ERROR there.
		logFLACEncodingError(err)
		return nil, errors.New(err).
			Component("birdweather").
			Category(errors.CategoryAudio).
			Context("operation", "native_flac_encode").
			Context("timestamp", timestamp).
			Build()
	}

	// encoder and gain_db are on the Info line, not just the Debug one above, so
	// that a default-level support dump answers for uploads the two questions it
	// already answers for saved clips: which encoder produced the audio, and how
	// much gain it applied ("my BirdWeather uploads are too quiet"). The Debug
	// line keeps the full measurement detail.
	//
	// operation names the line for grep and for the system-events classifier,
	// and pairs with birdweather_soundscape_encode_failed (emitted by
	// logFLACEncodingError in birdweather_client.go) so success and failure are
	// distinguishable without parsing the message.
	//
	// Whether it reaches GET /api/v2/system/events/operational depends on the
	// logging configuration, and an earlier version of this comment wrongly said
	// it always does. That endpoint reads two configured base paths (plus their
	// rotated variants): GetDefaultOutputPath() and GetOutputPath("audio").
	// CentralLogger.Module routes to a module's own writer only when that module
	// has an output entry AND it is enabled, otherwise it falls back to the
	// shared base handler. On a default install birdweather gets its own file
	// (ensureModuleOutput, DefaultBirdweatherLogPath), so these lines land
	// outside what the endpoint reads and never appear there. Disable or
	// redirect the birdweather module output and they land in the default output
	// instead, at which point they DO appear, which is what the matching
	// noiseOperations entry is for.
	log.Info("Encoded audio to FLAC format (native go-flac)",
		logger.String("timestamp", timestamp),
		logger.String("encoder", clipenc.NativeFLAC),
		logger.Float64("gain_db", gainDB),
		logger.Int("bytes", buf.Len()),
		logger.String("operation", "birdweather_soundscape_encode"))
	return &audioEncodingResult{buffer: buf, ext: "flac"}, nil
}
