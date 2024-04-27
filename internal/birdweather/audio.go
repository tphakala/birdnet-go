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

	// Writing the RIFF header
	binary.Write(buffer, binary.LittleEndian, []byte("RIFF"))
	binary.Write(buffer, binary.LittleEndian, chunkSize)
	binary.Write(buffer, binary.LittleEndian, []byte("WAVE"))

	// Writing the fmt sub-chunk
	binary.Write(buffer, binary.LittleEndian, []byte("fmt "))
	binary.Write(buffer, binary.LittleEndian, uint32(16)) // Sub-chunk1 size for PCM
	binary.Write(buffer, binary.LittleEndian, uint16(1))  // Audio format 1 is PCM
	binary.Write(buffer, binary.LittleEndian, uint16(numChannels))
	binary.Write(buffer, binary.LittleEndian, uint32(sampleRate))
	binary.Write(buffer, binary.LittleEndian, uint32(byteRate))
	binary.Write(buffer, binary.LittleEndian, uint16(blockAlign))
	binary.Write(buffer, binary.LittleEndian, uint16(bitDepth))

	// Writing the data sub-chunk
	binary.Write(buffer, binary.LittleEndian, []byte("data"))
	binary.Write(buffer, binary.LittleEndian, subChunk2Size)
	binary.Write(buffer, binary.LittleEndian, pcmData)

	return buffer, nil
}

// saveBufferToDisk writes a bytes.Buffer to a file, this is only used for debugging.
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
