package readfile

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// readWAVInfo reads audio metadata from an open WAV file.
func readWAVInfo(file *os.File) (AudioInfo, error) {
	decoder := wav.NewDecoder(file)
	decoder.ReadInfo()

	if !decoder.IsValidFile() {
		return AudioInfo{}, errors.Newf("invalid WAV file format").
			Component("audiocore/readfile").
			Category(errors.CategoryValidation).
			Context("operation", "read_wav_info").
			Build()
	}

	if decoder.BitDepth != 16 && decoder.BitDepth != 24 && decoder.BitDepth != 32 {
		return AudioInfo{}, errors.Newf("unsupported bit depth: %d", decoder.BitDepth).
			Component("audiocore/readfile").
			Category(errors.CategoryValidation).
			Context("operation", "read_wav_info").
			Context("bit_depth", int(decoder.BitDepth)).
			Build()
	}

	if decoder.NumChans != 1 && decoder.NumChans != 2 {
		return AudioInfo{}, errors.Newf("unsupported number of channels: %d", decoder.NumChans).
			Component("audiocore/readfile").
			Category(errors.CategoryValidation).
			Context("operation", "read_wav_info").
			Context("num_channels", int(decoder.NumChans)).
			Build()
	}

	duration, err := decoder.Duration()
	if err != nil {
		return AudioInfo{}, errors.Newf("unable to determine WAV duration: %w", err).
			Component("audiocore/readfile").
			Category(errors.CategoryFileIO).
			Context("operation", "read_wav_info").
			Build()
	}
	totalSamples := int(duration.Seconds() * float64(decoder.SampleRate))

	return AudioInfo{
		SampleRate:   int(decoder.SampleRate),
		TotalSamples: totalSamples,
		NumChannels:  int(decoder.NumChans),
		BitDepth:     int(decoder.BitDepth),
	}, nil
}

