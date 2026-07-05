package flac

import (
	"bytes"
	"context"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"

	"github.com/tphakala/birdnet-go/internal/audiocore/audiotemp"
	"github.com/tphakala/birdnet-go/internal/errors"
	goflac "github.com/tphakala/go-flac/pcm"
)

const (
	// tempExt is appended to the output path while encoding; the file is renamed
	// to the final path on success. It aliases audiotemp.Ext (a leaf package with
	// no dependency on the ffmpeg package this encoder is meant to replace).
	tempExt = audiotemp.Ext

	// defaultCompressionLevel matches FFmpeg's default FLAC compression level and
	// go-flac's benchmark default.
	defaultCompressionLevel = 5

	// bitDepth16 is the only PCM bit depth produced by the capture path and the
	// only depth the native encoder currently handles.
	bitDepth16 = 16

	// gainScratchBytes is the size of the pooled scratch chunk used to apply gain
	// without allocating a full-size copy of the PCM. Even so int16 samples never
	// straddle a chunk boundary.
	gainScratchBytes = 32 * 1024
)

// encoderPool reuses go-flac encoders (and their multi-MB workspaces) across
// exports. Encoder.Reset is safe on a zero-value encoder, so the pool seeds with
// new(goflac.Encoder). An encoder is returned to the pool only after a
// successful Reset; from that point a later Write/Close error still Puts it back
// safely, because the next Get/Reset fully reinitializes its leftover state (do
// not "fix" that into a Close-before-Put, which would double-write the stream
// headers). An encoder whose Reset failed is dropped, not pooled.
var encoderPool = sync.Pool{New: func() any { return new(goflac.Encoder) }}

// gainScratchPool reuses gain scratch chunks. Pooling a pointer avoids the
// per-Get slice-header allocation that sync.Pool of []byte would incur.
var gainScratchPool = sync.Pool{New: func() any {
	b := make([]byte, gainScratchBytes)
	return &b
}}

// Options configures a native FLAC export of an in-memory PCM buffer.
type Options struct {
	// PCMData is interleaved little-endian PCM (the capture buffer format).
	PCMData []byte
	// OutputPath is the final file path; the temp file and rename are internal.
	OutputPath string
	// SampleRate is the PCM sample rate in Hz.
	SampleRate int
	// Channels is the number of interleaved channels.
	Channels int
	// BitDepth is the PCM bit depth; only 16 is supported.
	BitDepth int
	// GainDB is the volume adjustment in dB (0 = no change).
	GainDB float64
}

// SupportedBitDepth reports whether the native encoder supports a PCM bit depth.
// Only 16-bit capture exists today; this guards the dispatch so FLAC export
// falls back to FFmpeg if a wider depth is ever introduced, without baking a
// constant-true condition into the call site.
func SupportedBitDepth(bitDepth int) bool { return bitDepth == bitDepth16 }

