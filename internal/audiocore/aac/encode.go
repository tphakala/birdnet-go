// Package aac encodes captured PCM to an AAC-LC clip in an MP4 (.m4a)
// container using the pure-Go go-aac encoder and go-m4a muxer, with no FFmpeg
// process involved.
//
// It mirrors the native FLAC encoder in internal/audiocore/flac: the same
// Options shape, the same atomic temp-file-then-rename write (via audiotemp),
// and the same
// enhanced-error conventions. Gain is applied in Go before encoding.
//
// This path is gated at the call site (see internal/conf/native_encoders.go); AAC clip
// export still defaults to FFmpeg.
package aac

import (
	"context"
	"os"
	"slices"

	aacpcm "github.com/tphakala/go-aac/pcm"
	"github.com/tphakala/go-m4a/aacm4a"

	"github.com/tphakala/birdnet-go/internal/audiocore/audiotemp"
	"github.com/tphakala/birdnet-go/internal/audiocore/pcmgain"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	// component is the error-telemetry component name for this package.
	component = "audiocore/aac"

	// bitsPerKilobit converts the configured kbps bitrate to the bits per second
	// go-aac expects.
	bitsPerKilobit = 1000

	// bitDepth16 is the only PCM bit depth this encoder accepts. go-aac itself
	// also codes 24- and 32-bit input, but pcmgain (and the audionorm
	// measurement feeding GainDB) operate on int16 samples, so a wider depth
	// would be silently reinterpreted as int16 the moment gain is non-zero.
	// Advertising only what the whole path can honour keeps that trap shut; the
	// capture pipeline is 16-bit throughout, so nothing is lost.
	bitDepth16 = 16
)

// supportedSampleRates are the input rates go-aac accepts. Anything else must
// stay on FFmpeg, which resamples internally.
var supportedSampleRates = [...]int{44100, 48000}

// Options configures a native AAC export of an in-memory PCM buffer.
type Options struct {
	// PCMData is interleaved little-endian PCM (the capture buffer format).
	PCMData []byte
	// OutputPath is the final .m4a file path; the temp file and rename are
	// internal.
	OutputPath string
	// SampleRate is the PCM sample rate in Hz.
	SampleRate int
	// Channels is the number of interleaved channels.
	Channels int
	// BitDepth is the PCM bit depth; only 16 is supported.
	BitDepth int
	// BitrateKbps is the target bitrate in kbit/s for the whole stream. Zero
	// selects go-aac's default.
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
		return errors.Newf("aac: unsupported sample rate %d (supported: %v)", sampleRate, supportedSampleRates).
			Component(component).
			Category(errors.CategoryValidation).
			Context("operation", "aac_supports").
			Context("sample_rate", sampleRate).
			Build()
	}
	if bitDepth != bitDepth16 {
		return errors.Newf("aac: unsupported bit depth %d (supported: %d)", bitDepth, bitDepth16).
			Component(component).
			Category(errors.CategoryValidation).
			Context("operation", "aac_supports").
			Context("bit_depth", bitDepth).
			Build()
	}
	if channels < 1 || channels > 2 {
		return errors.Newf("aac: unsupported channel count %d (supported: 1, 2)", channels).
			Component(component).
			Category(errors.CategoryValidation).
			Context("operation", "aac_supports").
			Context("channels", channels).
			Build()
	}
	return nil
}

// EncodePCM encodes opts.PCMData to an AAC-LC clip in an MP4 container at
// opts.OutputPath. The write is atomic: data is encoded to a unique per-export
// temp file and renamed on success, with the temp file removed on any failure.
// A non-zero GainDB is applied in Go before encoding; opts.PCMData itself is
// never modified.
//
// The MP4 muxer patches the mdat size and appends the moov atom on close, so
// the sink must be seekable. That is why this encodes to a real file rather
// than streaming to an arbitrary writer. Like un-faststarted FFmpeg output, moov
// therefore lands after the payload, so the file is not optimised for
// progressive download; the FFmpeg export path produces the same layout.
//
// ctx is honoured before the temp file is created but not during encoding:
// go-m4a's one-shot entry point runs to completion once started.
func EncodePCM(ctx context.Context, opts *Options) error {
	if opts == nil {
		return validationErr("aac encode: nil options")
	}
	if err := validateEncodeInput(opts); err != nil {
		return err
	}

	cfg := aacpcm.Config{
		SampleRate: opts.SampleRate,
		BitDepth:   opts.BitDepth,
		Channels:   opts.Channels,
		Bitrate:    opts.BitrateKbps * bitsPerKilobit,
	}

	// Gain is applied up front because the library entry point takes the whole
	// clip; at 0 dB (the common case) Applied returns the source unchanged, so
	// no copy is made. Note aacm4a buffers the entire encoded ADTS stream in
	// memory before muxing it, so peak usage scales with clip length rather than
	// staying constant as it does on the FLAC path.
	pcm := pcmgain.Applied(opts.PCMData, opts.GainDB)

	// WriteFile classifies its own filesystem failures and passes a cancelled
	// context through raw. The payload write happens in here though, so a write
	// fault surfacing through the codec is classified as file I/O rather than
	// blamed on the encoder.
	return audiotemp.WriteFile(ctx, component, opts.OutputPath, func(f *os.File) error {
		encErr := aacm4a.EncodeInterleaved(f, cfg, pcm)
		if encErr == nil {
			return nil
		}
		if audiotemp.IsWriteFault(encErr) {
			return errors.New(encErr).
				Component(component).
				Category(errors.CategoryFileIO).
				Context("operation", "aac_encode_write").
				Build()
		}
		return errors.New(encErr).
			Component(component).
			Category(errors.CategoryAudio).
			Context("operation", "aac_encode_stream").
			Context("sample_rate", opts.SampleRate).
			Context("channels", opts.Channels).
			Context("bitrate_kbps", opts.BitrateKbps).
			Build()
	})
}

// validateEncodeInput rejects options the encoder cannot honour, with a clear
// error rather than an opaque failure deep inside go-aac.
func validateEncodeInput(opts *Options) error {
	if len(opts.PCMData) == 0 {
		return validationErr("aac encode: empty PCM data")
	}
	if opts.OutputPath == "" {
		return validationErr("aac encode: empty output path")
	}
	if err := Supports(opts.SampleRate, opts.BitDepth, opts.Channels); err != nil {
		return err
	}
	if opts.BitrateKbps < 0 {
		return errors.Newf("aac encode: negative bitrate %d kbps", opts.BitrateKbps).
			Component(component).
			Category(errors.CategoryValidation).
			Context("operation", "aac_encode_validate").
			Context("bitrate_kbps", opts.BitrateKbps).
			Build()
	}
	// Reject a partial trailing frame early rather than letting it surface as an
	// opaque stride error inside go-aac.
	if bytesPerFrame := (opts.BitDepth / 8) * opts.Channels; len(opts.PCMData)%bytesPerFrame != 0 {
		return errors.Newf("aac encode: PCM length %d is not a multiple of the %d-byte frame size (%d-bit x %d ch)",
			len(opts.PCMData), bytesPerFrame, opts.BitDepth, opts.Channels).
			Component(component).
			Category(errors.CategoryValidation).
			Context("operation", "aac_encode_validate").
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
		Context("operation", "aac_encode_validate").
		Build()
}
