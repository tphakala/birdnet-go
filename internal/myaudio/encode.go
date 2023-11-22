package myaudio

import (
	"bytes"
	"encoding/binary"
	"os"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
	libflac "github.com/go-musicfox/goflac"
	liblame "github.com/viert/go-lame"
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

func savePCMDataToFlac(filePath string, pcmData []byte) error {
	encoder, err := libflac.NewEncoder(filePath, 1, 16, 48000)
	if err != nil {
		return err // handle error
	}

	// Convert pcmData from []byte to []int32
	int32Buffer := make([]int32, len(pcmData)/2)
	for i := 0; i < len(pcmData); i += 2 {
		int32Buffer[i/2] = int32(pcmData[i]) | int32(pcmData[i+1])<<8
	}

	// Prepare the frame with the converted buffer
	frame := libflac.Frame{
		Channels: 1,
		Depth:    16,
		Rate:     48000,
		Buffer:   int32Buffer,
	}

	// Encode the frame
	if err := encoder.WriteFrame(frame); err != nil {
		encoder.Close()
		return err // handle error
	}

	// Close the encoder
	encoder.Close()

	return nil
}

func savePCMDataToMP3(filePath string, pcmData []byte) error {
	// Open a file for the MP3 output
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Create a new MP3 encoder writing to the file
	encoder := liblame.NewEncoder(file)
	defer encoder.Close()

	// Set encoder parameters
	encoder.SetNumChannels(saveChannelCount)
	encoder.SetInSamplerate(saveSampleRate)
	encoder.SetBrate(saveBitDepth) // This sets the bitrate, adjust as needed
	encoder.SetQuality(2)          // Adjust quality as needed
	encoder.SetMode(3)             // mono
	encoder.SetVBR(3)              // VBR mode
	encoder.SetVBRMeanBitrateKbps(160)

	// Write PCM data to the encoder
	if _, err := encoder.Write(pcmData); err != nil {
		return err
	}

	// Flush any remaining data in the encoder
	if _, err := encoder.Flush(); err != nil {
		return err
	}

	return nil
}