// EncodePCM encodes opts.PCMData to a FLAC file at opts.OutputPath. The write is
// atomic: data is encoded to a unique per-export temp file and renamed on
// success, with the temp file removed on any failure. A non-zero GainDB is
// applied in Go before encoding; opts.PCMData itself is never modified.
func EncodePCM(ctx context.Context, opts *Options) (err error) {
	if opts == nil {
		return errors.Newf("flac encode: nil options").
			Component("audiocore/flac").
			Category(errors.CategoryValidation).
			Context("operation", "flac_encode_validate").
			Build()
	}
	if err := validateEncodeInput(opts.PCMData, opts.BitDepth, opts.Channels); err != nil {
		return err
	}
	if opts.OutputPath == "" {
		return errors.Newf("flac encode: empty output path").
			Component("audiocore/flac").
			Category(errors.CategoryValidation).
			Context("operation", "flac_encode_validate").
			Build()
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}

	if mkErr := os.MkdirAll(filepath.Dir(opts.OutputPath), 0o750); mkErr != nil {
		return errors.New(mkErr).
			Component("audiocore/flac").
			Category(errors.CategoryFileIO).
			Context("operation", "flac_encode_mkdir").
			Build()
	}

	tempPath := audiotemp.UniquePath(opts.OutputPath)
	f, createErr := os.Create(tempPath) //nolint:gosec // path derived from validated config
	if createErr != nil {
		return errors.New(createErr).
			Component("audiocore/flac").
			Category(errors.CategoryFileIO).
			Context("operation", "flac_encode_create_temp").
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

	cfg := goflac.Config{
		SampleRate:       opts.SampleRate,
		BitDepth:         opts.BitDepth,
		Channels:         opts.Channels,
		CompressionLevel: defaultCompressionLevel,
		// One seek point per second, matching FFmpeg's SEEKTABLE so players seek
		// efficiently in long extended-capture clips. Patched in place at Close
		// via the seekable temp file.
		SeekTableInterval: opts.SampleRate,
	}

	if encErr := encodeToWriter(ctx, f, cfg, opts.PCMData, opts.GainDB); encErr != nil {
		return encErr
	}

	if syncErr := f.Sync(); syncErr != nil {
		return errors.New(syncErr).
			Component("audiocore/flac").
			Category(errors.CategoryFileIO).
			Context("operation", "flac_encode_sync").
			Build()
	}
	if closeErr := closeFile(); closeErr != nil {
		return errors.New(closeErr).
			Component("audiocore/flac").
			Category(errors.CategoryFileIO).
			Context("operation", "flac_encode_close_temp").
			Build()
	}

	if renameErr := audiotemp.Finalize(tempPath, opts.OutputPath); renameErr != nil {
		return errors.New(renameErr).
			Component("audiocore/flac").
			Category(errors.CategoryFileIO).
			Context("operation", "flac_encode_rename").
			Build()
	}
	committed = true
	return nil
}

// validateEncodeInput checks the PCM data and bit depth shared by the file and
// buffer encode entry points. It returns an enhanced validation error or nil.
func validateEncodeInput(pcmData []byte, bitDepth, channels int) error {
	if len(pcmData) == 0 {
		return errors.Newf("flac encode: empty PCM data").
			Component("audiocore/flac").
			Category(errors.CategoryValidation).
			Context("operation", "flac_encode_validate").
			Build()
	}
	if !SupportedBitDepth(bitDepth) {
		return errors.Newf("flac encode: unsupported bit depth %d", bitDepth).
			Component("audiocore/flac").
			Category(errors.CategoryValidation).
			Context("operation", "flac_encode_validate").
			Context("bit_depth", bitDepth).
			Build()
	}
	if channels <= 0 {
		return errors.Newf("flac encode: channels must be positive, got %d", channels).
			Component("audiocore/flac").
			Category(errors.CategoryValidation).
			Context("operation", "flac_encode_validate").
			Context("channels", channels).
			Build()
	}
	// Reject a partial trailing frame early with a clear error rather than
	// letting it surface as an opaque flush failure deep inside go-flac. A full
	// inter-channel sample (frame) is bytesPerSample*channels.
	if bytesPerFrame := (bitDepth / 8) * channels; len(pcmData)%bytesPerFrame != 0 {
		return errors.Newf("flac encode: PCM length %d is not a multiple of the %d-byte frame size (%d-bit x %d ch)", len(pcmData), bytesPerFrame, bitDepth, channels).
			Component("audiocore/flac").
			Category(errors.CategoryValidation).
			Context("operation", "flac_encode_validate").
			Context("pcm_len", len(pcmData)).
			Context("bytes_per_frame", bytesPerFrame).
			Build()
	}
	return nil
}

// encodeToWriter encodes pcmData (applying gainDB when non-zero) to w using a
// pooled go-flac encoder configured by cfg. It resets the encoder, writes the
// samples, and closes the encoder (which patches STREAMINFO/SEEKTABLE when w is
// an io.WriteSeeker), but does not close w itself. Used by both the file and
// in-memory buffer entry points.
func encodeToWriter(ctx context.Context, w io.Writer, cfg goflac.Config, pcmData []byte, gainDB float64) error {
	enc := encoderPool.Get().(*goflac.Encoder)
	if resetErr := enc.Reset(w, cfg); resetErr != nil {
		// A failed Reset may leave the encoder half-initialized; go-flac's
		// contract says it must not be reused, so drop it (do not Put it back)
		// and let the pool allocate a fresh one next time.
		return errors.New(resetErr).
			Component("audiocore/flac").
			Category(errors.CategoryAudio).
			Context("operation", "flac_encode_reset").
			Build()
	}
	// Reset succeeded, so the encoder may be pooled even if a later Write/Close
	// errors. Before pooling, re-Reset onto io.Discard to drop the reference to
	// w: for the buffer path w is the *bytes.Buffer holding the entire encoded
	// clip, and a pooled encoder would otherwise pin that memory until its next
	// use. go-flac exposes no writer setter, so re-Reset is the only way to
	// release it; SeekTableInterval is zeroed so a non-seekable sink is accepted.
	// This also re-primes the encoder, so it is pooled only if the re-Reset
	// succeeds (a failed one drops the encoder, matching the failed-Reset path).
	defer func() {
		depinCfg := cfg
		depinCfg.SeekTableInterval = 0
		if enc.Reset(io.Discard, depinCfg) == nil {
			encoderPool.Put(enc)
		}
	}()

	if writeErr := encodeSamples(ctx, enc, pcmData, gainDB); writeErr != nil {
		return writeErr
	}

	if closeErr := enc.Close(); closeErr != nil {
		return errors.New(closeErr).
			Component("audiocore/flac").
			Category(errors.CategoryAudio).
			Context("operation", "flac_encode_close").
			Build()
	}
	return nil
}

// encodeSamples writes pcmData to enc, applying gain when requested. With no gain
// it is a single zero-copy Write; with gain it streams fixed-size chunks through
// a pooled scratch buffer so the source is never copied wholesale or mutated.
func encodeSamples(ctx context.Context, enc *goflac.Encoder, pcmData []byte, gainDB float64) error {
	if gainDB == 0 {
		if _, err := enc.Write(pcmData); err != nil {
			return wrapWriteErr(err)
		}
		return nil
	}

	factor := math.Pow(10, gainDB/20)
	scratchPtr := gainScratchPool.Get().(*[]byte)
	scratch := *scratchPtr
	defer gainScratchPool.Put(scratchPtr)

	src := pcmData
	for off := 0; off < len(src); {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		end := min(off+len(scratch), len(src))
		chunk := scratch[:end-off]
		applyGainInt16(chunk, src[off:end], factor)
		if _, err := enc.Write(chunk); err != nil {
			return wrapWriteErr(err)
		}
		off = end
	}
	return nil
}

func wrapWriteErr(err error) error {
	return errors.New(err).
		Component("audiocore/flac").
		Category(errors.CategoryAudio).
		Context("operation", "flac_encode_write").
		Build()
}

// BufferOptions configures a native FLAC export of an in-memory PCM buffer to an
// in-memory FLAC buffer. It mirrors Options without OutputPath.
type BufferOptions struct {
	// PCMData is interleaved little-endian PCM (the capture buffer format).
	PCMData []byte
	// SampleRate is the PCM sample rate in Hz.
	SampleRate int
	// Channels is the number of interleaved channels.
	Channels int
	// BitDepth is the PCM bit depth; only 16 is supported.
	BitDepth int
	// GainDB is the volume adjustment in dB (0 = no change).
	GainDB float64
}

// EncodePCMToBuffer encodes opts.PCMData to a FLAC stream and returns it as an
// in-memory buffer. Unlike EncodePCM it writes to a non-seekable bytes.Buffer,
// so no SEEKTABLE is emitted. The STREAMINFO total-samples field is finalized
// up front from the PCM length (go-flac verifies it against the samples actually
// written on Close), so consumers that read the sample count for duration get a
// correct value without a seekable sink; the MD5 field is left at its spec-legal
// "unknown" sentinel for this streaming path. A non-zero GainDB is applied in Go
// before encoding; opts.PCMData itself is never modified.
func EncodePCMToBuffer(ctx context.Context, opts *BufferOptions) (*bytes.Buffer, error) {
	if opts == nil {
		return nil, errors.Newf("flac encode: nil options").
			Component("audiocore/flac").
			Category(errors.CategoryValidation).
			Context("operation", "flac_encode_validate").
			Build()
	}
	if err := validateEncodeInput(opts.PCMData, opts.BitDepth, opts.Channels); err != nil {
		return nil, err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}

	// Derive the inter-channel sample count (frames) from the PCM length so
	// go-flac can write a finalized STREAMINFO.total_samples up front on a
	// non-seekable sink. validateEncodeInput has already confirmed the length is
	// a whole number of frames, so this division is exact.
	bytesPerFrame := (opts.BitDepth / 8) * opts.Channels
	totalSamples := uint64(len(opts.PCMData) / bytesPerFrame)

	cfg := goflac.Config{
		SampleRate:       opts.SampleRate,
		BitDepth:         opts.BitDepth,
		Channels:         opts.Channels,
		CompressionLevel: defaultCompressionLevel,
		// TotalSamples finalizes STREAMINFO.total_samples in the header without a
		// seek-back, so a bytes.Buffer sink still reports the correct duration.
		// go-flac verifies it against the samples written on Close.
		TotalSamples: totalSamples,
		// No SeekTableInterval: a bytes.Buffer is not an io.WriteSeeker, and
		// go-flac rejects a seektable on a non-seekable sink. Short upload clips
		// do not need one.
	}

	buf := new(bytes.Buffer)
	if encErr := encodeToWriter(ctx, buf, cfg, opts.PCMData, opts.GainDB); encErr != nil {
		return nil, encErr
	}
	return buf, nil
}
