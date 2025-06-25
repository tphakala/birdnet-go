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

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("save_wav", "wav", "error")
			fileMetrics.RecordFileOperationError("save_wav", "wav", "empty_path")
		}
		return enhancedErr
	}

	if len(pcmData) == 0 {
		enhancedErr := errors.Newf("empty PCM data provided for WAV save operation").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "save_pcm_to_wav").
			Context("data_size", 0).
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("save_wav", "wav", "error")
			fileMetrics.RecordFileOperationError("save_wav", "wav", "empty_data")
		}
		return enhancedErr
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

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("save_wav", "wav", "error")
			fileMetrics.RecordFileOperationError("save_wav", "wav", "data_alignment")
			fileMetrics.RecordAudioDataValidationError("save_wav", "alignment")
		}
		return enhancedErr
	}

	// Create the directory structure if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		enhancedErr := errors.New(err).
			Component("myaudio").
			Category(errors.CategoryFileIO).
			Context("operation", "save_pcm_to_wav").
			Context("file_operation", "create_directories").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("save_wav", "wav", "error")
			fileMetrics.RecordFileOperationError("save_wav", "wav", "directory_creation_failed")
		}
		return enhancedErr
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

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("save_wav", "wav", "error")
			fileMetrics.RecordFileOperationError("save_wav", "wav", "file_creation_failed")
		}
		return enhancedErr
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

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("save_wav", "wav", "error")
			fileMetrics.RecordFileOperationError("save_wav", "wav", "encoder_creation_failed")
		}
		return enhancedErr
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

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("save_wav", "wav", "error")
			fileMetrics.RecordFileOperationError("save_wav", "wav", "sample_conversion_failed")
			fileMetrics.RecordAudioDataValidationError("save_wav", "conversion")
		}
		return enhancedErr
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

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("save_wav", "wav", "error")
			fileMetrics.RecordFileOperationError("save_wav", "wav", "write_failed")
		}
		return enhancedErr
	}

	// Close the WAV encoder, which finalizes the file format
	if err := enc.Close(); err != nil {
		enhancedErr := errors.New(err).
			Component("myaudio").
			Category(errors.CategoryFileIO).
			Context("operation", "save_pcm_to_wav").
			Context("file_operation", "close_encoder").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("save_wav", "wav", "error")
			fileMetrics.RecordFileOperationError("save_wav", "wav", "encoder_close_failed")
		}
		return enhancedErr
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
	var samples []int
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
