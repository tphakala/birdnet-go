// Package readfile provides WAV and FLAC audio file reading utilities.
// It exposes functions for reading audio metadata and buffered audio data
// without depending on global configuration state.
package readfile

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// Audio file extension constants.
const (
	// ExtWAV is the file extension for WAV audio files.
	ExtWAV = ".wav"

	// ExtFLAC is the file extension for FLAC audio files.
	ExtFLAC = ".flac"
)

// Analysis chunk timing constants.
const (
	// chunkDurationSeconds is the duration in seconds of each analysis chunk.
	chunkDurationSeconds = 3

	// minChunkDurationSeconds is the minimum duration in seconds for the
	// final chunk to be considered valid for analysis.
	minChunkDurationSeconds = 1.5
)

// AudioInfo holds basic metadata about an audio file.
type AudioInfo struct {
	// SampleRate is the number of audio samples per second (Hz).
	SampleRate int

	// TotalSamples is the total number of audio samples in the file.
	TotalSamples int

	// NumChannels is the number of audio channels (1=mono, 2=stereo).
	NumChannels int

	// BitDepth is the number of bits per sample (16, 24, or 32).
	BitDepth int
}

// AudioChunkCallback is called with successive chunks of float32 audio samples.
// The isEOF parameter is true when the final chunk has been delivered.
type AudioChunkCallback func(samples []float32, isEOF bool) error

// GetAudioInfo returns basic metadata for the audio file at filePath.
// Supported formats are WAV (.wav) and FLAC (.flac).
func GetAudioInfo(filePath string) (AudioInfo, error) {
	if filePath == "" {
		return AudioInfo{}, errors.Newf("empty file path provided for audio info retrieval").
			Component("audiocore/readfile").
			Category(errors.CategoryValidation).
			Context("operation", "get_audio_info").
			Build()
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	if ext == "" {
		return AudioInfo{}, errors.Newf("file has no extension: %s", filepath.Base(filePath)).
			Component("audiocore/readfile").
			Category(errors.CategoryValidation).
			Context("operation", "get_audio_info").
			Context("file_extension", "none").
			Build()
	}

	file, err := os.Open(filePath) //nolint:gosec // G304: filePath is from CLI args or directory walking
	if err != nil {
		return AudioInfo{}, errors.New(err).
			Component("audiocore/readfile").
			Category(errors.CategoryFileIO).
			Context("operation", "get_audio_info").
			Context("file_extension", ext).
			Context("file_operation", "open").
			Build()
	}
	defer func() {
		_ = file.Close()
	}()

	switch ext {
	case ExtWAV:
		info, err := readWAVInfo(file)
		if err != nil {
			return AudioInfo{}, errors.New(err).
				Component("audiocore/readfile").
				Category(errors.CategoryFileIO).
				Context("operation", "get_audio_info").
				Context("file_extension", ext).
				Context("file_operation", "read_header").
				Build()
		}
		return info, nil

	case ExtFLAC:
		info, err := readFLACInfo(file)
		if err != nil {
			return AudioInfo{}, errors.New(err).
				Component("audiocore/readfile").
				Category(errors.CategoryFileIO).
				Context("operation", "get_audio_info").
				Context("file_extension", ext).
				Context("file_operation", "read_header").
				Build()
		}
		return info, nil

	default:
		return AudioInfo{}, errors.Newf("unsupported audio format: %s", ext).
			Component("audiocore/readfile").
			Category(errors.CategoryValidation).
			Context("operation", "get_audio_info").
			Context("file_extension", ext).
			Context("supported_formats", "wav,flac").
			Build()
	}
}

// GetTotalChunks calculates the total number of 3-second analysis chunks for
// the given audio parameters. overlap is the overlap between chunks in seconds.
func GetTotalChunks(sampleRate, totalSamples int, overlap float64) int {
	chunkSamples := 3 * sampleRate                          // samples in 3 seconds
	stepSamples := int((3 - overlap) * float64(sampleRate)) // samples per step based on overlap

	if stepSamples <= 0 {
		return 0
	}

	// Calculate total chunks including partial chunks, rounding up.
	// Guard against negative results when totalSamples < chunkSamples.
	result := (totalSamples - chunkSamples + stepSamples + (stepSamples - 1)) / stepSamples
	if result <= 0 {
		return 0
	}
	return result
}

// getAudioDivisor returns the divisor used to normalise integer PCM samples to
// the [-1.0, 1.0] float32 range for the given bit depth.
//
// Only 16-bit, 24-bit, and 32-bit integer PCM are supported. Other bit depths
// (e.g., 8-bit unsigned, 32-bit float IEEE) return an explicit error rather
// than silently producing incorrect normalisation values.
func getAudioDivisor(bitDepth int) (float32, error) {
	switch bitDepth {
	case 16:
		return 32768.0, nil
	case 24:
		return 8388608.0, nil
	case 32:
		return 2147483648.0, nil
	default:
		return 0, errors.Newf("unsupported audio file bit depth: %d", bitDepth).
			Component("audiocore/readfile").
			Category(errors.CategoryValidation).
			Context("operation", "get_audio_divisor").
			Context("bit_depth", bitDepth).
			Context("supported_bit_depths", "16,24,32").
			Build()
	}
}
