// audio.go contains helper functions for audio file handling
package birdweather

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"
)

// WAVHeaderSize is the standard size of a WAV file header in bytes
const WAVHeaderSize = 44

// saveBufferToFile writes a bytes.Buffer containing audio data to a file, along with
// timestamp and format information for debugging purposes.
func saveBufferToFile(buffer *bytes.Buffer, filename string, startTime, endTime time.Time) error {
	// Validate input parameters
	if buffer == nil {
		return fmt.Errorf("buffer is nil")
	}

	// Get the buffer size before any operations that might consume it
	bufferSize := buffer.Len()

	// Create directory if it doesn't exist
	dirPath := filepath.Dir(filename)
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		return fmt.Errorf("error creating directory: %w", err)
	}

	// Save the audio file
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("error creating file: %w", err)
	}
	defer file.Close()

	// Write the buffer to the file
	if _, err := io.Copy(file, buffer); err != nil {
		return fmt.Errorf("error writing buffer to file: %w", err)
	}

	// Get the actual file size from the filesystem
	fileInfo, err := os.Stat(filename)
	if err != nil {
		log.Printf("Warning: couldn't get file size: %v", err)
	}
	actualFileSize := int64(0)
	if fileInfo != nil {
		actualFileSize = fileInfo.Size()
	}

	// Create a metadata file with the same name but .txt extension
	metaFilename := filename[:len(filename)-len(filepath.Ext(filename))] + ".txt"
	metaFile, err := os.Create(metaFilename)
	if err != nil {
		log.Printf("Warning: could not create metadata file: %v", err)
		return nil // Continue even if metadata file creation fails
	}
	defer metaFile.Close()

	// Write timestamp information to the metadata file
	fileExt := filepath.Ext(filename)
	metaInfo := fmt.Sprintf("File: %s\n", filepath.Base(filename))
	metaInfo += fmt.Sprintf("Format: %s\n", fileExt)
	metaInfo += fmt.Sprintf("Start Time: %s\n", startTime.Format(time.RFC3339))
	metaInfo += fmt.Sprintf("End Time: %s\n", endTime.Format(time.RFC3339))
	metaInfo += fmt.Sprintf("Duration: %s\n", endTime.Sub(startTime))
	metaInfo += fmt.Sprintf("File Size: %d bytes\n", actualFileSize)
	metaInfo += fmt.Sprintf("Buffer Size: %d bytes\n", bufferSize)

	// Add format-specific info if known (basic for now)
	if fileExt == ".wav" {
		// Calculate estimated PCM data size for WAV
		pcmDataSize := actualFileSize - WAVHeaderSize
		if pcmDataSize < 0 {
			pcmDataSize = 0
		}
		metaInfo += fmt.Sprintf("Estimated PCM Data Size: %d bytes\n", pcmDataSize)
		metaInfo += fmt.Sprintf("Expected Audio Duration (PCM): %.3f seconds\n",
			float64(pcmDataSize)/(48000.0*2.0))
	}
	metaInfo += "Sample Rate: 48000 Hz\n"
	metaInfo += "Bits Per Sample: 16\n"
	metaInfo += "Channels: 1\n"

	if _, err := metaFile.WriteString(metaInfo); err != nil {
		log.Printf("Warning: could not write to metadata file: %v", err)
	}

	return nil
}
