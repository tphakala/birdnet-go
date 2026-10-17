package wav_test

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/bits"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/wav"
	"github.com/tphakala/birdnet-go/internal/errors"
	wavpcm "github.com/tphakala/go-wav/pcm"
)

// savePCM writes pcm as a mono WAV at path through the production writer.
func savePCM(t *testing.T, path string, pcm []byte, sampleRate, bitDepth int) error {
	t.Helper()
	return wav.EncodePCM(t.Context(), &wav.Options{
		PCMData:    pcm,
		OutputPath: path,
		SampleRate: sampleRate,
		BitDepth:   bitDepth,
	})
}

// TestEncodePCM_ConcurrentSamePathNoCorruption reproduces the WAV
// counterpart of GitHub #3323: when several detections resolve to the same clip
// path, the WAV writer must not let concurrent saves interleave into one file.
// Each worker writes a worker-distinct constant sample value, so a clean result
// (one writer's complete WAV, finalized atomically) is uniform, while an
// interleaved write mixes values from different writers. The save must use a
// per-write unique temp file and an atomic rename, like the FFmpeg/FLAC paths.
func TestEncodePCM_ConcurrentSamePathNoCorruption(t *testing.T) {
	t.Parallel()
	const (
		workers = 32
		samples = 96000 // 2 s mono 16-bit
	)
	dir := t.TempDir()
	filePath := filepath.Join(dir, "columba_palumbus_95p_20260531T083828Z.wav")

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, workers)
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pcm := make([]byte, samples*2)
			for j := range samples {
				binary.LittleEndian.PutUint16(pcm[j*2:], uint16(1000+i)) //nolint:gosec // G115: small positive test constant
			}
			<-start // release all workers together to maximise collision
			errs[i] = savePCM(t, filePath, pcm, 48000, 16)
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		require.NoErrorf(t, err, "concurrent WAV save %d must not fail", i)
	}

	// The surviving clip must be exactly one worker's complete, uncorrupted WAV:
	// a valid header whose data size matches a full write, and a payload of a
	// single repeated value (not an interleaved mix of different workers).
	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(data), 44, "valid WAV header expected")
	dataSize := binary.LittleEndian.Uint32(data[40:44])
	require.Equal(t, uint32(samples*2), dataSize, "data chunk size must match one complete write")
	require.GreaterOrEqual(t, len(data), 44+samples*2, "file must hold the full payload")

	payload := data[44 : 44+samples*2]
	first := binary.LittleEndian.Uint16(payload[0:2])
	for off := 2; off < len(payload); off += 2 {
		v := binary.LittleEndian.Uint16(payload[off:])
		require.Equalf(t, first, v, "sample at byte offset %d differs (%d vs %d): interleaved/corrupt WAV", off, v, first)
	}
}

// makePCM16Bytes generates a mono 16-bit PCM byte slice from int16 samples. It
// delegates to makePCMBytes so there is a single little-endian PCM encoder in
// the tests.
func makePCM16Bytes(t *testing.T, samples []int16) []byte {
	t.Helper()
	widened := make([]int32, len(samples))
	for i, s := range samples {
		widened[i] = int32(s)
	}
	return makePCMBytes(t, 16, widened)
}

