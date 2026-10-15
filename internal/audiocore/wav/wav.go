// Package wav writes mono PCM clips as WAV files with go-wav.
package wav

import (
	"context"
	"os"
	"path/filepath"

	"github.com/tphakala/birdnet-go/internal/audiocore/audiotemp"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/go-audio-dsp/gain"
	wavpcm "github.com/tphakala/go-wav/pcm"
)

// bitDepth16 is the only depth the gain stage supports.
const bitDepth16 = 16

const (
	// wavNumChannels is the number of audio channels for WAV output (mono).
	wavNumChannels = 1
)

// supportedWAVBitDepths lists the signed integer PCM sample widths EncodePCM
// accepts. These are the depths go-wav writes for integer PCM (8-bit is excluded
// because WAV stores it unsigned, which does not match the signed PCM BirdNET-Go
// produces).
var supportedWAVBitDepths = map[int]struct{}{16: {}, 24: {}, 32: {}}

// Options configures a native WAV export of an in-memory PCM buffer.
type Options struct {
	// PCMData is mono little-endian signed integer PCM (the capture buffer
	// format). EncodePCM never modifies it.
	PCMData []byte
	// OutputPath is the final file path; the temp file and rename are internal.
	// Parent directories are created automatically.
	OutputPath string
	// SampleRate is the PCM sample rate in Hz (for example 48000).
	SampleRate int
	// BitDepth is the number of bits per sample: 16, 24 or 32.
	BitDepth int
	// GainDB is the volume adjustment in dB (0 = no change). Only 16-bit PCM
	// supports a non-zero gain.
	GainDB float64
}

