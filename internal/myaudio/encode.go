package myaudio

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"time"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// seekableBuffer extends bytes.Buffer to add a Seek method, making it compatible with io.WriteSeeker.
type seekableBuffer struct {
	bytes.Buffer
	pos int64
}

// recordFileOperationError is a helper function to record file operation errors and return the enhanced error
func recordFileOperationError(operation, format, errorType string, enhancedErr error) error {
	if fileMetrics != nil {
		fileMetrics.RecordFileOperation(operation, format, "error")
		fileMetrics.RecordFileOperationError(operation, format, errorType)
	}
	return enhancedErr
}

// recordFileOperationErrorWithValidation is a helper function to record file operation errors with additional validation error
func recordFileOperationErrorWithValidation(operation, format, errorType, validationType string, enhancedErr error) error {
	if fileMetrics != nil {
		fileMetrics.RecordFileOperation(operation, format, "error")
		fileMetrics.RecordFileOperationError(operation, format, errorType)
		fileMetrics.RecordAudioDataValidationError(operation, validationType)
	}
	return enhancedErr
}

// SavePCMDataToWAV saves the given PCM data as a WAV file at the specified filePath.
func SavePCMDataToWAV(filePath string, pcmData []byte) error {
	start := time.Now()

	// Validate inputs
	if filePath == "" {
		enhancedErr := errors.Newf("empty file path provided for WAV save operation").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "save_pcm_to_wav").
			Build()

		return recordFileOperationError("save_wav", "wav", "empty_path", enhancedErr)
	}

	if len(pcmData) == 0 {
		enhancedErr := errors.Newf("empty PCM data provided for WAV save operation").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "save_pcm_to_wav").
			Context("data_size", 0).
			Build()

		return recordFileOperationError("save_wav", "wav", "empty_data", enhancedErr)
	}

	// Validate PCM data alignment
	expectedAlignment := conf.BitDepth / 8
	if len(pcmData)%expectedAlignment != 0 {
		enhancedErr := errors.Newf("PCM data size (%d bytes) is not aligned with bit depth (%d bits, %d bytes per sample)", len(pcmData), conf.BitDepth, expectedAlignment).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "save_pcm_to_wav").
			Context("data_size", len(pcmData)).
			Context("bit_depth", conf.BitDepth).
			Context("expected_alignment", expectedAlignment).
			Build()

		return recordFileOperationErrorWithValidation("save_wav", "wav", "data_alignment", "alignment", enhancedErr)
	}

	// Create the directory structure if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		enhancedErr := errors.New(err).
			Component("myaudio").
			Category(errors.CategoryFileIO).
			Context("operation", "save_pcm_to_wav").
			Context("file_operation", "create_directories").
			Build()

		return recordFileOperationError("save_wav", "wav", "directory_creation_failed", enhancedErr)
	}

	// Open a new file for writing
	outFile, err := os.Create(filePath)
	if err != nil {
		enhancedErr := errors.New(err).
			Component("myaudio").
			Category(errors.CategoryFileIO).
			Context("operation", "save_pcm_to_wav").
			Context("file_operation", "create_file").
			Build()

		return recordFileOperationError("save_wav", "wav", "file_creation_failed", enhancedErr)
	}
	defer outFile.Close()

	// Create a new WAV encoder with the specified format settings
	enc := wav.NewEncoder(outFile, conf.SampleRate, conf.BitDepth, conf.NumChannels, 1)
	if enc == nil {
		enhancedErr := errors.Newf("failed to create WAV encoder").
			Component("myaudio").
			Category(errors.CategorySystem).
			Context("operation", "save_pcm_to_wav").
			Context("sample_rate", conf.SampleRate).
			Context("bit_depth", conf.BitDepth).
			Context("num_channels", conf.NumChannels).
			Build()

		return recordFileOperationError("save_wav", "wav", "encoder_creation_failed", enhancedErr)
	}

	// Convert the byte slice to a slice of integer samples
	intSamples := byteSliceToInts(pcmData)
	if len(intSamples) == 0 {
		enhancedErr := errors.Newf("failed to convert PCM data to integer samples").
			Component("myaudio").
			Category(errors.CategorySystem).
			Context("operation", "save_pcm_to_wav").
			Context("pcm_data_size", len(pcmData)).
			Build()

		return recordFileOperationErrorWithValidation("save_wav", "wav", "sample_conversion_failed", "conversion", enhancedErr)
	}

	// Write the integer samples to the WAV file
	if err := enc.Write(&audio.IntBuffer{Data: intSamples, Format: &audio.Format{SampleRate: conf.SampleRate, NumChannels: conf.NumChannels}}); err != nil {
		enhancedErr := errors.New(err).
			Component("myaudio").
			Category(errors.CategoryFileIO).
			Context("operation", "save_pcm_to_wav").
			Context("file_operation", "write_samples").
			Context("sample_count", len(intSamples)).
			Build()

		return recordFileOperationError("save_wav", "wav", "write_failed", enhancedErr)
	}

	// Close the WAV encoder, which finalizes the file format
	if err := enc.Close(); err != nil {
		enhancedErr := errors.New(err).
			Component("myaudio").
			Category(errors.CategoryFileIO).
			Context("operation", "save_pcm_to_wav").
			Context("file_operation", "close_encoder").
			Build()

		return recordFileOperationError("save_wav", "wav", "encoder_close_failed", enhancedErr)
	}

	// Record successful operation
	if fileMetrics != nil {
		duration := time.Since(start).Seconds()
		fileMetrics.RecordFileOperation("save_wav", "wav", "success")
		fileMetrics.RecordFileOperationDuration("save_wav", "wav", duration)
		fileMetrics.RecordFileSize("save_wav", "wav", int64(len(pcmData)))
		fileMetrics.RecordAudioFileInfo("wav", conf.SampleRate, conf.NumChannels, conf.BitDepth, len(intSamples))
	}

	return nil
}

// byteSliceToInts converts a byte slice to a slice of integers.
// Each pair of bytes is treated as a single 16-bit sample.
func byteSliceToInts(pcmData []byte) []int {
	// Pre-allocate samples slice based on PCM data length (2 bytes per sample)
	samples := make([]int, 0, len(pcmData)/2)
	buf := bytes.NewBuffer(pcmData)

	// Read each 16-bit sample from the byte buffer and store it as an int.
	for {
		var sample int16
		if err := binary.Read(buf, binary.LittleEndian, &sample); err != nil {
			break // Exit loop on read error (e.g., end of buffer).
		}
		samples = append(samples, int(sample))
	}

	return samples
}