// TestEncodePCM verifies that known PCM data is written as a valid WAV file
// with correct RIFF/fmt/data chunk structure and matching audio parameters.
func TestEncodePCM(t *testing.T) {
	t.Parallel()

	t.Run("valid 48kHz mono 16-bit WAV", func(t *testing.T) {
		t.Parallel()

		// 10 samples of silence at 48kHz/16-bit mono
		samples := make([]int16, 10)
		pcmData := makePCM16Bytes(t, samples)

		dir := t.TempDir()
		filePath := filepath.Join(dir, "out.wav")

		err := savePCM(t, filePath, pcmData, 48000, 16)
		require.NoError(t, err)

		data, err := os.ReadFile(filePath)
		require.NoError(t, err)

		// Minimum WAV header is 44 bytes
		require.GreaterOrEqual(t, len(data), 44, "WAV file must have at least a 44-byte header")

		// RIFF magic
		assert.Equal(t, []byte("RIFF"), data[0:4], "expected RIFF magic")

		// WAVE magic
		assert.Equal(t, []byte("WAVE"), data[8:12], "expected WAVE format")

		// fmt chunk marker
		assert.Equal(t, []byte("fmt "), data[12:16], "expected fmt chunk")

		// fmt chunk size should be 16 for PCM
		fmtSize := binary.LittleEndian.Uint32(data[16:20])
		assert.Equal(t, uint32(16), fmtSize, "PCM fmt chunk size should be 16")

		// Audio format: 1 = PCM
		audioFmt := binary.LittleEndian.Uint16(data[20:22])
		assert.Equal(t, uint16(1), audioFmt, "audio format should be PCM (1)")

		// Channels: 1 = mono
		numChannels := binary.LittleEndian.Uint16(data[22:24])
		assert.Equal(t, uint16(1), numChannels, "should be mono (1 channel)")

		// Sample rate: 48000
		sampleRate := binary.LittleEndian.Uint32(data[24:28])
		assert.Equal(t, uint32(48000), sampleRate, "sample rate should be 48000 Hz")

		// Bit depth: 16
		bitsPerSample := binary.LittleEndian.Uint16(data[34:36])
		assert.Equal(t, uint16(16), bitsPerSample, "bits per sample should be 16")

		// data chunk marker
		assert.Equal(t, []byte("data"), data[36:40], "expected data chunk")

		// data chunk size should equal pcmData length
		dataSize := binary.LittleEndian.Uint32(data[40:44])
		assert.Equal(t, uint32(len(pcmData)), dataSize, "data chunk size should match PCM data length")
	})

	t.Run("empty file path returns error", func(t *testing.T) {
		t.Parallel()
		pcmData := makePCM16Bytes(t, []int16{0, 1, 2})
		err := savePCM(t, "", pcmData, 48000, 16)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty file path")
	})

	t.Run("empty PCM data returns error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		err := savePCM(t, filepath.Join(dir, "out.wav"), []byte{}, 48000, 16)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty PCM data")
	})

	t.Run("misaligned PCM data returns error", func(t *testing.T) {
		t.Parallel()
		// Each byte count is not a whole number of samples at its bit depth.
		cases := []struct {
			bitDepth int
			nbytes   int
		}{
			{16, 3}, // not a multiple of 2
			{24, 4}, // not a multiple of 3
			{32, 5}, // not a multiple of 4
		}
		for _, c := range cases {
			dir := t.TempDir()
			err := savePCM(t, filepath.Join(dir, "out.wav"), make([]byte, c.nbytes), 48000, c.bitDepth)
			require.Errorf(t, err, "%d-bit %d-byte input must be rejected", c.bitDepth, c.nbytes)
			assert.Contains(t, err.Error(), "not aligned")
		}
	})

	t.Run("non-positive sample rate returns error, not a panic", func(t *testing.T) {
		t.Parallel()
		for _, sr := range []int{0, -1} {
			dir := t.TempDir()
			err := savePCM(t, filepath.Join(dir, "out.wav"), makePCM16Bytes(t, []int16{0, 1}), sr, 16)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "sample rate")
		}
	})

	t.Run("creates parent directories", func(t *testing.T) {
		t.Parallel()
		pcmData := makePCM16Bytes(t, []int16{100, -100, 200})
		dir := t.TempDir()
		filePath := filepath.Join(dir, "nested", "deep", "out.wav")

		err := savePCM(t, filePath, pcmData, 48000, 16)
		require.NoError(t, err)

		_, statErr := os.Stat(filePath)
		assert.NoError(t, statErr, "file should exist after creation")
	})

	t.Run("44100 Hz sample rate is encoded correctly", func(t *testing.T) {
		t.Parallel()
		pcmData := makePCM16Bytes(t, []int16{0, 1, 2, 3})
		dir := t.TempDir()
		filePath := filepath.Join(dir, "out44.wav")

		err := savePCM(t, filePath, pcmData, 44100, 16)
		require.NoError(t, err)

		data, err := os.ReadFile(filePath)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(data), 44)

		sampleRate := binary.LittleEndian.Uint32(data[24:28])
		assert.Equal(t, uint32(44100), sampleRate, "sample rate should be 44100 Hz")
	})
}

