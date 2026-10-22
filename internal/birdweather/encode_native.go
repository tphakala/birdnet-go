// encode_native.go implements the FFmpeg-free FLAC encoding path for BirdWeather
// soundscape uploads. It is the sole encoder for the FLAC-only upload API (see
// internal/audiocore/flac). Loudness is matched to the historical FFmpeg target
// (-23 LUFS) using go-audio-dsp loudnorm, which additionally applies true-peak
// limiting the old volume-filter path lacked.
package birdweather

import (
	"context"

	"github.com/tphakala/go-audio-dsp/loudnorm"

	"github.com/tphakala/birdnet-go/internal/audiocore/clipenc"
	"github.com/tphakala/birdnet-go/internal/audiocore/flac"
	"github.com/tphakala/birdnet-go/internal/audiocore/loudness"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// encodeWithNativeFLAC encodes PCM directly to an in-memory FLAC upload buffer
// using the native go-flac encoder and go-audio-dsp loudnorm loudness
// normalization, with no FFmpeg dependency. go-flac writes a finalized
// STREAMINFO total-samples field up front from the PCM length, so the
// non-seekable buffer encoder yields the correct soundscape duration with no
// temp-file round-trip. Pass 1 measures
// integrated loudness and true peak; the planned gain (clamped to
// +/-loudnorm.DefaultMaxGainDB) is then applied in Go while the original bytes
// are streamed into the encoder.
// Both passes run under one encodingTimeout, and a failure in either is logged
// through logFLACEncodingError, so a timeout or cancel is a WARN either way.
// pcmData is not modified.
func (b *BwClient) encodeWithNativeFLAC(pcmData []byte, timestamp string) (*audioEncodingResult, error) {
	log := GetLogger()

	// Bound the whole upload encode, measurement included, so a slow host (e.g. a
	// Raspberry Pi on a tired SD card) cannot hang the upload, matching the
	// FFmpeg path's timeout. loudnorm.PlanBytes checks the context between
	// chunks, so a soundscape that takes too long to measure fails here rather
	// than after the deadline.
	ctx, cancel := context.WithTimeout(context.Background(), encodingTimeout)
	defer cancel()

	// Pass 1: measure loudness + true peak directly from the PCM bytes (never
	// mutated) and plan the gain toward the soundscape target. The policy
	// (loudness.SoundscapePlanOptions) keeps the default -1 dBTP ceiling, clamps
	// to +/-loudnorm.DefaultMaxGainDB, the bound the FFmpeg path used, and has no
	// gate fallback: silence and sub-gate soundscapes yield GainDB == 0, so quiet
	// clips stay quiet instead of being boosted into noise.
	opts := loudness.SoundscapePlanOptions()
	plan, err := loudnorm.PlanBytes(ctx, pcmData, opts)
	if err != nil {
		logFLACEncodingError(err)
		return nil, errors.New(err).
			Component("birdweather").
			Category(errors.CategoryAudio).
			Context("operation", "native_flac_measure").
			Context("encoder", clipenc.NativeFLAC).
			Context("timestamp", timestamp).
			Build()
	}

	if plan.Clamped {
		if plan.PlannedGainDB > 0 {
			b.logGainLimit(log, "Limiting gain to prevent excessive amplification",
				"calculated_gain", plan.PlannedGainDB, "max_gain", opts.MaxGainDB)
		} else {
			b.logGainLimit(log, "Limiting gain to prevent excessive attenuation",
				"calculated_gain", plan.PlannedGainDB, "min_gain", -opts.MaxGainDB)
		}
	}

	log.Debug("Native FLAC loudness analysis",
		logger.Float64("measured_lufs", plan.Result.Input.IntegratedLUFS),
		logger.Float64("true_peak_dbtp", plan.Result.Input.TruePeakDBTP),
		logger.Float64("target_lufs", opts.TargetLUFS),
		logger.Float64("gain_db", plan.GainDB),
		logger.Bool("peak_limited", plan.PeakLimited))

	// Pass 2: apply the gain in Go and encode straight to an in-memory FLAC
	// buffer. go-flac finalizes STREAMINFO.total_samples up front from the PCM
	// length, so BirdWeather derives the correct soundscape duration without the
	// temp-file round-trip the encoder needed before.
	buf, err := flac.EncodePCMToBuffer(ctx, &flac.BufferOptions{
		PCMData:    pcmData,
		SampleRate: conf.SampleRate,
		Channels:   conf.NumChannels,
		BitDepth:   conf.BitDepth,
		GainDB:     plan.GainDB,
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
	// shared base handler. On a default install the birdweather module output is
	// disabled (the viper default and the shipped config.yaml both set
	// enabled: false), so these lines land in the default output and DO appear
	// there, which is what the matching noiseOperations entry is for. Enable the
	// birdweather module output and they go to its own file, which the endpoint
	// does not read.
	log.Info("Encoded audio to FLAC format (native go-flac)",
		logger.String("timestamp", timestamp),
		logger.String("encoder", clipenc.NativeFLAC),
		logger.Float64("gain_db", plan.GainDB),
		logger.Int("bytes", buf.Len()),
		logger.String("operation", "birdweather_soundscape_encode"))
	return &audioEncodingResult{buffer: buf, ext: "flac"}, nil
}
