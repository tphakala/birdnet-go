package myaudio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/flac"
)

func readFLACInfo(file *os.File) (AudioInfo, error) {
	decoder, err := flac.NewDecoder(file)
	if err != nil {
		return AudioInfo{}, fmt.Errorf("invalid FLAC file: %w", err)
	}

	// Additional FLAC-specific validations
	if decoder.BitsPerSample != 16 && decoder.BitsPerSample != 24 && decoder.BitsPerSample != 32 {
		return AudioInfo{}, fmt.Errorf("unsupported bit depth: %d", decoder.BitsPerSample)
	}

	if decoder.NChannels != 1 && decoder.NChannels != 2 {
		return AudioInfo{}, fmt.Errorf("unsupported number of channels: %d", decoder.NChannels)
	}

	return AudioInfo{
		SampleRate:   decoder.SampleRate,
		TotalSamples: int(decoder.TotalSamples),
		NumChannels:  decoder.NChannels,
		BitDepth:     decoder.BitsPerSample,
	}, nil
}

func readFLACBuffered(file *os.File, settings *conf.Settings, callback AudioChunkCallback) error {
	decoder, err := flac.NewDecoder(file)
	if err != nil {
		return err
	}

	if settings.Debug {
		fmt.Println("Sample rate:", decoder.SampleRate)
		fmt.Println("Bits per sample:", decoder.BitsPerSample)
		fmt.Println("Channels:", decoder.NChannels)
	}

	var doResample bool = false
	var sourceSampleRate int = decoder.SampleRate

	if decoder.SampleRate != conf.SampleRate {
		doResample = true
	}

	divisor, err := getAudioDivisor(decoder.BitsPerSample)
	if err != nil {
		return err
	}

	step := int((3 - settings.BirdNET.Overlap) * conf.SampleRate)
	minLenSamples := int(1.5 * conf.SampleRate)
	secondsSamples := int(3 * conf.SampleRate)

	var currentChunk []float32

	// Process FLAC frames
	for {
		frame, err := decoder.Next()
		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return err
		}

		// Convert bytes to float32 samples
		var floatChunk []float32
		for i := 0; i < len(frame); i += (decoder.BitsPerSample / 8) * decoder.NChannels {
			var sample int32
			switch decoder.BitsPerSample {
			case 16:
				sample = int32(int16(binary.LittleEndian.Uint16(frame[i:])))
			case 24:
				sample = int32(frame[i]) | int32(frame[i+1])<<8 | int32(frame[i+2])<<16
			case 32:
				sample = int32(binary.LittleEndian.Uint32(frame[i:]))
			}
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
			if err := callback(currentChunk[:secondsSamples], false); err != nil {
				return err
			}
			currentChunk = currentChunk[step:]
		}
	}

	// Handle the last chunk and signal EOF
	if len(currentChunk) >= minLenSamples || len(currentChunk) > 0 {
		if len(currentChunk) < secondsSamples {
			padding := make([]float32, secondsSamples-len(currentChunk))
			currentChunk = append(currentChunk, padding...)
		}
		if err := callback(currentChunk, true); err != nil {
			return err
		}
	} else {
		// Signal EOF even if there's no final chunk to process
		if err := callback(nil, true); err != nil {
			return err
		}
	}

	return nil
}