// ReadWAVBuffered reads audio samples from an open WAV file and delivers them
// to callback in overlapping 3-second chunks.
//
// chunkSize is the number of samples per analysis window (typically 3 * sampleRate).
// overlap is the overlap between successive windows in seconds.
// targetSampleRate is the desired output sample rate; if it differs from the file's
// sample rate a resampler function must be supplied via resample.
// resample may be nil when no resampling is needed.
func ReadWAVBuffered(
	file *os.File,
	chunkSize int,
	overlap float64,
	targetSampleRate int,
	resample func([]float32, int, int) ([]float32, error),
	callback AudioChunkCallback,
) error {
	decoder := wav.NewDecoder(file)
	decoder.ReadInfo()

	if !decoder.IsValidFile() {
		return errors.Newf("input is not a valid WAV audio file").
			Component("audiocore/readfile").
			Category(errors.CategoryValidation).
			Context("operation", "read_wav_buffered").
			Build()
	}

	doResample := decoder.SampleRate != uint32(targetSampleRate) //nolint:gosec // G115: targetSampleRate is a positive audio sample rate
	sourceSampleRate := int(decoder.SampleRate)

	if doResample && resample == nil {
		return errors.Newf("resampling required (%d Hz to %d Hz) but no resample function provided", sourceSampleRate, targetSampleRate).
			Component("audiocore/readfile").
			Category(errors.CategoryValidation).
			Context("operation", "read_wav_buffered").
			Context("source_rate", sourceSampleRate).
			Context("target_rate", targetSampleRate).
			Build()
	}

	divisor, err := getAudioDivisor(int(decoder.BitDepth))
	if err != nil {
		return err
	}

	step := int((chunkDurationSeconds - overlap) * float64(targetSampleRate))
	minLenSamples := int(minChunkDurationSeconds * float64(targetSampleRate))
	secondsSamples := chunkSize

	// Get file size to decide processing approach.
	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("error getting file info: %w", err)
	}

	fileSize := fileInfo.Size()
	isLargeFile := fileSize > 1*1024*1024*1024 // 1 GB threshold

	if isLargeFile {
		return readWAVDirectBytes(file, decoder, divisor, secondsSamples, step, minLenSamples,
			doResample, sourceSampleRate, targetSampleRate, resample, callback)
	}

	// Buffered approach for smaller files.
	// Buffer size = 8 chunks × 3 s × 48000 samples = 1,152,000 samples ≈ 2.3 MB.
	const bufferSize = 1_152_000
	buf := &audio.IntBuffer{
		Data:   make([]int, bufferSize),
		Format: &audio.Format{SampleRate: targetSampleRate, NumChannels: 1},
	}

	var currentChunk []float32

	for {
		n, decErr := decoder.PCMBuffer(buf)
		if decErr != nil {
			return decErr
		}

		if n == 0 {
			break
		}

		floatChunk := make([]float32, n)
		for i, sample := range buf.Data[:n] {
			floatChunk[i] = float32(sample) / divisor
		}

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

// readWAVDirectBytes handles very large WAV files by reading raw PCM bytes directly
// rather than using the decoder's buffered path.
func readWAVDirectBytes(
	file *os.File,
	decoder *wav.Decoder,
	divisor float32,
	secondsSamples, step, minLenSamples int,
	doResample bool,
	sourceSampleRate, targetSampleRate int,
	resample func([]float32, int, int) ([]float32, error),
	callback AudioChunkCallback,
) error {
	if err := seekToDataChunk(file); err != nil {
		return fmt.Errorf("error seeking to data chunk: %w", err)
	}

	bytesPerSample := int(decoder.BitDepth / 8)
	bytesPerFrame := bytesPerSample * int(decoder.NumChans)

	// Read raw blocks of chunkDurationSeconds each.
	chunkSamples := int(decoder.SampleRate) * chunkDurationSeconds
	blockSize := chunkSamples * bytesPerFrame
	buffer := make([]byte, blockSize)

	var currentChunk []float32

	for {
		bytesRead, err := file.Read(buffer)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("error reading WAV data: %w", err)
		}

		if bytesRead == 0 {
			break
		}

		floatChunk, err := convertPCMToFloat32(buffer[:bytesRead], bytesPerSample, int(decoder.NumChans), divisor)
		if err != nil {
			return fmt.Errorf("error converting PCM data: %w", err)
		}

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

// seekToDataChunk advances file to the beginning of the WAV PCM data chunk.
func seekToDataChunk(file *os.File) error {
	// Seek past the RIFF chunk header (12 bytes).
	if _, err := file.Seek(12, io.SeekStart); err != nil {
		return err
	}

	var header [4]byte
	var size uint32

	for {
		if _, err := io.ReadFull(file, header[:]); err != nil {
			return err
		}

		if err := binary.Read(file, binary.LittleEndian, &size); err != nil {
			return err
		}

		if string(header[:]) == "data" {
			return nil
		}

		if _, err := file.Seek(int64(size), io.SeekCurrent); err != nil {
			return err
		}
	}
}

// convertPCMToFloat32 converts a slice of raw PCM bytes to normalised float32
// samples, averaging across channels.
func convertPCMToFloat32(data []byte, bytesPerSample, numChannels int, divisor float32) ([]float32, error) {
	frameBytes := bytesPerSample * numChannels
	if len(data)%frameBytes != 0 {
		return nil, fmt.Errorf("invalid PCM data length: %d bytes is not divisible by %d bytes per frame",
			len(data), frameBytes)
	}

	frameCount := len(data) / frameBytes
	result := make([]float32, frameCount)

	for i := range frameCount {
		var sum int32

		for ch := range numChannels {
			offset := i*frameBytes + ch*bytesPerSample
			var sample int32

			switch bytesPerSample {
			case 2: // 16-bit
				sample = int32(int16(binary.LittleEndian.Uint16(data[offset : offset+2]))) //nolint:gosec // G115: WAV audio sample conversion within 16-bit range
			case 3: // 24-bit little-endian with sign extension
				b0, b1, b2 := int32(data[offset]), int32(data[offset+1]), int32(data[offset+2])
				sample = (b2 << 16) | (b1 << 8) | b0
				if (sample & 0x800000) != 0 {
					sample |= -0x800000
				}
			case 4: // 32-bit
				sample = int32(binary.LittleEndian.Uint32(data[offset : offset+4])) //nolint:gosec // G115: WAV audio sample conversion within 32-bit range
			default:
				return nil, fmt.Errorf("unsupported bytes per sample: %d", bytesPerSample)
			}

			sum += sample
		}

		result[i] = float32(sum) / float32(numChannels) / divisor
	}

	return result, nil
}

// flushFinalChunk pads and delivers the last audio chunk, then signals EOF.
func flushFinalChunk(currentChunk []float32, secondsSamples, minLenSamples int, callback AudioChunkCallback) error {
	if len(currentChunk) >= minLenSamples || len(currentChunk) > 0 {
		if len(currentChunk) < secondsSamples {
			padding := make([]float32, secondsSamples-len(currentChunk))
			currentChunk = append(currentChunk, padding...)
		}
		return callback(currentChunk, true)
	}

	// No data at all — still signal EOF.
	return callback(nil, true)
}
