package convert_test

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/convert"
)

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
