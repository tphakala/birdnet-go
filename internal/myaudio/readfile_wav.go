package myaudio

import (
	"errors"
	"fmt"
	"os"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func readWAVInfo(file *os.File) (AudioInfo, error) {
	decoder := wav.NewDecoder(file)
	decoder.ReadInfo()

	if !decoder.IsValidFile() {
		return AudioInfo{}, errors.New("invalid WAV file format")
	}

	// Additional WAV-specific validations
	if decoder.BitDepth != 16 && decoder.BitDepth != 24 && decoder.BitDepth != 32 {
		return AudioInfo{}, fmt.Errorf("unsupported bit depth: %d", decoder.BitDepth)
	}

	if decoder.NumChans != 1 && decoder.NumChans != 2 {
		return AudioInfo{}, fmt.Errorf("unsupported number of channels: %d", decoder.NumChans)
	}

	// Get file size in bytes
	fileInfo, err := file.Stat()
	if err != nil {
		return AudioInfo{}, err
	}

	// Calculate total samples
	bytesPerSample := int(decoder.BitDepth / 8)
	totalSamples := int(fileInfo.Size()) / bytesPerSample / int(decoder.NumChans)

	return AudioInfo{
		SampleRate:   int(decoder.SampleRate),
		TotalSamples: totalSamples,
		NumChannels:  int(decoder.NumChans),
		BitDepth:     int(decoder.BitDepth),
	}, nil
}

func readWAVBuffered(file *os.File, settings *conf.Settings, callback AudioChunkCallback) error {
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

	divisor, err := getAudioDivisor(int(decoder.BitDepth))
	if err != nil {
		return err
	}

	step := int((3 - settings.BirdNET.Overlap) * conf.SampleRate)
	minLenSamples := int(1.5 * conf.SampleRate)
	secondsSamples := int(3 * conf.SampleRate)

	var currentChunk []float32
	// Calculate buffer size for 8 complete chunks of audio
	// One chunk = 3 seconds = 48000 * 3 = 144000 samples
	// Using 8 chunks worth of data = 1,152,000 samples
	// This provides 24 seconds of buffered audio
	// Memory usage: 1,152,000 samples * 2 bytes = 2.3MB
	bufferSize := 1_152_000
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