// EncodePCM saves opts.PCMData as a mono WAV file at opts.OutputPath, applying
// opts.GainDB first when it is not zero.
//
// Encoding is delegated to github.com/tphakala/go-wav, which passes the PCM bytes
// through unchanged (no per-sample conversion) and, for a clip that outgrows the
// 4 GiB RIFF limit, transparently writes an RF64 container instead of silently
// truncating the size fields. Gain runs on the go-audio-dsp gain package and
// works on a copy, so the caller's PCMData stays pristine for later readers such
// as the spectrogram pre-render job.
//
// Errors are categorized: invalid options and encoder rejections of the stream
// are validation or audio errors, and only filesystem failures are file I/O.
//
// ctx is accepted for symmetry with the other clip encoders and is not consulted:
// a WAV save is one buffered write that is not cancellable, so a cancelled context
// neither aborts it nor turns a real failure into a cancellation.
func EncodePCM(_ context.Context, opts *Options) error {
	if opts == nil {
		return errors.Newf("nil options provided for WAV save operation").
			Component("audiocore/wav").
			Category(errors.CategoryValidation).
			Context("operation", "save_pcm_to_wav").
			Build()
	}
	filePath, pcmData, sampleRate, bitDepth := opts.OutputPath, opts.PCMData, opts.SampleRate, opts.BitDepth

	if filePath == "" {
		return errors.Newf("empty file path provided for WAV save operation").
			Component("audiocore/wav").
			Category(errors.CategoryValidation).
			Context("operation", "save_pcm_to_wav").
			Build()
	}

	if len(pcmData) == 0 {
		return errors.Newf("empty PCM data provided for WAV save operation").
			Component("audiocore/wav").
			Category(errors.CategoryValidation).
			Context("operation", "save_pcm_to_wav").
			Context("data_size", 0).
			Build()
	}

	if _, ok := supportedWAVBitDepths[bitDepth]; !ok {
		return errors.Newf("unsupported bit depth %d: EncodePCM requires 16-, 24- or 32-bit PCM", bitDepth).
			Component("audiocore/wav").
			Category(errors.CategoryValidation).
			Context("operation", "save_pcm_to_wav").
			Context("bit_depth", bitDepth).
			Build()
	}

	// Reject a non-positive sample rate up front: go-wav rejects it too, but a
	// birdnet-go structured error carries the telemetry category and context.
	if sampleRate <= 0 {
		return errors.Newf("invalid sample rate %d: EncodePCM requires a positive sample rate", sampleRate).
			Component("audiocore/wav").
			Category(errors.CategoryValidation).
			Context("operation", "save_pcm_to_wav").
			Context("sample_rate", sampleRate).
			Build()
	}

	bytesPerSample := bitDepth / 8
	if len(pcmData)%bytesPerSample != 0 {
		return errors.Newf("PCM data size (%d bytes) is not aligned with bit depth (%d bits, %d bytes per sample)", len(pcmData), bitDepth, bytesPerSample).
			Component("audiocore/wav").
			Category(errors.CategoryValidation).
			Context("operation", "save_pcm_to_wav").
			Context("data_size", len(pcmData)).
			Context("bit_depth", bitDepth).
			Context("bytes_per_sample", bytesPerSample).
			Build()
	}

	if opts.GainDB != 0 {
		gained, err := applyGain(pcmData, bitDepth, opts.GainDB)
		if err != nil {
			return err
		}
		pcmData = gained
	}

	if err := os.MkdirAll(filepath.Dir(filePath), 0o750); err != nil {
		return errors.New(err).
			Component("audiocore/wav").
			Category(errors.CategoryFileIO).
			Context("operation", "save_pcm_to_wav").
			Context("file_operation", "create_directories").
			Build()
	}

	// Write to a process-unique temp file and atomically rename it onto the final
	// path, so concurrent saves of the same clip (see GitHub #3323) cannot
	// interleave into one file and a failed save never leaves a partial clip.
	tempPath := audiotemp.UniquePath(filePath)
	outFile, err := os.Create(tempPath) //nolint:gosec // G304: tempPath derives from filePath, which is constructed programmatically, not from raw user input
	if err != nil {
		return errors.New(err).
			Component("audiocore/wav").
			Category(errors.CategoryFileIO).
			Context("operation", "save_pcm_to_wav").
			Context("file_operation", "create_temp_file").
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
		return outFile.Close()
	}
	defer func() {
		_ = closeFile()
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()

	// EncodeInterleaved writes the whole clip in one call. It knows the length,
	// so it emits a plain RIFF header for a normal clip and only upgrades to
	// RF64 if the payload genuinely exceeds 4 GiB. The temp file is seekable, so
	// the header sizes are patched in place at the end.
	cfg := wavpcm.Config{
		SampleRate: sampleRate,
		BitDepth:   bitDepth,
		Channels:   wavNumChannels,
	}
	if err := wavpcm.EncodeInterleaved(outFile, cfg, pcmData); err != nil {
		return classifyEncodeError(err, sampleRate, bitDepth)
	}

	// Flush the temp file to stable storage before renaming so a crash right
	// after the rename cannot leave an empty or partial clip (matches the FLAC
	// encoder's sync-before-rename).
	if err := outFile.Sync(); err != nil {
		return errors.New(err).
			Component("audiocore/wav").
			Category(errors.CategoryFileIO).
			Context("operation", "save_pcm_to_wav").
			Context("file_operation", "sync_temp_file").
			Build()
	}

	// Close the temp file before renaming: flush its contents and release the
	// handle so the rename succeeds on Windows (which cannot rename an open file).
	if err := closeFile(); err != nil {
		return errors.New(err).
			Component("audiocore/wav").
			Category(errors.CategoryFileIO).
			Context("operation", "save_pcm_to_wav").
			Context("file_operation", "close_temp_file").
			Build()
	}

	if err := audiotemp.Finalize(tempPath, filePath); err != nil {
		return errors.New(err).
			Component("audiocore/wav").
			Category(errors.CategoryFileIO).
			Context("operation", "save_pcm_to_wav").
			Context("file_operation", "finalize_rename").
			Build()
	}
	committed = true
	return nil
}

// applyGain returns pcmData with gainDB applied. The result is a copy, or pcmData
// itself when the factor rounds to exactly 1, in which case it is only read.
func applyGain(pcmData []byte, bitDepth int, gainDB float64) ([]byte, error) {
	if bitDepth != bitDepth16 {
		return nil, errors.Newf("gain is not supported for %d-bit PCM: EncodePCM applies gain to 16-bit PCM only", bitDepth).
			Component("audiocore/wav").
			Category(errors.CategoryValidation).
			Context("operation", "save_pcm_to_wav").
			Context("bit_depth", bitDepth).
			Build()
	}
	g, err := gain.New(gainDB)
	if err != nil {
		return nil, errors.New(err).
			Component("audiocore/wav").
			Category(errors.CategoryValidation).
			Context("operation", "save_pcm_to_wav").
			Context("file_operation", "gain_init").
			Context("gain_db", gainDB).
			Build()
	}
	gained, err := g.AppliedBytes(pcmData)
	if err != nil {
		return nil, errors.New(err).
			Component("audiocore/wav").
			Category(errors.CategoryAudio).
			Context("operation", "save_pcm_to_wav").
			Context("file_operation", "apply_gain").
			Build()
	}
	return gained, nil
}

// classifyEncodeError tags an error from the go-wav encoder. A *os.PathError is
// a write fault (a full disk, a read-only remount) and stays a file I/O error;
// anything else is the stream or its parameters and is an audio error, so a full
// disk does not show up in telemetry as an audio bug.
func classifyEncodeError(err error, sampleRate, bitDepth int) error {
	if audiotemp.IsWriteFault(err) {
		return errors.New(err).
			Component("audiocore/wav").
			Category(errors.CategoryFileIO).
			Context("operation", "save_pcm_to_wav").
			Context("file_operation", "encode_wav").
			Build()
	}
	return errors.New(err).
		Component("audiocore/wav").
		Category(errors.CategoryAudio).
		Context("operation", "save_pcm_to_wav").
		Context("file_operation", "encode_wav").
		Context("sample_rate", sampleRate).
		Context("bit_depth", bitDepth).
		Build()
}