// makePCMBytes builds a mono little-endian signed integer PCM byte slice of the
// given bit depth from the provided sample values.
func makePCMBytes(t *testing.T, bitDepth int, samples []int32) []byte {
	t.Helper()
	bytesPerSample := bitDepth / 8
	buf := make([]byte, len(samples)*bytesPerSample)
	for i, s := range samples {
		u := uint32(s) //nolint:gosec // G115: intentional signed→unsigned bit reinterpretation for PCM audio
		off := i * bytesPerSample
		for b := range bytesPerSample {
			buf[off+b] = byte(u >> (8 * b)) // little-endian
		}
	}
	return buf
}

// TestEncodePCM_HigherBitDepths verifies that 24- and 32-bit mono PCM is
// written and reads back byte-for-byte through the go-wav decoder. These depths
// use WAVE_FORMAT_EXTENSIBLE, so the round-trip is verified via the decoder
// rather than fixed canonical header offsets.
func TestEncodePCM_HigherBitDepths(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		bitDepth int
		samples  []int32
	}{
		{"24-bit mono", 24, []int32{0, 1, -1, 8388607, -8388608, 12345, -12345}},
		{"32-bit mono", 32, []int32{0, 1, -1, 2147483647, -2147483648, 999999, -999999}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pcmData := makePCMBytes(t, tc.bitDepth, tc.samples)

			dir := t.TempDir()
			filePath := filepath.Join(dir, "out.wav")
			require.NoError(t, savePCM(t, filePath, pcmData, 48000, tc.bitDepth))

			data, err := os.ReadFile(filePath)
			require.NoError(t, err)

			// Assert the format tag independently of the go-wav decoder: 24/32-bit
			// integer PCM must be written as WAVE_FORMAT_EXTENSIBLE (0xFFFE). This
			// catches an encoder-side header bug that a symmetric decoder bug would
			// otherwise round-trip clean past the assertions below.
			require.GreaterOrEqual(t, len(data), 22, "file must hold at least the fmt tag")
			audioFmt := binary.LittleEndian.Uint16(data[20:22])
			assert.Equal(t, uint16(0xFFFE), audioFmt, "24/32-bit PCM should use WAVE_FORMAT_EXTENSIBLE")

			decoded, info, err := wavpcm.DecodeInterleavedBytes(data)
			require.NoError(t, err)
			assert.Equal(t, 48000, info.SampleRate, "sample rate round-trips")
			assert.Equal(t, tc.bitDepth, info.BitDepth, "bit depth round-trips")
			assert.Equal(t, 1, info.Channels, "channel count round-trips")
			assert.Equal(t, pcmData, decoded, "PCM payload round-trips byte-for-byte")
		})
	}
}

// TestEncodePCM_UnsupportedBitDepth verifies that depths outside the
// supported {16, 24, 32} integer set are rejected before any file is created.
func TestEncodePCM_UnsupportedBitDepth(t *testing.T) {
	t.Parallel()
	for _, bd := range []int{8, 12, 64} {
		dir := t.TempDir()
		filePath := filepath.Join(dir, "out.wav")
		// One frame's worth of bytes, so alignment is not the reason it fails.
		pcm := make([]byte, bd/8)
		err := savePCM(t, filePath, pcm, 48000, bd)
		require.Errorf(t, err, "bit depth %d must be rejected", bd)
		assert.Contains(t, err.Error(), "unsupported bit depth")
		_, statErr := os.Stat(filePath)
		assert.Truef(t, os.IsNotExist(statErr), "no file should be created for unsupported depth %d", bd)
	}
}

// gainTestSamples is a ramp that stays inside int16 after a +6 dB gain.
func gainTestSamples() []int16 {
	samples := make([]int16, 1000)
	for i := range samples {
		samples[i] = int16((i - 500) * 20) //nolint:gosec // G115: bounded to +-10000 by construction
	}
	return samples
}

