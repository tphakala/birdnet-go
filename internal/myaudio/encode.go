package myaudio

import (
	"bytes"
	"encoding/binary"
	"os"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
)

const saveSampleRate = 48000
const saveBitDepth = 16
const saveChannelCount = 1

func savePCMDataToWAV(filePath string, pcmData []byte) error {
	// Open a new file for writing
	outFile, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	// Create a new WAV encoder
	enc := wav.NewEncoder(outFile, saveSampleRate, saveBitDepth, saveChannelCount, 1)

	// Convert the byte slice to int samples for the encoder
	intSamples := byteSliceToInts(pcmData)

	// Write the samples to the WAV file
	if err := enc.Write(&audio.IntBuffer{Data: intSamples, Format: &audio.Format{SampleRate: 48000, NumChannels: 1}}); err != nil {
		return err
	}

	// Close the WAV encoder which will write the WAV headers
	return enc.Close()
}

// byteSliceToInts converts a byte slice to a slice of ints (16-bit samples in this case).
func byteSliceToInts(pcmData []byte) []int {
	var samples []int
	buf := bytes.NewBuffer(pcmData)

	for {
		var sample int16
		if err := binary.Read(buf, binary.LittleEndian, &sample); err != nil {
			break
		}
		samples = append(samples, int(sample))
	}

	return samples
}
