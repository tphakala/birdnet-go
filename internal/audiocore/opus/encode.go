// Package opus encodes captured PCM to an Ogg Opus clip (.opus) using the
// pure-Go go-opus encoder, with no FFmpeg process involved.
//
// It mirrors the native FLAC encoder in internal/audiocore/flac: the same
// Options shape, the same atomic temp-file-then-rename write via audiotemp, and
// the same enhanced-error conventions. Gain is applied in Go before encoding.
//
// This path is gated at the call site by internal/audiocore/nativeenc; Opus
// clip export still defaults to FFmpeg.
package opus

import (
	"context"
	"os"
	"path/filepath"

	"github.com/tphakala/go-opus/oggopus"

	"github.com/tphakala/birdnet-go/internal/audiocore/audiotemp"
	"github.com/tphakala/birdnet-go/internal/audiocore/pcmgain"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	// component is the error-telemetry component name for this package.
	component = "audiocore/opus"

	// bitDepth16 is the only PCM bit depth go-opus accepts. Opus is defined on
	// int16 (or float) samples and go-opus exposes only the int16 path.
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

// SupportedSampleRate reports whether the native encoder accepts a PCM sample
// rate. Callers use it to keep unsupported rates on FFmpeg rather than failing
// the export.
func SupportedSampleRate(sampleRate int) bool {
	for _, r := range supportedSampleRates {
		if sampleRate == r {
			return true
		}
	}
	return false
}

// SupportedBitDepth reports whether the native encoder accepts a PCM bit depth.
func SupportedBitDepth(bitDepth int) bool { return bitDepth == bitDepth16 }

// SupportedChannels reports whether the native encoder accepts a channel count.
func SupportedChannels(channels int) bool { return channels == 1 || channels == 2 }

// EncodePCM encodes opts.PCMData to an Ogg Opus file at opts.OutputPath. The
// write is atomic: data is encoded to a unique per-export temp file and renamed
// on success, with the temp file removed on any failure. A non-zero GainDB is
// applied in Go before encoding; opts.PCMData itself is never modified.
//
// Ogg carries duration in per-page granule positions written as the stream
// progresses, so unlike the MP4 path this needs no seeking. It still writes
// through a temp file so a partially written clip is never visible at the final
// path.
func EncodePCM(ctx context.Context, opts *Options) (err error) {
	if opts == nil {
		return validationErr("opus encode: nil options")
	}
	if err := validateEncodeInput(opts); err != nil {
		return err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}

	if mkErr := os.MkdirAll(filepath.Dir(opts.OutputPath), 0o750); mkErr != nil {
		return errors.New(mkErr).
			Component(component).
			Category(errors.CategoryFileIO).
			Context("operation", "opus_encode_mkdir").
			Build()
	}

	tempPath := audiotemp.UniquePath(opts.OutputPath)
	f, createErr := os.Create(tempPath) //nolint:gosec // path derived from validated config
	if createErr != nil {
		return errors.New(createErr).
			Component(component).
			Category(errors.CategoryFileIO).
			Context("operation", "opus_encode_create_temp").
			Build()
	}

	// Cleanup: close the temp file (idempotent) and remove it unless committed.
	committed := false
	fileOpen := true
	closeFile := func() error {
		if !fileOpen {
			return nil
		}
		fileOpen = false
		return f.Close()
	}
	defer func() {
		_ = closeFile()
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()

	cfg := oggopus.Config{
		SampleRate: opts.SampleRate,
		Channels:   opts.Channels,
		Bitrate:    opts.BitrateKbps * bitsPerKilobit,
	}

	// oggopus draws its encoder from an internal pool and streams Ogg pages as
	// it goes. Gain is applied up front because the one-shot API takes the whole
	// clip; at 0 dB (the common case) Applied returns the source unchanged, so
	// no copy is made.
	if encErr := oggopus.EncodeInterleaved(f, cfg, pcmgain.Applied(opts.PCMData, opts.GainDB)); encErr != nil {
		return errors.New(encErr).
			Component(component).
			Category(errors.CategoryAudio).
			Context("operation", "opus_encode_stream").
			Context("sample_rate", opts.SampleRate).
			Context("channels", opts.Channels).
			Context("bitrate_kbps", opts.BitrateKbps).
			Build()
	}

	if syncErr := f.Sync(); syncErr != nil {
		return errors.New(syncErr).
			Component(component).
			Category(errors.CategoryFileIO).
			Context("operation", "opus_encode_sync").
			Build()
	}
	if closeErr := closeFile(); closeErr != nil {
		return errors.New(closeErr).
			Component(component).
			Category(errors.CategoryFileIO).
			Context("operation", "opus_encode_close_temp").
			Build()
	}

	if renameErr := audiotemp.Finalize(tempPath, opts.OutputPath); renameErr != nil {
		return errors.New(renameErr).
			Component(component).
			Category(errors.CategoryFileIO).
			Context("operation", "opus_encode_rename").
			Build()
	}
	committed = true
	return nil
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
	if !SupportedSampleRate(opts.SampleRate) {
		return errors.Newf("opus encode: unsupported sample rate %d", opts.SampleRate).
			Component(component).
			Category(errors.CategoryValidation).
			Context("operation", "opus_encode_validate").
			Context("sample_rate", opts.SampleRate).
			Build()
	}
	if !SupportedBitDepth(opts.BitDepth) {
		return errors.Newf("opus encode: unsupported bit depth %d", opts.BitDepth).
			Component(component).
			Category(errors.CategoryValidation).
			Context("operation", "opus_encode_validate").
			Context("bit_depth", opts.BitDepth).
			Build()
	}
	if !SupportedChannels(opts.Channels) {
		return errors.Newf("opus encode: unsupported channel count %d", opts.Channels).
			Component(component).
			Category(errors.CategoryValidation).
			Context("operation", "opus_encode_validate").
			Context("channels", opts.Channels).
			Build()
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
