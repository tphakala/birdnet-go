package readfile

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/flac"
)

// readFLACInfo reads audio metadata from an open FLAC file.
func readFLACInfo(file *os.File) (AudioInfo, error) {
	decoder, err := flac.NewDecoder(file)
	if err != nil {
		return AudioInfo{}, fmt.Errorf("invalid FLAC file: %w", err)
	}

	if decoder.BitsPerSample != 16 && decoder.BitsPerSample != 24 && decoder.BitsPerSample != 32 {
		return AudioInfo{}, errors.Newf("unsupported bit depth: %d", decoder.BitsPerSample).
			Component("audiocore/readfile").
			Category(errors.CategoryValidation).
			Context("operation", "read_flac_info").
			Context("bit_depth", decoder.BitsPerSample).
			Build()
	}

	if decoder.NChannels != 1 && decoder.NChannels != 2 {
		return AudioInfo{}, errors.Newf("unsupported number of channels: %d", decoder.NChannels).
			Component("audiocore/readfile").
			Category(errors.CategoryValidation).
			Context("operation", "read_flac_info").
			Context("num_channels", decoder.NChannels).
			Build()
	}

	return AudioInfo{
		SampleRate:   decoder.SampleRate,
		TotalSamples: int(decoder.TotalSamples),
		NumChannels:  decoder.NChannels,
		BitDepth:     decoder.BitsPerSample,
	}, nil
}

// ReadFLACBuffered reads audio samples from an open FLAC file and delivers them
// to callback in overlapping 3-second chunks.
//
// chunkSize is the number of samples per analysis window (typically 3 * targetSampleRate).
// overlap is the overlap between successive windows in seconds.
// targetSampleRate is the desired output sample rate; if it differs from the
// file's sample rate a resampler function must be supplied via resample.
// resample may be nil when no resampling is needed.
func ReadFLACBuffered(
	file *os.File,
	chunkSize int,
	overlap float64,
	targetSampleRate int,
	resample func([]float32, int, int) ([]float32, error),
	callback AudioChunkCallback,
) error {
	decoder, err := flac.NewDecoder(file)
	if err != nil {
		return fmt.Errorf("invalid FLAC file: %w", err)
	}

	doResample := decoder.SampleRate != targetSampleRate
	sourceSampleRate := decoder.SampleRate

	if doResample && resample == nil {
		return errors.Newf("resampling required (%d Hz to %d Hz) but no resample function provided", sourceSampleRate, targetSampleRate).
			Component("audiocore/readfile").
			Category(errors.CategoryValidation).
			Context("operation", "read_flac_buffered").
			Context("source_rate", sourceSampleRate).
			Context("target_rate", targetSampleRate).
			Build()
	}

	divisor, err := getAudioDivisor(decoder.BitsPerSample)
	if err != nil {
		return err
	}

	step := int((chunkDurationSeconds - overlap) * float64(targetSampleRate))
	minLenSamples := int(minChunkDurationSeconds * float64(targetSampleRate))
	secondsSamples := chunkSize

	var currentChunk []float32

	for {
		frame, frameErr := decoder.Next()
		if errors.Is(frameErr, io.EOF) {
			break
		} else if frameErr != nil {
			return frameErr
		}

		floatChunk := decodeFLACFrame(frame, decoder.BitsPerSample, decoder.NChannels, divisor)

		if doResample {
			floatChunk, err = resample(floatChunk, sourceSampleRate, targetSampleRate)
			if err != nil {
				return fmt.Errorf("error resampling audio: %w", err)
			}
		}

		currentChunk = append(currentChunk, floatChunk...)

		for len(currentChunk) >= secondsSamples {
			if err := callback(currentChunk[:secondsSamples], false); err != nil {
				return err
			}
			currentChunk = currentChunk[step:]
		}
	}

	return flushFinalChunk(currentChunk, secondsSamples, minLenSamples, callback)
}

// decodeFLACFrame converts a raw FLAC frame byte slice to normalised float32 samples.
// Multi-channel frames are downmixed to mono by averaging all channels.
func decodeFLACFrame(frame []byte, bitsPerSample, nChannels int, divisor float32) []float32 {
	bytesPerSample := bitsPerSample / 8
	frameStride := bytesPerSample * nChannels
	sampleCount := len(frame) / frameStride

	result := make([]float32, sampleCount)

	for i := range sampleCount {
		var sum int32

		for ch := range nChannels {
			offset := i*frameStride + ch*bytesPerSample

			// Guard against truncated frames.
			if offset+bytesPerSample > len(frame) {
				return result[:i]
			}

			var sample int32
			switch bitsPerSample {
			case 16:
				sample = int32(int16(binary.LittleEndian.Uint16(frame[offset:]))) //nolint:gosec // G115: FLAC audio sample conversion within 16-bit range
			case 24:
				b0, b1, b2 := int32(frame[offset]), int32(frame[offset+1]), int32(frame[offset+2])
				sample = (b2 << 16) | (b1 << 8) | b0
				if (sample & 0x800000) != 0 {
					sample |= -0x800000
				}
			case 32:
				sample = int32(binary.LittleEndian.Uint32(frame[offset:])) //nolint:gosec // G115: FLAC audio sample conversion within 32-bit range
			}

			sum += sample
		}

		result[i] = float32(sum) / float32(nChannels) / divisor
	}

	return result
}
