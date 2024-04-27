// audio.go this contains code for encoding PCM to WAV format in memory.
package birdweather

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// encodePCMtoWAV creates WAV data from PCM and returns it in a bytes.Buffer.
func encodePCMtoWAV(pcmData []byte) (*bytes.Buffer, error) {
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
			if err := binary.Write(buffer, binary.LittleEndian, b); err != nil {
				return nil, err
			}
		} else {
			// Handle all other data types
			if err := binary.Write(buffer, binary.LittleEndian, elem); err != nil {
				return nil, err
			}
		}
	}

	return buffer, nil
}

// saveBufferToDisk writes a bytes.Buffer to a file, this is only used for debugging.
// golangci-lint:ignore unused for now
func saveBufferToDisk(buffer *bytes.Buffer, filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("error creating file: %w", err)
	}
	defer file.Close()

	_, err = io.Copy(file, buffer)
	if err != nil {
		return fmt.Errorf("error writing buffer to file: %w", err)
	}
	return nil
}
