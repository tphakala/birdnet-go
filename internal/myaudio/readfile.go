package myaudio

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// AudioChunkCallback is a function type that processes audio chunks
// The second parameter (isEOF) indicates when EOF has been reached in the audio file
type AudioChunkCallback func([]float32, bool) error

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

	ext := strings.ToLower(filepath.Ext(filePath))

	switch ext {
	case ".wav":
		return readWAVInfo(file)
	case ".flac":
		return readFLACInfo(file)
	default:
		return AudioInfo{}, fmt.Errorf("unsupported audio format: %s", ext)
	}
}

// ReadAudioFileBuffered reads and processes audio data in chunks
func ReadAudioFileBuffered(settings *conf.Settings, callback AudioChunkCallback) error {
	file, err := os.Open(settings.Input.Path)
	if err != nil {
		return err
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(settings.Input.Path))

	switch ext {
	case ".wav":
		return readWAVBuffered(file, settings, callback)
	case ".flac":
		return readFLACBuffered(file, settings, callback)
	default:
		return fmt.Errorf("unsupported audio format: %s", ext)
	}
}

// getAudioDivisor returns the appropriate divisor for converting samples based on bit depth
func getAudioDivisor(bitDepth int) (float32, error) {
	switch bitDepth {
	case 16:
		return 32768.0, nil
	case 24:
		return 8388608.0, nil
	case 32:
		return 2147483648.0, nil
	default:
		return 0, errors.New("unsupported audio file bit depth")
	}
}
