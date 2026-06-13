package flac

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"sync"

	"github.com/tphakala/birdnet-go/internal/errors"
	goflac "github.com/tphakala/go-flac/pcm"
)

const (
	// tempExt is appended to the output path while encoding; the file is renamed
	// to the final path on success. Intentionally duplicated from ffmpeg.TempExt
	// (same value ".temp") rather than imported, to keep this package free of any
	// dependency on the ffmpeg package it is meant to replace.
	tempExt = ".temp"

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
// atomic: data is encoded to OutputPath+tempExt and renamed on success, with the
// temp file removed on any failure. A non-zero GainDB is applied in Go before
// encoding; opts.PCMData itself is never modified.
func EncodePCM(ctx context.Context, opts *Options) (err error) {
	if opts == nil {
		return errors.Newf("flac encode: nil options").
			Component("audiocore/flac").
			Category(errors.CategoryValidation).
			Context("operation", "flac_encode_validate").
			Build()
	}
	if len(opts.PCMData) == 0 {
		return errors.Newf("flac encode: empty PCM data").
			Component("audiocore/flac").
			Category(errors.CategoryValidation).
			Context("operation", "flac_encode_validate").
			Build()
	}
	if !SupportedBitDepth(opts.BitDepth) {
		return errors.Newf("flac encode: unsupported bit depth %d", opts.BitDepth).
			Component("audiocore/flac").
			Category(errors.CategoryValidation).
			Context("operation", "flac_encode_validate").
			Context("bit_depth", opts.BitDepth).
			Build()
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

	tempPath := opts.OutputPath + tempExt
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

	enc := encoderPool.Get().(*goflac.Encoder)
	if resetErr := enc.Reset(f, cfg); resetErr != nil {
		// A failed Reset may leave the encoder half-initialized; go-flac's
		// contract says it must not be reused, so drop it (do not Put it back)
		// and let the pool allocate a fresh one next time.
		return errors.New(resetErr).
			Component("audiocore/flac").
			Category(errors.CategoryAudio).
			Context("operation", "flac_encode_reset").
			Build()
	}
	// Reset succeeded: safe to return to the pool even if a later Write/Close
	// errors, since the next Get/Reset fully reinitializes it.
	defer encoderPool.Put(enc)

	if writeErr := encodeSamples(ctx, enc, opts); writeErr != nil {
		return writeErr
	}

	if closeErr := enc.Close(); closeErr != nil {
		return errors.New(closeErr).
			Component("audiocore/flac").
			Category(errors.CategoryAudio).
			Context("operation", "flac_encode_close").
			Build()
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

	if renameErr := os.Rename(tempPath, opts.OutputPath); renameErr != nil {
		return errors.New(renameErr).
			Component("audiocore/flac").
			Category(errors.CategoryFileIO).
			Context("operation", "flac_encode_rename").
			Build()
	}
	committed = true
	return nil
}

// encodeSamples writes opts.PCMData to enc, applying gain when requested. With no
// gain it is a single zero-copy Write; with gain it streams fixed-size chunks
// through a pooled scratch buffer so the source is never copied wholesale or
// mutated.
func encodeSamples(ctx context.Context, enc *goflac.Encoder, opts *Options) error {
	if opts.GainDB == 0 {
		if _, err := enc.Write(opts.PCMData); err != nil {
			return wrapWriteErr(err)
		}
		return nil
	}

	factor := math.Pow(10, opts.GainDB/20)
	scratchPtr := gainScratchPool.Get().(*[]byte)
	scratch := *scratchPtr
	defer gainScratchPool.Put(scratchPtr)

	src := opts.PCMData
	for off := 0; off < len(src); {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		end := off + len(scratch)
		if end > len(src) {
			end = len(src)
		}
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
