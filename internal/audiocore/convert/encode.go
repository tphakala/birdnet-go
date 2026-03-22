package convert

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	// wavNumChannels is the number of audio channels for WAV output (mono).
	wavNumChannels = 1

	// wavAudioFormat is the PCM audio format identifier for WAV files.
	wavAudioFormat = 1
)

// SavePCMDataToWAV saves raw 16-bit PCM data as a WAV file at filePath.
// sampleRate specifies the sample rate in Hz (e.g. 48000), and bitDepth specifies
// the number of bits per sample (e.g. 16). The output is always mono.
// Parent directories are created automatically if they do not exist.
func SavePCMDataToWAV(filePath string, pcmData []byte, sampleRate, bitDepth int) error {
	if filePath == "" {
		return errors.Newf("empty file path provided for WAV save operation").
			Component("audiocore/convert").
			Category(errors.CategoryValidation).
			Context("operation", "save_pcm_to_wav").
			Build()
	}

	if len(pcmData) == 0 {
		return errors.Newf("empty PCM data provided for WAV save operation").
			Component("audiocore/convert").
			Category(errors.CategoryValidation).
			Context("operation", "save_pcm_to_wav").
			Context("data_size", 0).
			Build()
	}

	if bitDepth != 16 {
		return errors.Newf("unsupported bit depth %d: SavePCMDataToWAV requires 16-bit PCM", bitDepth).
			Component("audiocore/convert").
			Category(errors.CategoryValidation).
			Context("operation", "save_pcm_to_wav").
			Context("bit_depth", bitDepth).
			Build()
	}

	bytesPerSample := bitDepth / 8
	if len(pcmData)%bytesPerSample != 0 {
		return errors.Newf("PCM data size (%d bytes) is not aligned with bit depth (%d bits, %d bytes per sample)", len(pcmData), bitDepth, bytesPerSample).
			Component("audiocore/convert").
			Category(errors.CategoryValidation).
			Context("operation", "save_pcm_to_wav").
			Context("data_size", len(pcmData)).
			Context("bit_depth", bitDepth).
			Context("bytes_per_sample", bytesPerSample).
			Build()
	}

	if err := os.MkdirAll(filepath.Dir(filePath), 0o750); err != nil {
		return errors.New(err).
			Component("audiocore/convert").
			Category(errors.CategoryFileIO).
			Context("operation", "save_pcm_to_wav").
			Context("file_operation", "create_directories").
			Build()
	}

	outFile, err := os.Create(filePath) //nolint:gosec // G304: filePath is constructed programmatically, not from raw user input
	if err != nil {
		return errors.New(err).
			Component("audiocore/convert").
			Category(errors.CategoryFileIO).
			Context("operation", "save_pcm_to_wav").
			Context("file_operation", "create_file").
			Build()
	}
	defer func() {
		// Best-effort close; encoder.Close() is the authoritative finalization step.
		_ = outFile.Close()
	}()

	enc := wav.NewEncoder(outFile, sampleRate, bitDepth, wavNumChannels, wavAudioFormat)
	if enc == nil {
		return errors.Newf("failed to create WAV encoder").
			Component("audiocore/convert").
			Category(errors.CategorySystem).
			Context("operation", "save_pcm_to_wav").
			Context("sample_rate", sampleRate).
			Context("bit_depth", bitDepth).
			Build()
	}

	intSamples := byteSliceToInts(pcmData)

	buf := &audio.IntBuffer{
		Data:   intSamples,
		Format: &audio.Format{SampleRate: sampleRate, NumChannels: wavNumChannels},
	}
	if err := enc.Write(buf); err != nil {
		return errors.New(err).
			Component("audiocore/convert").
			Category(errors.CategoryFileIO).
			Context("operation", "save_pcm_to_wav").
			Context("file_operation", "write_samples").
			Context("sample_count", len(intSamples)).
			Build()
	}

	if err := enc.Close(); err != nil {
		return errors.New(err).
			Component("audiocore/convert").
			Category(errors.CategoryFileIO).
			Context("operation", "save_pcm_to_wav").
			Context("file_operation", "close_encoder").
			Build()
	}

	return nil
}

// byteSliceToInts converts a byte slice of little-endian 16-bit PCM samples to a slice of int.
// Each pair of bytes is interpreted as a single signed 16-bit sample.
func byteSliceToInts(pcmData []byte) []int {
	samples := make([]int, 0, len(pcmData)/2)
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
