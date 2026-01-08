package myaudio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
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
		log := GetLogger()
		log.Debug("WAV file info",
			logger.Bool("valid", decoder.IsValidFile()),
			logger.Int("sample_rate", int(decoder.SampleRate)),
			logger.Int("bits_per_sample", int(decoder.BitDepth)),
			logger.Int("channels", int(decoder.NumChans)))
	}

	doResample := false
	sourceSampleRate := int(decoder.SampleRate)

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

	// Get file size to decide processing approach
	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("error getting file info: %w", err)
	}

	fileSize := fileInfo.Size()
	isLargeFile := fileSize > 1*1024*1024*1024 // 1GB threshold

	if settings.Debug {
		log := GetLogger()
		approach := "buffered decoder"
		if isLargeFile {
			approach = "direct byte reading"
		}
		log.Debug("WAV processing approach",
			logger.Float64("file_size_gb", float64(fileSize)/(1024*1024*1024)),
			logger.String("approach", approach))
	}

	if isLargeFile {
		// For extremely large files, use direct file reading approach
		return readWAVDirectBytes(file, decoder, settings, divisor, secondsSamples, step, minLenSamples,
			doResample, sourceSampleRate, callback)
	}

	// For smaller files, use the existing buffered approach
	// Calculate buffer size for 8 complete chunks of audio
	// One chunk = 3 seconds = 48000 * 3 = 144000 samples
	// Using 8 chunks worth of data = 1,152,000 samples
	// This provides 24 seconds of buffered audio
	// Memory usage: 1,152,000 samples * 2 bytes = 2.3MB
	bufferSize := 22050 * 3 * 8
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

// readWAVDirectBytes processes very large WAV files by reading and processing small chunks of bytes directly
func readWAVDirectBytes(file *os.File, decoder *wav.Decoder, settings *conf.Settings,
	divisor float32, secondsSamples, step, minLenSamples int,
	doResample bool, sourceSampleRate int, callback AudioChunkCallback) error {

	// Create a more manageable buffer for reading - just read a few seconds at a time
	// This ensures we don't try to load too much data at once
	chunkDuration := 3 * time.Second // Read 3 seconds at a time
	chunkSamples := int(chunkDuration.Seconds() * float64(decoder.SampleRate))

	// Start by seeking to the data chunk
	if err := seekToDataChunk(file); err != nil {
		return fmt.Errorf("error seeking to data chunk: %w", err)
	}

	bytesPerSample := int(decoder.BitDepth / 8)
	bytesPerFrame := bytesPerSample * int(decoder.NumChans)

	// Read data in smaller blocks to avoid memory issues
	blockSize := chunkSamples * bytesPerFrame
	buffer := make([]byte, blockSize)

	var currentChunk []float32

	if settings.Debug {
		log := GetLogger()
		log.Debug("processing large WAV file",
			logger.Int("block_size_bytes", blockSize),
			logger.Int("bytes_per_sample", bytesPerSample),
			logger.Int("bytes_per_frame", bytesPerFrame),
			logger.Int("sample_rate", int(decoder.SampleRate)),
			logger.Int("chunk_duration_seconds", int(chunkDuration.Seconds())))
	}

	for {
		// Read a block of raw PCM data
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

		if settings.Debug && bytesRead < blockSize {
			log := GetLogger()
			log.Debug("read partial block",
				logger.Int("bytes_read", bytesRead),
				logger.Int("block_size", blockSize))
		}

		// Convert raw bytes to float32 samples
		floatChunk, err := convertPCMToFloat32(buffer[:bytesRead], bytesPerSample, int(decoder.NumChans), divisor)
		if err != nil {
			return fmt.Errorf("error converting PCM data: %w", err)
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

// seekToDataChunk seeks the file to the beginning of the PCM data chunk
func seekToDataChunk(file *os.File) error {
	// Reset to beginning of file
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}

	// Standard WAV header is 44 bytes, but we'll look for the data chunk to be safe
	var header [4]byte
	var size uint32

	// Skip RIFF chunk header (12 bytes)
	if _, err := file.Seek(12, 0); err != nil {
		return err
	}

	// Look for the 'data' chunk
	for {
		// Read chunk ID
		if _, err := file.Read(header[:]); err != nil {
			return err
		}

		// Read chunk size (4 bytes)
		if err := binary.Read(file, binary.LittleEndian, &size); err != nil {
			return err
		}

		// If we found the data chunk, stop
		if string(header[:]) == "data" {
			return nil
		}

		// Skip this chunk
		if _, err := file.Seek(int64(size), 1); err != nil {
			return err
		}
	}
}

// convertPCMToFloat32 converts raw PCM bytes to float32 samples
func convertPCMToFloat32(data []byte, bytesPerSample, numChannels int, divisor float32) ([]float32, error) {
	if len(data)%(bytesPerSample*numChannels) != 0 {
		return nil, fmt.Errorf("invalid PCM data length: %d bytes is not divisible by %d bytes per frame",
			len(data), bytesPerSample*numChannels)
	}

	frameCount := len(data) / (bytesPerSample * numChannels)
	result := make([]float32, frameCount)

	for i := range frameCount {
		// Average channels if needed
		var sum int32
		for ch := range numChannels {
			offset := i*bytesPerSample*numChannels + ch*bytesPerSample
			var sample int32

			// Convert bytes to sample based on bit depth
			switch bytesPerSample {
			case 2: // 16-bit
				sample = int32(int16(binary.LittleEndian.Uint16(data[offset : offset+2]))) //nolint:gosec // G115: WAV audio sample conversion within 16-bit range
			case 3: // 24-bit
				// 24-bit is typically stored as 3 bytes in little endian
				b1, b2, b3 := int32(data[offset]), int32(data[offset+1]), int32(data[offset+2])
				sample = ((b3 << 16) | (b2 << 8) | b1)
				// Sign extension for negative values
				if (sample & 0x800000) != 0 {
					sample |= -0x800000 // Properly sign-extend negative values
				}
			case 4: // 32-bit
				sample = int32(binary.LittleEndian.Uint32(data[offset : offset+4])) //nolint:gosec // G115: WAV audio sample conversion within 32-bit range
			default:
				return nil, fmt.Errorf("unsupported bytes per sample: %d", bytesPerSample)
			}
			sum += sample
		}

		// Average the channels
		avg := float32(sum) / float32(numChannels) / divisor
		result[i] = avg
	}

	return result, nil
}
