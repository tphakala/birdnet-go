package myaudio

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// AudioChunkCallback is a function type that processes audio chunks
type AudioChunkCallback func([]float32) error

// GetAudioInfo returns basic information about the audio file
type AudioInfo struct {
	SampleRate   int
	TotalSamples int
	NumChannels  int
	BitDepth     int
}

// GetTotalChunks calculates the total number of chunks for a given audio file
func GetTotalChunks(sampleRate, totalSamples int, overlap float64) int {
	chunkSamples := 3 * sampleRate                          // samples in 3 seconds
	stepSamples := int((3 - overlap) * float64(sampleRate)) // samples per step based on overlap

	if stepSamples <= 0 {
		return 0
	}

	// Calculate total chunks including partial chunks, rounding up
	return (totalSamples - chunkSamples + stepSamples + (stepSamples - 1)) / stepSamples
}

func GetAudioInfo(filePath string) (AudioInfo, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return AudioInfo{}, err
	}
	defer file.Close()

	// Get file extension
	ext := filepath.Ext(filePath)

	switch ext {
	case ".wav":
		decoder := wav.NewDecoder(file)
		decoder.ReadInfo()
		if !decoder.IsValidFile() {
			return AudioInfo{}, errors.New("input is not a valid WAV audio file")
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

	default:
		return AudioInfo{}, fmt.Errorf("unsupported audio format: %s", ext)
	}
}

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
