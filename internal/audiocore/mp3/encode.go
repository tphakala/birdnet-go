// Package mp3 encodes captured PCM to a CBR MP3 clip (.mp3) using the pure-Go
// go-mp3 encoder, with no FFmpeg process involved.
//
// It mirrors the native Opus encoder in internal/audiocore/opus: the same
// Options shape, the same atomic temp-file-then-rename write (via audiotemp),
// and the same enhanced-error conventions. Gain is applied in Go before
// encoding.
//
// This path is opt-in: MP3 clip export still defaults to FFmpeg until the native
// encoder earns field confidence. The gate lives in
// internal/conf/native_encoders.go and the encoder selection in
// internal/analysis/processor.
package mp3

import (
	"context"
	"os"
	"slices"

	mp3pcm "github.com/tphakala/go-mp3/pcm"

	"github.com/tphakala/birdnet-go/internal/audiocore/audiotemp"
	"github.com/tphakala/birdnet-go/internal/audiocore/pcmgain"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	// component is the error-telemetry component name for this package.
	component = "audiocore/mp3"

	// bitDepth16 is the only PCM bit depth go-mp3 accepts. go-mp3/pcm reads its
	// input as interleaved int16 (S16LE) and has no bit-depth field at all, so
	// Options.BitDepth never reaches the library and exists only to validate the
	// caller's buffer against that assumption.
	bitDepth16 = 16

	// bitsPerKilobit converts the configured kbps bitrate to the bits per second
	// go-mp3 expects.
	bitsPerKilobit = 1000
)

// supportedSampleRates are the input rates go-mp3 accepts (the MPEG-1 Layer III
// rates). Anything else, notably an ultrasonic bat-capture rate, must stay on
// FFmpeg, which resamples internally.
var supportedSampleRates = [...]int{32000, 44100, 48000}

// supportedBitratesKbps is the fixed set of MPEG-1 Layer III CBR bitrates go-mp3
// accepts. Unlike AAC and Opus, MP3 has no continuous bitrate range: a value
// outside this set (a BirdNET-Go config as ordinary as 100k, which validation
// allows anywhere in 32-320k) is rejected by go-mp3 rather than snapped, so the
// encoder selection uses SupportsBitrate to fall back to FFmpeg rather than
// failing the export. The set is duplicated here rather than imported because
// go-mp3 keeps its validator (enc.ValidBitrateKbps) internal, the same way the
// sibling codecs duplicate small constants across module boundaries.
var supportedBitratesKbps = [...]int{32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}

// Options configures a native MP3 export of an in-memory PCM buffer.
type Options struct {
	// PCMData is interleaved little-endian PCM (the capture buffer format).
	PCMData []byte
	// OutputPath is the final .mp3 file path; the temp file and rename are
	// internal.
	OutputPath string
	// SampleRate is the PCM sample rate in Hz.
	SampleRate int
	// Channels is the number of interleaved channels.
	Channels int
	// BitDepth is the PCM bit depth; only 16 is supported.
	BitDepth int
	// BitrateKbps is the target CBR bitrate in kbit/s. It must be one of the 14
	// MPEG-1 Layer III rates (see SupportsBitrate); zero selects go-mp3's default
	// (128 kbps).
	BitrateKbps int
	// GainDB is the volume adjustment in dB (0 = no change).
	GainDB float64
}

// Supports reports whether the native encoder can carry a clip of this shape,
// returning a descriptive error naming the offending value when it cannot. It
// covers rate, depth and channel count; bitrate is checked separately by
// SupportsBitrate because MP3, unlike the other lossy formats, constrains it to
// a fixed set rather than a range.
func Supports(sampleRate, bitDepth, channels int) error {
	if !slices.Contains(supportedSampleRates[:], sampleRate) {
		return errors.Newf("mp3: unsupported sample rate %d (supported: %v)", sampleRate, supportedSampleRates).
			Component(component).
			Category(errors.CategoryValidation).
			Context("operation", "mp3_supports").
			Context("sample_rate", sampleRate).
			Build()
	}
	if bitDepth != bitDepth16 {
		return errors.Newf("mp3: unsupported bit depth %d (supported: %d)", bitDepth, bitDepth16).
			Component(component).
			Category(errors.CategoryValidation).
			Context("operation", "mp3_supports").
			Context("bit_depth", bitDepth).
			Build()
	}
	if channels < 1 || channels > 2 {
		return errors.Newf("mp3: unsupported channel count %d (supported: 1, 2)", channels).
			Component(component).
			Category(errors.CategoryValidation).
			Context("operation", "mp3_supports").
			Context("channels", channels).
			Build()
	}
	return nil
}

