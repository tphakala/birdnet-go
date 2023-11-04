package myaudio

import (
	"errors"
	"fmt"
	"os"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
	"github.com/tphakala/go-birdnet/pkg/config"
)

// Required sample rate for input audio data
const SampleRate = 48000

// Read 48000 sample rate WAV file into 3 second chunks
func ReadAudioFile(cfg *config.Settings) ([][]float32, error) {
	fmt.Print("- Reading audio data")

	file, err := os.Open(cfg.InputFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	decoder := wav.NewDecoder(file)
	decoder.ReadInfo()
	if !decoder.IsValidFile() {
		return nil, errors.New("input is not a valid WAV audio file")
	}

	if cfg.Debug {
		fmt.Println("File is valid wav: ", decoder.IsValidFile())
		fmt.Println("Sample rate:", decoder.SampleRate)
		fmt.Println("Bits per sample:", decoder.BitDepth)
		fmt.Println("Channels:", decoder.NumChans)
	}

	if decoder.SampleRate != SampleRate {
		return nil, errors.New("input file sample rate is not valid for BirdNet model")
	}

	// Divisor for converting audio sample chunk from int to float32
	var divisor float32

	switch decoder.BitDepth {
	case 16:
		divisor = 32768.0
	case 24:
		divisor = 8388608.0
	case 32:
		divisor = 2147483648.0
	default:
		return nil, errors.New("unsupported audio file bit depth")
	}

	step := int((3 - cfg.Overlap) * SampleRate)
	minLenSamples := int(1.5 * SampleRate)
	secondsSamples := int(3 * SampleRate)

	var chunks [][]float32
	var currentChunk []float32

	buf := &audio.IntBuffer{Data: make([]int, step), Format: &audio.Format{SampleRate: int(SampleRate), NumChannels: 1}}

	for {
		n, err := decoder.PCMBuffer(buf)
		if err != nil {
			return nil, err
		}

		// If no data is read, we've reached the end of the file
		if n == 0 {
			break
		}

		for _, sample := range buf.Data[:n] {
			// Convert sample from int to float32 type
			currentChunk = append(currentChunk, float32(sample)/divisor)

			if len(currentChunk) == secondsSamples {
				chunks = append(chunks, currentChunk)
				currentChunk = currentChunk[step:]
			}
		}
	}

	// Handle the last chunk
	if len(currentChunk) >= minLenSamples {
		if len(currentChunk) < secondsSamples {
			padding := make([]float32, secondsSamples-len(currentChunk))
			currentChunk = append(currentChunk, padding...)
		}
		chunks = append(chunks, currentChunk)
	}

	return chunks, nil
}
