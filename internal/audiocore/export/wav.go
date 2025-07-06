package export

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// WAVExporter implements native Go WAV export functionality
type WAVExporter struct {
	logger interface{} // Add logger when needed
}

// NewWAVExporter creates a new WAV exporter
func NewWAVExporter() *WAVExporter {
	return &WAVExporter{}
}

// ExportToFile exports audio data to a WAV file
func (w *WAVExporter) ExportToFile(ctx context.Context, audioData *audiocore.AudioData, config *Config) (string, error) {
	if err := w.ValidateConfig(config); err != nil {
		return "", err
	}

	// Generate file path
	fileName := GenerateFileName(config.FileNameTemplate, audioData.SourceID, audioData.Timestamp, FormatWAV)
	filePath := filepath.Join(config.OutputPath, fileName)

	// Ensure output directory exists
	if err := os.MkdirAll(config.OutputPath, 0o755); err != nil {
		return "", errors.New(err).
			Component("audiocore").
			Category(errors.CategoryFileIO).
			Context("operation", "create_export_directory").
			Context("path", config.OutputPath).
			Build()
	}

	// Create temporary file for atomic write
	tempPath := filePath + ".tmp"
	file, err := os.Create(tempPath)
	if err != nil {
		return "", errors.New(err).
			Component("audiocore").
			Category(errors.CategoryFileIO).
			Context("operation", "create_temp_file").
			Context("path", tempPath).
			Build()
	}

	// Ensure cleanup on error
	success := false
	defer func() {
		_ = file.Close()
		if !success {
			_ = os.Remove(tempPath)
		}
	}()

	// Export to the file
	if err := w.ExportToWriter(ctx, audioData, file, config); err != nil {
		return "", err
	}

	// Close file before rename
	if err := file.Close(); err != nil {
		return "", errors.New(err).
			Component("audiocore").
			Category(errors.CategoryFileIO).
			Context("operation", "close_temp_file").
			Build()
	}

	// Atomic rename
	if err := os.Rename(tempPath, filePath); err != nil {
		return "", errors.New(err).
			Component("audiocore").
			Category(errors.CategoryFileIO).
			Context("operation", "rename_export_file").
			Context("from", tempPath).
			Context("to", filePath).
			Build()
	}

	success = true
	return filePath, nil
}

// ExportToWriter exports audio data to an io.Writer as WAV format
func (w *WAVExporter) ExportToWriter(ctx context.Context, audioData *audiocore.AudioData, writer io.Writer, config *Config) error {
	// Check context cancellation
	select {
	case <-ctx.Done():
		return errors.New(ctx.Err()).
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("operation", "wav_export_cancelled").
			Build()
	default:
	}

	// Validate audio format
	if audioData.Format.BitDepth != 16 {
		return errors.Newf("WAV export currently only supports 16-bit audio, got %d-bit", audioData.Format.BitDepth).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("bit_depth", audioData.Format.BitDepth).
			Build()
	}

	// Build WAV header
	wavData, err := w.encodeWAV(audioData.Buffer, audioData.Format)
	if err != nil {
		return err
	}

	// Write to output
	if _, err := writer.Write(wavData); err != nil {
		return errors.New(err).
			Component("audiocore").
			Category(errors.CategoryFileIO).
			Context("operation", "write_wav_data").
			Build()
	}

	return nil
}

// ExportClip exports a specific time range as a clip
func (w *WAVExporter) ExportClip(ctx context.Context, audioData *audiocore.AudioData, startTime, endTime time.Time, config *Config) (string, error) {
	// For WAV export, we assume the entire audioData buffer is the clip
	// The capture manager should have already extracted the correct time range
	return w.ExportToFile(ctx, audioData, config)
}

// ValidateConfig validates the export configuration
func (w *WAVExporter) ValidateConfig(config *Config) error {
	if config == nil {
		return errors.Newf("export config is nil").
			Component("audiocore").
			Category(errors.CategoryValidation).
			Build()
	}

	if config.Format != FormatWAV {
		return errors.Newf("WAV exporter only supports WAV format, got %s", config.Format).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("format", string(config.Format)).
			Build()
	}

	return ValidateConfig(config)
}

// SupportedFormats returns the formats supported by this exporter
func (w *WAVExporter) SupportedFormats() []Format {
	return []Format{FormatWAV}
}

// encodeWAV encodes PCM data as WAV format
func (w *WAVExporter) encodeWAV(pcmData []byte, format audiocore.AudioFormat) ([]byte, error) {
	// Calculate WAV parameters
	byteRate := format.SampleRate * format.Channels * (format.BitDepth / 8)
	blockAlign := format.Channels * (format.BitDepth / 8)
	subChunk2Size := uint32(len(pcmData))
	chunkSize := 36 + subChunk2Size // 36 is fixed header size

	// Create buffer for WAV data
	buffer := bytes.NewBuffer(nil)

	// Write WAV header
	elements := []interface{}{
		[]byte("RIFF"),
		chunkSize,
		[]byte("WAVE"),
		[]byte("fmt "),
		uint32(16),                // SubChunk1Size
		uint16(1),                 // AudioFormat (1 = PCM)
		uint16(format.Channels),   // NumChannels
		uint32(format.SampleRate), // SampleRate
		uint32(byteRate),          // ByteRate
		uint16(blockAlign),        // BlockAlign
		uint16(format.BitDepth),   // BitsPerSample
		[]byte("data"),
		subChunk2Size,
	}

	// Write header elements
	for _, elem := range elements {
		if b, ok := elem.([]byte); ok {
			if _, err := buffer.Write(b); err != nil {
				return nil, errors.New(err).
					Component("audiocore").
					Category(errors.CategorySystem).
					Context("operation", "write_wav_header_bytes").
					Build()
			}
		} else {
			if err := binary.Write(buffer, binary.LittleEndian, elem); err != nil {
				return nil, errors.New(err).
					Component("audiocore").
					Category(errors.CategorySystem).
					Context("operation", "write_wav_header_binary").
					Build()
			}
		}
	}

	// Write PCM data
	if _, err := buffer.Write(pcmData); err != nil {
		return nil, errors.New(err).
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("operation", "write_wav_pcm_data").
			Build()
	}

	return buffer.Bytes(), nil
}
