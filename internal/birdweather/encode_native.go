// encode_native.go implements the FFmpeg-free FLAC encoding path for BirdWeather
// soundscape uploads. It is selected by the same BIRDNET_FLAC_ENCODER=native gate
// as the detection save path (see internal/audiocore/flac). Loudness is matched
// to the FFmpeg path's target (-23 LUFS) using the native audionorm library,
// which additionally applies true-peak limiting the old volume-filter path
// lacked.
package birdweather

import (
	"context"
	"encoding/binary"

	"github.com/tphakala/birdnet-go/internal/audiocore/audionorm"
	"github.com/tphakala/birdnet-go/internal/audiocore/flac"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// maxGainDB bounds the normalization gain, matching the FFmpeg path's safety
// limit so a near-silent clip is not over-amplified into loud static.
const maxGainDB = 30.0

// encodeWithNativeFLAC encodes PCM to a seekable temporary FLAC file, reads it
// into an upload buffer, and uses the native go-flac encoder and audionorm
// loudness normalization, with no FFmpeg dependency. The temp file round-trip is
// handled by encodeUploadAudioToBuffer through os.MkdirTemp/os.ReadFile so
// STREAMINFO total samples can be finalized. Pass 1 measures integrated loudness
// and true peak; the planned gain (clamped to +/-maxGainDB) is then applied in
// Go while the original bytes are streamed into the encoder. pcmData is not
// modified.
func (b *BwClient) encodeWithNativeFLAC(pcmData []byte, timestamp string) (*audioEncodingResult, error) {
	log := GetLogger()

	// Bound the encode pass so a slow host (e.g. a Raspberry Pi on a tired SD
	// card) cannot hang the upload, matching the FFmpeg path's timeout. The
	// pass-1 measurement below is pure in-memory CPU work and needs no deadline.
	ctx, cancel := context.WithTimeout(context.Background(), encodingTimeout)
	defer cancel()

	// Pass 1: measure loudness + true peak. Reads a throwaway int16 view of the
	// PCM; the original bytes are never mutated.
	samples := pcmInt16FromBytes(pcmData)
	meas, err := audionorm.MeasureInt16(samples, conf.SampleRate, conf.NumChannels)
	if err != nil {
		return nil, errors.New(err).
			Component("birdweather").
			Category(errors.CategoryAudio).
			Context("operation", "native_flac_measure").
			Context("timestamp", timestamp).
			Build()
	}

	opts := audionorm.DefaultOptions()
	opts.SampleRate = conf.SampleRate
	opts.Channels = conf.NumChannels
	// Target the same loudness as the FFmpeg path. The default -1.0 dBTP ceiling
	// is kept, adding inter-sample-peak protection the volume filter lacked.
	opts.TargetLUFS = targetIntegratedLoudnessLUFS

	res := audionorm.PlanGain(meas, opts)

	// Clamp to the same +/-30 dB bound the FFmpeg path used. Silence yields
	// GainDB == 0 (audionorm returns -Inf LUFS), so quiet clips stay quiet
	// instead of being boosted into noise.
	gainDB := res.GainDB
	switch {
	case gainDB > maxGainDB:
		b.logGainLimit(log, "Limiting gain to prevent excessive amplification",
			"calculated_gain", res.GainDB, "max_gain", maxGainDB)
		gainDB = maxGainDB
	case gainDB < -maxGainDB:
		b.logGainLimit(log, "Limiting gain to prevent excessive attenuation",
			"calculated_gain", res.GainDB, "min_gain", -maxGainDB)
		gainDB = -maxGainDB
	}

	log.Debug("Native FLAC loudness analysis",
		logger.Float64("measured_lufs", meas.IntegratedLUFS),
		logger.Float64("true_peak_dbtp", meas.TruePeakDBTP),
		logger.Float64("target_lufs", opts.TargetLUFS),
		logger.Float64("gain_db", gainDB),
		logger.Bool("peak_limited", res.PeakLimited))

	// Pass 2: apply the gain in Go and encode to seekable FLAC. BirdWeather
	// derives soundscape duration from FLAC STREAMINFO, so uploads must not use
	// the non-seekable buffer encoder that leaves total samples unknown.
	buf, err := encodeUploadAudioToBuffer("flac", func(outputPath string) error {
		return flac.EncodePCM(ctx, &flac.Options{
			PCMData:    pcmData,
			OutputPath: outputPath,
			SampleRate: conf.SampleRate,
			Channels:   conf.NumChannels,
			BitDepth:   conf.BitDepth,
			GainDB:     gainDB,
		})
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

	log.Info("Encoded audio to FLAC format (native go-flac)",
		logger.String("timestamp", timestamp),
		logger.Int("bytes", buf.Len()))
	return &audioEncodingResult{buffer: buf, ext: "flac"}, nil
}

// pcmInt16FromBytes reinterprets interleaved little-endian 16-bit PCM bytes as
// []int16 for loudness measurement. PCM samples are signed, so each pair is read
// as a uint16 and cast to int16 (a Uint16-only read would turn negative samples
// into large positives and corrupt the measurement). A trailing odd byte (not
// produced by real int16 PCM) is ignored. The returned slice is a copy; the
// source bytes are not modified.
func pcmInt16FromBytes(b []byte) []int16 {
	out := make([]int16, len(b)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(b[i*2:])) //nolint:gosec // G115: intentional uint16->int16 bit reinterpretation for signed PCM
	}
	return out
}
