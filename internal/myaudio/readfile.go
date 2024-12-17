package myaudio

import (
	"errors"
	"fmt"
	"os"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// AudioChunkCallback is a function type that processes audio chunks
type AudioChunkCallback func([]float32) error

// ReadAudioFileBuffered reads and processes audio data in chunks
func ReadAudioFileBuffered(settings *conf.Settings, callback AudioChunkCallback) error {
	fmt.Print("- Reading audio data")

	file, err := os.Open(settings.Input.Path)
	if err != nil {
		return err
	}
	defer file.Close()

	decoder := wav.NewDecoder(file)
	decoder.ReadInfo()
	if !decoder.IsValidFile() {
		return errors.New("input is not a valid WAV audio file")
	}

	if settings.Debug {
		fmt.Println("File is valid wav: ", decoder.IsValidFile())
		fmt.Println("Sample rate:", decoder.SampleRate)
		fmt.Println("Bits per sample:", decoder.BitDepth)
		fmt.Println("Channels:", decoder.NumChans)
	}

	var doResample bool = false
	var sourceSampleRate int = int(decoder.SampleRate)

	if decoder.SampleRate != conf.SampleRate {
		doResample = true
		//return nil, errors.New("input file sample rate is not valid for BirdNet model")
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
		return errors.New("unsupported audio file bit depth")
	}

	step := int((3 - settings.BirdNET.Overlap) * conf.SampleRate)
	minLenSamples := int(1.5 * conf.SampleRate)
	secondsSamples := int(3 * conf.SampleRate)

	var currentChunk []float32
	// Use a smaller buffer size, e.g., 1MB worth of samples
	bufferSize := 262144 // Adjust this value based on your needs
	buf := &audio.IntBuffer{
		Data:   make([]int, bufferSize),
		Format: &audio.Format{SampleRate: int(conf.SampleRate), NumChannels: conf.NumChannels},
	}

	for {
		n, err := decoder.PCMBuffer(buf)
		if err != nil {
			return err
		}

		if n == 0 {
			break
		}

		var floatChunk []float32
		for _, sample := range buf.Data[:n] {
			floatChunk = append(floatChunk, float32(sample)/divisor)
		}

		if doResample {
			floatChunk, err = ResampleAudio(floatChunk, sourceSampleRate, conf.SampleRate)
			if err != nil {
				return fmt.Errorf("error resampling audio: %w", err)
			}
		}

		currentChunk = append(currentChunk, floatChunk...)

		// Process complete 3-second chunks
		for len(currentChunk) >= secondsSamples {
			if err := callback(currentChunk[:secondsSamples]); err != nil {
				return err
			}
			currentChunk = currentChunk[step:]
		}
	}

	// Handle the last chunk
	if len(currentChunk) >= minLenSamples {
		if len(currentChunk) < secondsSamples {
			padding := make([]float32, secondsSamples-len(currentChunk))
			currentChunk = append(currentChunk, padding...)
		}
		if err := callback(currentChunk); err != nil {
			return err
		}
	}

	return nil
}