// TestEncodePCM_AppliesGain verifies that GainDB scales the written samples:
// +6 dB is a factor of 1.9953, so every sample lands within 1 LSB of its double.
func TestEncodePCM_AppliesGain(t *testing.T) {
	t.Parallel()
	const gainDB = 6.0
	samples := gainTestSamples()
	filePath := filepath.Join(t.TempDir(), "gain.wav")

	require.NoError(t, wav.EncodePCM(t.Context(), &wav.Options{
		PCMData:    makePCM16Bytes(t, samples),
		OutputPath: filePath,
		SampleRate: 48000,
		BitDepth:   16,
		GainDB:     gainDB,
	}))

	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	decoded, _, err := wavpcm.DecodeInterleavedBytes(data)
	require.NoError(t, err)
	require.Len(t, decoded, len(samples)*2)

	factor := math.Pow(10, gainDB/20)
	for i, in := range samples {
		got := int16(binary.LittleEndian.Uint16(decoded[i*2:])) //nolint:gosec // G115: intentional uint16->int16 reinterpretation of PCM
		assert.InDeltaf(t, float64(in)*factor, float64(got), 1.0, "sample %d", i)
	}
}

// TestEncodePCM_LeavesInputUnchanged verifies that applying gain works on a
// copy: the caller's PCMData is read again after the export by the spectrogram
// pre-render job.
func TestEncodePCM_LeavesInputUnchanged(t *testing.T) {
	t.Parallel()
	pcm := makePCM16Bytes(t, gainTestSamples())
	want := bytes.Clone(pcm)

	require.NoError(t, wav.EncodePCM(t.Context(), &wav.Options{
		PCMData:    pcm,
		OutputPath: filepath.Join(t.TempDir(), "gain.wav"),
		SampleRate: 48000,
		BitDepth:   16,
		GainDB:     6,
	}))

	assert.Equal(t, want, pcm)
}

// TestEncodePCM_GainRequiresSixteenBit verifies that a non-zero gain on 24-bit
// PCM is rejected instead of being applied to the wrong sample layout.
func TestEncodePCM_GainRequiresSixteenBit(t *testing.T) {
	t.Parallel()
	filePath := filepath.Join(t.TempDir(), "gain.wav")

	err := wav.EncodePCM(t.Context(), &wav.Options{
		PCMData:    make([]byte, 6),
		OutputPath: filePath,
		SampleRate: 48000,
		BitDepth:   24,
		GainDB:     6,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "gain is not supported")
	_, statErr := os.Stat(filePath)
	assert.True(t, os.IsNotExist(statErr), "no file should be created")
}

// TestEncodePCM_NilOptions verifies the nil guard.
func TestEncodePCM_NilOptions(t *testing.T) {
	t.Parallel()
	require.Error(t, wav.EncodePCM(t.Context(), nil))
}

// TestEncodePCM_EncoderValidationErrorIsAudioCategory verifies that an error the
// go-wav encoder raises for the stream itself (here a sample rate above what the
// format can carry) is not filed as a filesystem fault, and that no clip is
// left behind.
func TestEncodePCM_EncoderValidationErrorIsAudioCategory(t *testing.T) {
	t.Parallel()
	if bits.UintSize == 32 {
		t.Skip("a sample rate above the RIFF ceiling is not representable in a 32-bit int")
	}
	filePath := filepath.Join(t.TempDir(), "rate.wav")
	sampleRate := math.MaxInt32
	sampleRate++

	err := wav.EncodePCM(t.Context(), &wav.Options{
		PCMData:    make([]byte, 4),
		OutputPath: filePath,
		SampleRate: sampleRate,
		BitDepth:   16,
	})

	require.Error(t, err)
	var enhanced *errors.EnhancedError
	require.ErrorAs(t, err, &enhanced)
	assert.Equal(t, string(errors.CategoryAudio), enhanced.GetCategory())
	_, statErr := os.Stat(filePath)
	assert.True(t, os.IsNotExist(statErr), "no clip is left behind")
}
