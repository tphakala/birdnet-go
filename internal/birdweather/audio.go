// audio.go this contains code for encoding PCM to WAV format in memory.
package birdweather

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"
)

// WAVHeaderSize is the standard size of a WAV file header in bytes
const WAVHeaderSize = 44

// encodePCMtoWAV creates WAV data from PCM and returns it in a bytes.Buffer.
func encodePCMtoWAV(pcmData []byte) (*bytes.Buffer, error) {
	// Add check for empty pcmData
	if len(pcmData) == 0 {
		return nil, fmt.Errorf("pcmData is empty")
	}

	const sampleRate = 48000 // Correct sample rate is 48kHz
	const bitDepth = 16      // Bits per sample
	const numChannels = 1    // Mono audio

	// Calculating sizes and rates
	byteRate := sampleRate * numChannels * (bitDepth / 8) // 48000 * 1 * 2 = 96000 bytes per second
	blockAlign := numChannels * (bitDepth / 8)            // 1 * 2 = 2 bytes per frame
	subChunk2Size := uint32(len(pcmData))                 // Size of the data chunk in bytes
	chunkSize := 36 + subChunk2Size                       // 36 is fixed size for header

	// Initialize a buffer to build the WAV file
	buffer := bytes.NewBuffer(nil)

	// List of data elements to write sequentially to the buffer
	elements := []interface{}{
		[]byte("RIFF"), chunkSize, []byte("WAVE"),
		[]byte("fmt "), uint32(16), uint16(1), uint16(numChannels),
		uint32(sampleRate), uint32(byteRate), uint16(blockAlign), uint16(bitDepth),
		[]byte("data"), subChunk2Size, pcmData,
	}

	// Sequential write operation handling errors
	for _, elem := range elements {
		if b, ok := elem.([]byte); ok {
			// Ensure all byte slices are properly converted before writing
			if _, err := buffer.Write(b); err != nil {
				return nil, fmt.Errorf("failed to write byte slice to buffer: %w", err)
			}
		} else {
			// Handle all other data types
			if err := binary.Write(buffer, binary.LittleEndian, elem); err != nil {
				return nil, fmt.Errorf("failed to write element to buffer: %w", err)
			}
		}
	}

	return buffer, nil
}

// saveBufferToWAV writes a bytes.Buffer containing WAV data to a file, along with
// timestamp information for debugging purposes.
func saveBufferToWAV(buffer *bytes.Buffer, filename string, startTime, endTime time.Time) error {
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

	// Save the WAV file
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
	actualFileSize := fileInfo.Size()

	// Calculate the actual PCM data size (total size minus 44 bytes for the WAV header)
	pcmDataSize := actualFileSize - WAVHeaderSize

	// Create a metadata file with the same name but .txt extension
	metaFilename := filename[:len(filename)-len(filepath.Ext(filename))] + ".txt"
	metaFile, err := os.Create(metaFilename)
	if err != nil {
		log.Printf("Warning: could not create metadata file: %v", err)
		return nil // Continue even if metadata file creation fails
	}
	defer metaFile.Close()

	// Write timestamp information to the metadata file
	metaInfo := fmt.Sprintf("File: %s\n", filepath.Base(filename))
	metaInfo += fmt.Sprintf("Start Time: %s\n", startTime.Format(time.RFC3339))
	metaInfo += fmt.Sprintf("End Time: %s\n", endTime.Format(time.RFC3339))
	metaInfo += fmt.Sprintf("Duration: %s\n", endTime.Sub(startTime))
	metaInfo += fmt.Sprintf("Total File Size: %d bytes\n", actualFileSize)
	metaInfo += fmt.Sprintf("Buffer Size: %d bytes\n", bufferSize)
	metaInfo += fmt.Sprintf("PCM Data Size: %d bytes\n", pcmDataSize)
	metaInfo += fmt.Sprintf("Expected Audio Duration: %.3f seconds\n",
		float64(pcmDataSize)/(48000.0*2.0))
	metaInfo += "Sample Rate: 48000 Hz\n"
	metaInfo += "Bits Per Sample: 16\n"
	metaInfo += "Channels: 1\n"

	if _, err := metaFile.WriteString(metaInfo); err != nil {
		log.Printf("Warning: could not write to metadata file: %v", err)
	}

	return nil
}
