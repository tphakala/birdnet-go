package myaudio

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

var (
	fileMetrics *metrics.MyAudioMetrics // Global metrics instance for file operations
)

// AudioChunkCallback is a function type that processes audio chunks
// The second parameter (isEOF) indicates when EOF has been reached in the audio file
type AudioChunkCallback func([]float32, bool) error

// GetAudioInfo returns basic information about the audio file
type AudioInfo struct {
	SampleRate   int
	TotalSamples int
	NumChannels  int
	BitDepth     int
}

// GetTotalChunks calculates the total number of chunks for a given audio file
func GetTotalChunks(sampleRate, totalSamples int, overlap float64) int {
	chunkSamples := 3 * sampleRate                          // samples in 3 seconds
	stepSamples := int((3 - overlap) * float64(sampleRate)) // samples per step based on overlap

	if stepSamples <= 0 {
		return 0
	}

	// Calculate total chunks including partial chunks, rounding up
	return (totalSamples - chunkSamples + stepSamples + (stepSamples - 1)) / stepSamples
}

func GetAudioInfo(filePath string) (AudioInfo, error) {
	start := time.Now()

	// Validate input
	if filePath == "" {
		enhancedErr := errors.Newf("empty file path provided for audio info retrieval").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "get_audio_info").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("get_audio_info", "unknown", "error")
			fileMetrics.RecordFileOperationError("get_audio_info", "unknown", "empty_path")
		}
		return AudioInfo{}, enhancedErr
	}

	// Get file extension for validation
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext == "" {
		enhancedErr := errors.Newf("file has no extension: %s", filepath.Base(filePath)).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "get_audio_info").
			Context("file_extension", "none").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("get_audio_info", ext, "error")
			fileMetrics.RecordFileOperationError("get_audio_info", ext, "no_extension")
		}
		return AudioInfo{}, enhancedErr
	}

	// Open the file
	file, err := os.Open(filePath)
	if err != nil {
		enhancedErr := errors.New(err).
			Component("myaudio").
			Category(errors.CategoryFileIO).
			Context("operation", "get_audio_info").
			Context("file_extension", ext).
			Context("file_operation", "open").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("get_audio_info", ext, "error")
			fileMetrics.RecordFileOperationError("get_audio_info", ext, "open_failed")
		}
		return AudioInfo{}, enhancedErr
	}
	defer file.Close()

	// Process based on file extension
	var info AudioInfo
	switch ext {
	case ".wav":
		info, err = readWAVInfo(file)
	case ".flac":
		info, err = readFLACInfo(file)
	default:
		enhancedErr := errors.Newf("unsupported audio format: %s", ext).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "get_audio_info").
			Context("file_extension", ext).
			Context("supported_formats", "wav,flac").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("get_audio_info", ext, "error")
			fileMetrics.RecordFileOperationError("get_audio_info", ext, "unsupported_format")
		}
		return AudioInfo{}, enhancedErr
	}

	if err != nil {
		enhancedErr := errors.New(err).
			Component("myaudio").
			Category(errors.CategoryFileIO).
			Context("operation", "get_audio_info").
			Context("file_extension", ext).
			Context("file_operation", "read_header").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("get_audio_info", ext, "error")
			fileMetrics.RecordFileOperationError("get_audio_info", ext, "header_read_failed")
		}
		return AudioInfo{}, enhancedErr
	}

	// Record successful operation
	if fileMetrics != nil {
		duration := time.Since(start).Seconds()
		fileMetrics.RecordFileOperation("get_audio_info", ext, "success")
		fileMetrics.RecordFileOperationDuration("get_audio_info", ext, duration)
		fileMetrics.RecordAudioFileInfo(ext, info.SampleRate, info.NumChannels, info.BitDepth, info.TotalSamples)
	}

	return info, nil
}

// ReadAudioFileBuffered reads and processes audio data in chunks
func ReadAudioFileBuffered(settings *conf.Settings, callback AudioChunkCallback) error {
	start := time.Now()

	// Validate input
	if settings == nil {
		enhancedErr := errors.Newf("settings parameter is nil").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "read_audio_file_buffered").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("read_buffered", "unknown", "error")
			fileMetrics.RecordFileOperationError("read_buffered", "unknown", "nil_settings")
		}
		return enhancedErr
	}

	if settings.Input.Path == "" {
		enhancedErr := errors.Newf("empty input path in settings").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "read_audio_file_buffered").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("read_buffered", "unknown", "error")
			fileMetrics.RecordFileOperationError("read_buffered", "unknown", "empty_path")
		}
		return enhancedErr
	}

	if callback == nil {
		enhancedErr := errors.Newf("callback function is nil").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "read_audio_file_buffered").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("read_buffered", "unknown", "error")
			fileMetrics.RecordFileOperationError("read_buffered", "unknown", "nil_callback")
		}
		return enhancedErr
	}

	// Get file extension
	ext := strings.ToLower(filepath.Ext(settings.Input.Path))

	// Open the file
	file, err := os.Open(settings.Input.Path)
	if err != nil {
		enhancedErr := errors.New(err).
			Component("myaudio").
			Category(errors.CategoryFileIO).
			Context("operation", "read_audio_file_buffered").
			Context("file_extension", ext).
			Context("file_operation", "open").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("read_buffered", ext, "error")
			fileMetrics.RecordFileOperationError("read_buffered", ext, "open_failed")
		}
		return enhancedErr
	}
	defer file.Close()

	// Process based on file format
	switch ext {
	case ".wav":
		err = readWAVBuffered(file, settings, callback)
	case ".flac":
		err = readFLACBuffered(file, settings, callback)
	default:
		enhancedErr := errors.Newf("unsupported audio format: %s", ext).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "read_audio_file_buffered").
			Context("file_extension", ext).
			Context("supported_formats", "wav,flac").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("read_buffered", ext, "error")
			fileMetrics.RecordFileOperationError("read_buffered", ext, "unsupported_format")
		}
		return enhancedErr
	}

	if err != nil {
		enhancedErr := errors.New(err).
			Component("myaudio").
			Category(errors.CategoryFileIO).
			Context("operation", "read_audio_file_buffered").
			Context("file_extension", ext).
			Context("file_operation", "read_buffered").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("read_buffered", ext, "error")
			fileMetrics.RecordFileOperationError("read_buffered", ext, "read_failed")
		}
		return enhancedErr
	}

	// Record successful operation
	if fileMetrics != nil {
		duration := time.Since(start).Seconds()
		fileMetrics.RecordFileOperation("read_buffered", ext, "success")
		fileMetrics.RecordFileOperationDuration("read_buffered", ext, duration)
	}

	return nil
}

// SetFileMetrics sets the metrics instance for file operations
func SetFileMetrics(metrics *metrics.MyAudioMetrics) {
	fileMetrics = metrics
}

// getAudioDivisor returns the appropriate divisor for converting samples based on bit depth
func getAudioDivisor(bitDepth int) (float32, error) {
	switch bitDepth {
	case 16:
		return 32768.0, nil
	case 24:
		return 8388608.0, nil
	case 32:
		return 2147483648.0, nil
	default:
		enhancedErr := errors.Newf("unsupported audio file bit depth: %d", bitDepth).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "get_audio_divisor").
			Context("bit_depth", bitDepth).
			Context("supported_bit_depths", "16,24,32").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordAudioDataValidationError("file_processing", "unsupported_bit_depth")
		}
		return 0, enhancedErr
	}
}