// SupportsBitrate reports whether go-mp3 can encode at bitrateKbps, returning a
// descriptive error when it cannot. Zero is accepted and maps to go-mp3's
// default rate. MP3 only codes the 14 fixed MPEG-1 Layer III rates, so a
// configured bitrate outside that set is the reason a clip falls back to FFmpeg
// rather than being encoded natively.
func SupportsBitrate(bitrateKbps int) error {
	if bitrateKbps == 0 || slices.Contains(supportedBitratesKbps[:], bitrateKbps) {
		return nil
	}
	return errors.Newf("mp3: unsupported bitrate %d kbps (supported: %v)", bitrateKbps, supportedBitratesKbps).
		Component(component).
		Category(errors.CategoryValidation).
		Context("operation", "mp3_supports_bitrate").
		Context("bitrate_kbps", bitrateKbps).
		Build()
}

// EncodePCM encodes opts.PCMData to a CBR MP3 file at opts.OutputPath. The write
// is atomic: data is encoded to a unique per-export temp file and renamed on
// success, with the temp file removed on any failure. A non-zero GainDB is
// applied in Go before encoding; opts.PCMData itself is never modified.
//
// MP3 is a bare frame stream with no container, so unlike the MP4 path this needs
// no seeking. It still writes through a temp file so a partially written clip is
// never visible at the final path.
//
// ctx is honoured before the temp file is created but not during encoding:
// go-mp3's one-shot entry point runs to completion once started.
func EncodePCM(ctx context.Context, opts *Options) error {
	if opts == nil {
		return validationErr("mp3 encode: nil options")
	}
	if err := validateEncodeInput(opts); err != nil {
		return err
	}

	cfg := mp3pcm.Config{
		SampleRate: opts.SampleRate,
		Channels:   opts.Channels,
		Bitrate:    opts.BitrateKbps * bitsPerKilobit,
	}

	// Gain is applied up front because the library entry point takes the whole
	// clip; at 0 dB (the common case) Applied returns the source unchanged, so no
	// copy is made.
	pcm := pcmgain.Applied(opts.PCMData, opts.GainDB)

	// go-mp3/pcm draws its encoder from an internal pool and streams frames as it
	// goes, so the encoded stream is never held in memory whole.
	// WriteFile classifies its own filesystem failures and passes a cancelled
	// context through raw. The payload write happens in here though, so a write
	// fault surfacing through the codec is classified as file I/O rather than
	// blamed on the encoder.
	return audiotemp.WriteFile(ctx, component, opts.OutputPath, func(f *os.File) error {
		encErr := mp3pcm.EncodeInterleaved(f, cfg, pcm)
		if encErr == nil {
			return nil
		}
		if audiotemp.IsWriteFault(encErr) {
			return errors.New(encErr).
				Component(component).
				Category(errors.CategoryFileIO).
				Context("operation", "mp3_encode_write").
				Build()
		}
		return errors.New(encErr).
			Component(component).
			Category(errors.CategoryAudio).
			Context("operation", "mp3_encode_stream").
			Context("sample_rate", opts.SampleRate).
			Context("channels", opts.Channels).
			Context("bitrate_kbps", opts.BitrateKbps).
			Build()
	})
}

// validateEncodeInput rejects options the encoder cannot honour, with a clear
// error rather than an opaque failure deep inside go-mp3.
func validateEncodeInput(opts *Options) error {
	if len(opts.PCMData) == 0 {
		return validationErr("mp3 encode: empty PCM data")
	}
	if opts.OutputPath == "" {
		return validationErr("mp3 encode: empty output path")
	}
	if err := Supports(opts.SampleRate, opts.BitDepth, opts.Channels); err != nil {
		return err
	}
	if opts.BitrateKbps < 0 {
		return errors.Newf("mp3 encode: negative bitrate %d kbps", opts.BitrateKbps).
			Component(component).
			Category(errors.CategoryValidation).
			Context("operation", "mp3_encode_validate").
			Context("bitrate_kbps", opts.BitrateKbps).
			Build()
	}
	if err := SupportsBitrate(opts.BitrateKbps); err != nil {
		return err
	}
	// Reject a partial trailing sample early rather than letting it surface as an
	// opaque length error inside go-mp3.
	if bytesPerSample := (opts.BitDepth / 8) * opts.Channels; len(opts.PCMData)%bytesPerSample != 0 {
		return errors.Newf("mp3 encode: PCM length %d is not a multiple of the %d-byte sample (%d-bit x %d ch)",
			len(opts.PCMData), bytesPerSample, opts.BitDepth, opts.Channels).
			Component(component).
			Category(errors.CategoryValidation).
			Context("operation", "mp3_encode_validate").
			Context("pcm_len", len(opts.PCMData)).
			Context("bytes_per_sample", bytesPerSample).
			Build()
	}
	return nil
}

func validationErr(msg string) error {
	return errors.Newf("%s", msg).
		Component(component).
		Category(errors.CategoryValidation).
		Context("operation", "mp3_encode_validate").
		Build()
}
