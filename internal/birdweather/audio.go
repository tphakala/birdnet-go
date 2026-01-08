// audio.go contains helper functions for audio file handling
package birdweather

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Audio file constants
const (
	// audioDirPermission is the permission mode for created directories
	audioDirPermission = 0o750
)

// saveBufferToFile writes a bytes.Buffer containing audio data to a file, along with
// timestamp and format information for debugging purposes.
func saveBufferToFile(buffer *bytes.Buffer, filename string, startTime, endTime time.Time) error {
	log := GetLogger()

	// Validate input parameters
	if buffer == nil {
		return errors.New(fmt.Errorf("buffer is nil")).
			Component("birdweather").
			Category(errors.CategoryValidation).
			Context("filename", filename).
			Build()
	}

	// Clean the filename to prevent path traversal (gosec G304)
	cleanFilename := filepath.Clean(filename)

	// Get the buffer size before any operations that might consume it
	bufferSize := buffer.Len()

	// Create directory if it doesn't exist
	dirPath := filepath.Dir(cleanFilename)
	if err := os.MkdirAll(dirPath, audioDirPermission); err != nil {
		return errors.New(err).
			Component("birdweather").
			Category(errors.CategoryFileIO).
			FileContext(dirPath, 0).
			Context("operation", "create_directory").
			Build()
	}

	// Save the audio file
	file, err := os.Create(cleanFilename) //nolint:gosec // G304: filename is cleaned above
	if err != nil {
		return errors.New(err).
			Component("birdweather").
			Category(errors.CategoryFileIO).
			FileContext(cleanFilename, 0).
			Context("operation", "create_file").
			Build()
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Warn("Failed to close audio file", logger.Error(err))
		}
	}()

	// Write the buffer to the file
	if _, err := io.Copy(file, buffer); err != nil {
		return errors.New(err).
			Component("birdweather").
			Category(errors.CategoryFileIO).
			FileContext(cleanFilename, int64(bufferSize)).
			Context("operation", "write_file").
			Build()
	}

	// Get the actual file size from the filesystem
	fileInfo, err := os.Stat(cleanFilename)
	if err != nil {
		log.Warn("Couldn't get file size", logger.Error(err))
	}
	actualFileSize := int64(0)
	if fileInfo != nil {
		actualFileSize = fileInfo.Size()
	}

	// Create a metadata file with the same name but .txt extension
	metaFilename := filepath.Clean(cleanFilename[:len(cleanFilename)-len(filepath.Ext(cleanFilename))] + ".txt")
	metaFile, err := os.Create(metaFilename) //nolint:gosec // G304: filename is cleaned above
	if err != nil {
		log.Warn("Could not create metadata file", logger.Error(err))
		return nil // Continue even if metadata file creation fails
	}
	defer func() {
		if err := metaFile.Close(); err != nil {
			log.Warn("Failed to close metadata file", logger.Error(err))
		}
	}()

	// Write timestamp information to the metadata file
	fileExt := filepath.Ext(filename)
	metaInfo := fmt.Sprintf("File: %s\n", filepath.Base(filename))
	metaInfo += fmt.Sprintf("Format: %s\n", fileExt)
	metaInfo += fmt.Sprintf("Start Time: %s\n", startTime.Format(time.RFC3339))
	metaInfo += fmt.Sprintf("End Time: %s\n", endTime.Format(time.RFC3339))
	metaInfo += fmt.Sprintf("Duration: %s\n", endTime.Sub(startTime))
	metaInfo += fmt.Sprintf("File Size: %d bytes\n", actualFileSize)
	metaInfo += fmt.Sprintf("Buffer Size: %d bytes\n", bufferSize)

	metaInfo += "Sample Rate: 22050 Hz\n"
	metaInfo += "Bits Per Sample: 16\n"
	metaInfo += "Channels: 1\n"

	if _, err := metaFile.WriteString(metaInfo); err != nil {
		log.Warn("Could not write to metadata file", logger.Error(err))
	}

	return nil
}
