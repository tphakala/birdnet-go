// Package opus encodes captured PCM to an Ogg Opus clip (.opus) using the
// pure-Go go-opus encoder, with no FFmpeg process involved.
//
// It mirrors the native FLAC encoder in internal/audiocore/flac: the same
// Options shape, the same atomic temp-file-then-rename write (via audiotemp),
// and the same
// enhanced-error conventions. Gain is applied in Go before encoding.
//
// This path is gated at the call site by internal/audiocore/nativeenc; Opus
// clip export still defaults to FFmpeg.
package opus

import (
	"context"
	"os"
	"slices"

	"github.com/tphakala/go-opus/oggopus"

	"github.com/tphakala/birdnet-go/internal/audiocore/audiotemp"
	"github.com/tphakala/birdnet-go/internal/audiocore/pcmgain"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	// component is the error-telemetry component name for this package.
	component = "audiocore/opus"

	// bitDepth16 is the only PCM bit depth go-opus accepts. oggopus.Config has
	// no bit-depth field at all: it reads its input as interleaved int16, so
	// Options.BitDepth never reaches the library and exists only to validate the
	// caller's buffer against that assumption.
	bitDepth16 = 16

	// bitsPerKilobit converts the configured kbps bitrate to the bits per second
	// go-opus expects.
	bitsPerKilobit = 1000
)

// supportedSampleRates are the input rates go-opus accepts. Opus always decodes
// at 48 kHz; these are the rates its resampler-free input path handles. Any
// other rate (notably 44100) must stay on FFmpeg, which resamples internally.
var supportedSampleRates = [...]int{8000, 12000, 16000, 24000, 48000}

// Options configures a native Opus export of an in-memory PCM buffer.
type Options struct {
	// PCMData is interleaved little-endian PCM (the capture buffer format).
	PCMData []byte
	// OutputPath is the final .opus file path; the temp file and rename are
	// internal.
	OutputPath string
	// SampleRate is the PCM sample rate in Hz.
	SampleRate int
	// Channels is the number of interleaved channels.
	Channels int
	// BitDepth is the PCM bit depth; only 16 is supported.
	BitDepth int
	// BitrateKbps is the target bitrate in kbit/s. Zero selects go-opus's
	// automatic rate.
	BitrateKbps int
	// GainDB is the volume adjustment in dB (0 = no change).
	GainDB float64
}

// Supports reports whether the native encoder can carry a clip of this shape,
// returning a descriptive error naming the offending value when it cannot. It
// is the single predicate callers gate on, so a caller cannot satisfy two of
// three constraints and assume the third.
func Supports(sampleRate, bitDepth, channels int) error {
	if !slices.Contains(supportedSampleRates[:], sampleRate) {
		return errors.Newf("opus: unsupported sample rate %d (supported: %v)", sampleRate, supportedSampleRates).
			Component(component).
			Category(errors.CategoryValidation).
			Context("operation", "opus_supports").
			Context("sample_rate", sampleRate).
			Build()
	}
	if bitDepth != bitDepth16 {
		return errors.Newf("opus: unsupported bit depth %d (supported: %d)", bitDepth, bitDepth16).
			Component(component).
			Category(errors.CategoryValidation).
			Context("operation", "opus_supports").
			Context("bit_depth", bitDepth).
			Build()
	}
	if channels < 1 || channels > 2 {
		return errors.Newf("opus: unsupported channel count %d (supported: 1, 2)", channels).
			Component(component).
			Category(errors.CategoryValidation).
			Context("operation", "opus_supports").
			Context("channels", channels).
			Build()
	}
	return nil
}

// EncodePCM encodes opts.PCMData to an Ogg Opus file at opts.OutputPath. The
// write is atomic: data is encoded to a unique per-export temp file and renamed
// on success, with the temp file removed on any failure. A non-zero GainDB is
// applied in Go before encoding; opts.PCMData itself is never modified.
//
// Ogg carries duration in per-page granule positions written as the stream
// progresses, so unlike the MP4 path this needs no seeking. It still writes
// through a temp file so a partially written clip is never visible at the final
// path.
//
// ctx is honoured before the temp file is created but not during encoding:
// go-opus's one-shot entry point runs to completion once started.
func EncodePCM(ctx context.Context, opts *Options) error {
	if opts == nil {
		return validationErr("opus encode: nil options")
	}
	if err := validateEncodeInput(opts); err != nil {
		return err
	}

	cfg := oggopus.Config{
		SampleRate: opts.SampleRate,
		Channels:   opts.Channels,
		Bitrate:    opts.BitrateKbps * bitsPerKilobit,
	}

	// Gain is applied up front because the library entry point takes the whole
	// clip; at 0 dB (the common case) Applied returns the source unchanged, so
	// no copy is made.
	pcm := pcmgain.Applied(opts.PCMData, opts.GainDB)

	// oggopus draws its encoder from an internal pool and streams Ogg pages as
	// it goes, so the encoded stream is never held in memory whole.
	// WriteFile classifies its own filesystem failures and passes a cancelled
	// context through raw; only the codec failure is tagged here.
	return audiotemp.WriteFile(ctx, component, opts.OutputPath, func(f *os.File) error {
		if encErr := oggopus.EncodeInterleaved(f, cfg, pcm); encErr != nil {
			return errors.New(encErr).
				Component(component).
				Category(errors.CategoryAudio).
				Context("operation", "opus_encode_stream").
				Context("sample_rate", opts.SampleRate).
				Context("channels", opts.Channels).
				Context("bitrate_kbps", opts.BitrateKbps).
				Build()
		}
		return nil
	})
}

// validateEncodeInput rejects options the encoder cannot honour, with a clear
// error rather than an opaque failure deep inside go-opus.
func validateEncodeInput(opts *Options) error {
	if len(opts.PCMData) == 0 {
		return validationErr("opus encode: empty PCM data")
	}
	if opts.OutputPath == "" {
		return validationErr("opus encode: empty output path")
	}
	if err := Supports(opts.SampleRate, opts.BitDepth, opts.Channels); err != nil {
		return err
	}
	if opts.BitrateKbps < 0 {
		return errors.Newf("opus encode: negative bitrate %d kbps", opts.BitrateKbps).
			Component(component).
			Category(errors.CategoryValidation).
			Context("operation", "opus_encode_validate").
			Context("bitrate_kbps", opts.BitrateKbps).
			Build()
	}
	// Reject a partial trailing frame early rather than letting it surface as an
	// opaque length error inside go-opus.
	if bytesPerFrame := (opts.BitDepth / 8) * opts.Channels; len(opts.PCMData)%bytesPerFrame != 0 {
		return errors.Newf("opus encode: PCM length %d is not a multiple of the %d-byte frame size (%d-bit x %d ch)",
			len(opts.PCMData), bytesPerFrame, opts.BitDepth, opts.Channels).
			Component(component).
			Category(errors.CategoryValidation).
			Context("operation", "opus_encode_validate").
			Context("pcm_len", len(opts.PCMData)).
			Context("bytes_per_frame", bytesPerFrame).
			Build()
	}
	return nil
}

func validationErr(msg string) error {
	return errors.Newf("%s", msg).
		Component(component).
		Category(errors.CategoryValidation).
		Context("operation", "opus_encode_validate").
		Build()
}
