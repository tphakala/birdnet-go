package convert_test

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/convert"
)

// TestSavePCMDataToWAV_ConcurrentSamePathNoCorruption reproduces the WAV
// counterpart of GitHub #3323: when several detections resolve to the same clip
// path, the WAV writer must not let concurrent saves interleave into one file.
// Each worker writes a worker-distinct constant sample value, so a clean result
// (one writer's complete WAV, finalized atomically) is uniform, while an
// interleaved write mixes values from different writers. The save must use a
// per-write unique temp file and an atomic rename, like the FFmpeg/FLAC paths.
func TestSavePCMDataToWAV_ConcurrentSamePathNoCorruption(t *testing.T) {
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
			errs[i] = convert.SavePCMDataToWAV(filePath, pcm, 48000, 16)
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

// makePCM16Bytes generates a simple mono 16-bit PCM byte slice from int16 samples.
func makePCM16Bytes(t *testing.T, samples []int16) []byte {
	t.Helper()
	buf := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(s)) //nolint:gosec // G115: intentional int16→uint16 bit reinterpretation for PCM audio
	}
	return buf
}

// TestSavePCMDataToWAV verifies that known PCM data is written as a valid WAV file
// with correct RIFF/fmt/data chunk structure and matching audio parameters.
func TestSavePCMDataToWAV(t *testing.T) {
	t.Parallel()

	t.Run("valid 48kHz mono 16-bit WAV", func(t *testing.T) {
		t.Parallel()

		// 10 samples of silence at 48kHz/16-bit mono
		samples := make([]int16, 10)
		pcmData := makePCM16Bytes(t, samples)

		dir := t.TempDir()
		filePath := filepath.Join(dir, "out.wav")

		err := convert.SavePCMDataToWAV(filePath, pcmData, 48000, 16)
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
		err := convert.SavePCMDataToWAV("", pcmData, 48000, 16)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty file path")
	})

	t.Run("empty PCM data returns error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		err := convert.SavePCMDataToWAV(filepath.Join(dir, "out.wav"), []byte{}, 48000, 16)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty PCM data")
	})

	t.Run("misaligned PCM data returns error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		// 3 bytes is not divisible by 2 (16-bit samples = 2 bytes each)
		err := convert.SavePCMDataToWAV(filepath.Join(dir, "out.wav"), []byte{0x01, 0x02, 0x03}, 48000, 16)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not aligned")
	})

	t.Run("non-positive sample rate returns error, not a panic", func(t *testing.T) {
		t.Parallel()
		for _, sr := range []int{0, -1} {
			dir := t.TempDir()
			err := convert.SavePCMDataToWAV(filepath.Join(dir, "out.wav"), makePCM16Bytes(t, []int16{0, 1}), sr, 16)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "sample rate")
		}
	})

	t.Run("creates parent directories", func(t *testing.T) {
		t.Parallel()
		pcmData := makePCM16Bytes(t, []int16{100, -100, 200})
		dir := t.TempDir()
		filePath := filepath.Join(dir, "nested", "deep", "out.wav")

		err := convert.SavePCMDataToWAV(filePath, pcmData, 48000, 16)
		require.NoError(t, err)

		_, statErr := os.Stat(filePath)
		assert.NoError(t, statErr, "file should exist after creation")
	})

	t.Run("44100 Hz sample rate is encoded correctly", func(t *testing.T) {
		t.Parallel()
		pcmData := makePCM16Bytes(t, []int16{0, 1, 2, 3})
		dir := t.TempDir()
		filePath := filepath.Join(dir, "out44.wav")

		err := convert.SavePCMDataToWAV(filePath, pcmData, 44100, 16)
		require.NoError(t, err)

		data, err := os.ReadFile(filePath)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(data), 44)

		sampleRate := binary.LittleEndian.Uint32(data[24:28])
		assert.Equal(t, uint32(44100), sampleRate, "sample rate should be 44100 Hz")
	})
}
