package convert

import (
	"os"
	"path/filepath"

	"github.com/tphakala/birdnet-go/internal/audiocore/audiotemp"
	"github.com/tphakala/birdnet-go/internal/errors"
	wavpcm "github.com/tphakala/go-wav/pcm"
)

const (
	// wavNumChannels is the number of audio channels for WAV output (mono).
	wavNumChannels = 1
)

// supportedWAVBitDepths lists the signed integer PCM sample widths SavePCMDataToWAV
// accepts. These are the depths go-wav writes for integer PCM (8-bit is excluded
// because WAV stores it unsigned, which does not match the signed PCM BirdNET-Go
// produces).
var supportedWAVBitDepths = map[int]struct{}{16: {}, 24: {}, 32: {}}

// SavePCMDataToWAV saves raw little-endian signed integer PCM data as a mono WAV
// file at filePath. sampleRate specifies the sample rate in Hz (e.g. 48000), and
// bitDepth specifies the number of bits per sample (16, 24 or 32). Parent
// directories are created automatically if they do not exist.
//
// Encoding is delegated to github.com/tphakala/go-wav, which passes the PCM bytes
// through unchanged (no per-sample conversion) and, for a clip that outgrows the
// 4 GiB RIFF limit, transparently writes an RF64 container instead of silently
// truncating the size fields.
func SavePCMDataToWAV(filePath string, pcmData []byte, sampleRate, bitDepth int) error {
	if filePath == "" {
		return errors.Newf("empty file path provided for WAV save operation").
			Component("audiocore/convert").
			Category(errors.CategoryValidation).
			Context("operation", "save_pcm_to_wav").
			Build()
	}

	if len(pcmData) == 0 {
		return errors.Newf("empty PCM data provided for WAV save operation").
			Component("audiocore/convert").
			Category(errors.CategoryValidation).
			Context("operation", "save_pcm_to_wav").
			Context("data_size", 0).
			Build()
	}

	if _, ok := supportedWAVBitDepths[bitDepth]; !ok {
		return errors.Newf("unsupported bit depth %d: SavePCMDataToWAV requires 16-, 24- or 32-bit PCM", bitDepth).
			Component("audiocore/convert").
			Category(errors.CategoryValidation).
			Context("operation", "save_pcm_to_wav").
			Context("bit_depth", bitDepth).
			Build()
	}

	// Reject a non-positive sample rate up front: go-wav rejects it too, but a
	// birdnet-go structured error carries the telemetry category and context.
	if sampleRate <= 0 {
		return errors.Newf("invalid sample rate %d: SavePCMDataToWAV requires a positive sample rate", sampleRate).
			Component("audiocore/convert").
			Category(errors.CategoryValidation).
			Context("operation", "save_pcm_to_wav").
			Context("sample_rate", sampleRate).
			Build()
	}

	bytesPerSample := bitDepth / 8
	if len(pcmData)%bytesPerSample != 0 {
		return errors.Newf("PCM data size (%d bytes) is not aligned with bit depth (%d bits, %d bytes per sample)", len(pcmData), bitDepth, bytesPerSample).
			Component("audiocore/convert").
			Category(errors.CategoryValidation).
			Context("operation", "save_pcm_to_wav").
			Context("data_size", len(pcmData)).
			Context("bit_depth", bitDepth).
			Context("bytes_per_sample", bytesPerSample).
			Build()
	}

	if err := os.MkdirAll(filepath.Dir(filePath), 0o750); err != nil {
		return errors.New(err).
			Component("audiocore/convert").
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
			Component("audiocore/convert").
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
		return errors.New(err).
			Component("audiocore/convert").
			Category(errors.CategoryFileIO).
			Context("operation", "save_pcm_to_wav").
			Context("file_operation", "encode_wav").
			Context("sample_rate", sampleRate).
			Context("bit_depth", bitDepth).
			Build()
	}

	// Flush the temp file to stable storage before renaming so a crash right
	// after the rename cannot leave an empty or partial clip (matches the FLAC
	// encoder's sync-before-rename).
	if err := outFile.Sync(); err != nil {
		return errors.New(err).
			Component("audiocore/convert").
			Category(errors.CategoryFileIO).
			Context("operation", "save_pcm_to_wav").
			Context("file_operation", "sync_temp_file").
			Build()
	}

	// Close the temp file before renaming: flush its contents and release the
	// handle so the rename succeeds on Windows (which cannot rename an open file).
	if err := closeFile(); err != nil {
		return errors.New(err).
			Component("audiocore/convert").
			Category(errors.CategoryFileIO).
			Context("operation", "save_pcm_to_wav").
			Context("file_operation", "close_temp_file").
			Build()
	}

	if err := audiotemp.Finalize(tempPath, filePath); err != nil {
		return errors.New(err).
			Component("audiocore/convert").
			Category(errors.CategoryFileIO).
			Context("operation", "save_pcm_to_wav").
			Context("file_operation", "finalize_rename").
			Build()
	}
	committed = true
	return nil
}
