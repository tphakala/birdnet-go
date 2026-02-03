package birdweather

import (
	"context"
	"encoding/binary"
	"io"
	"math"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

func TestEncodePCMtoWAV_EmptyInput(t *testing.T) {
	// Test with empty PCM data
	emptyData := []byte{}
	ctx := t.Context()
	_, err := myaudio.EncodePCMtoWAVWithContext(ctx, emptyData)

	require.Error(t, err, "EncodePCMtoWAVWithContext should return an error with empty PCM data")
	assert.Equal(t, "PCM data is empty for WAV encoding", err.Error())
}

func TestEncodePCMtoWAV_ValidInput(t *testing.T) {
	// Create valid PCM data (simple sine wave or just zeros)
	sampleCount := 48000                   // 1 second of audio at 48kHz
	pcmData := make([]byte, sampleCount*2) // 16-bit samples = 2 bytes per sample

	// Fill with some non-zero values (could be a simple pattern)
	for i := range sampleCount {
		// Write a simple sawtooth pattern
		value := uint16(i % 32768) //nolint:gosec // G115: test data bounded by 16-bit range
		binary.LittleEndian.PutUint16(pcmData[i*2:], value)
	}

	// Encode to WAV
	ctx := t.Context()
	wavBuffer, err := myaudio.EncodePCMtoWAVWithContext(ctx, pcmData)

	// Check for errors
	require.NoError(t, err, "EncodePCMtoWAVWithContext failed with valid input")
	require.NotNil(t, wavBuffer, "EncodePCMtoWAVWithContext returned nil buffer")

	// Extract header components
	header := make([]byte, 44) // Standard WAV header size

	// Get all data from buffer to avoid issues with reading twice
	allData := wavBuffer.Bytes()
	require.GreaterOrEqual(t, len(allData), 44, "WAV buffer too small for header")

	// Copy header from the beginning of the data
	copy(header, allData[:44])

	// Check RIFF header
	assert.Equal(t, "RIFF", string(header[0:4]), "WAV header missing RIFF marker")

	// Check WAVE format
	assert.Equal(t, "WAVE", string(header[8:12]), "WAV header missing WAVE format")

	// Check fmt chunk
	assert.Equal(t, "fmt ", string(header[12:16]), "WAV header missing fmt chunk")

	// Check PCM format (should be 1)
	format := binary.LittleEndian.Uint16(header[20:22])
	assert.Equal(t, uint16(1), format, "WAV format should be 1 (PCM)")

	// Check channels (should be 1 - mono)
	channels := binary.LittleEndian.Uint16(header[22:24])
	assert.Equal(t, uint16(1), channels, "WAV channels should be 1 (mono)")

	// Check sample rate (should be 48000)
	sampleRate := binary.LittleEndian.Uint32(header[24:28])
	assert.Equal(t, uint32(48000), sampleRate, "WAV sample rate should be 48000")

	// Check bit depth (should be 16)
	bitDepth := binary.LittleEndian.Uint16(header[34:36])
	assert.Equal(t, uint16(16), bitDepth, "WAV bit depth should be 16")

	// Check data chunk
	assert.Equal(t, "data", string(header[36:40]), "WAV header missing data chunk")

	// Check data size (should match input size)
	dataSize := binary.LittleEndian.Uint32(header[40:44])
	assert.Equal(t, len(pcmData), int(dataSize), "WAV data size mismatch")
}

func TestEncodePCMtoWAV_SmallInput(t *testing.T) {
	// Test with very small PCM data (smaller than WAV header)
	smallData := []byte{0x01, 0x02, 0x03, 0x04} // Just 4 bytes

	ctx := t.Context()
	wavBuffer, err := myaudio.EncodePCMtoWAVWithContext(ctx, smallData)

	require.NoError(t, err, "EncodePCMtoWAVWithContext failed with small input")

	// The WAV file should still be valid, just with a very small data chunk
	wavData, err := io.ReadAll(wavBuffer)
	require.NoError(t, err, "Failed to read WAV data")

	// Expected size: 44 byte header + 4 bytes of data
	expectedSize := 44 + 4
	assert.Len(t, wavData, expectedSize, "WAV file size mismatch")
}

func TestEncodePCMtoWAV_RecreateOriginalPCM(t *testing.T) {
	// Create test PCM data with a known pattern
	sampleCount := 1000
	pcmData := make([]byte, sampleCount*2)

	// Fill with an easily recognizable pattern
	for i := range sampleCount {
		value := uint16(i % 256) //nolint:gosec // G115: test data bounded by 8-bit range
		binary.LittleEndian.PutUint16(pcmData[i*2:], value)
	}

	// Encode to WAV
	ctx := t.Context()
	wavBuffer, err := myaudio.EncodePCMtoWAVWithContext(ctx, pcmData)
	require.NoError(t, err, "EncodePCMtoWAVWithContext failed")

	// Read the WAV file data
	wavData, err := io.ReadAll(wavBuffer)
	require.NoError(t, err, "Failed to read WAV data")

	// Extract just the PCM portion (skip 44 byte header)
	extractedPCM := wavData[44:]

	// Verify the extracted PCM matches the original
	assert.Equal(t, pcmData, extractedPCM, "Extracted PCM data does not match the original PCM data")
}

func TestEncodePCMtoWAV_LargeInput(t *testing.T) {
	// Test with a larger PCM data (simulate 5 seconds of audio)
	sampleRate := 48000
	seconds := 5
	sampleCount := sampleRate * seconds
	largeData := make([]byte, sampleCount*2) // 16-bit samples

	// Fill with some pattern
	for i := range sampleCount {
		value := uint16(i % 32768) //nolint:gosec // G115: test data bounded by 16-bit range
		binary.LittleEndian.PutUint16(largeData[i*2:], value)
	}

	ctx := t.Context()
	wavBuffer, err := myaudio.EncodePCMtoWAVWithContext(ctx, largeData)
	require.NoError(t, err, "EncodePCMtoWAVWithContext failed with large input")

	// Check that the returned buffer size is correct (header + data)
	wavData, err := io.ReadAll(wavBuffer)
	require.NoError(t, err, "Failed to read WAV data")

	expectedSize := 44 + len(largeData) // 44 byte header + PCM data
	assert.Len(t, wavData, expectedSize, "WAV size mismatch")
}

func TestContextTimeout(t *testing.T) {
	// Create a large PCM data buffer
	sampleCount := 48000 * 10                // 10 seconds of audio
	largeData := make([]byte, sampleCount*2) // 16-bit samples

	// Create a context with a very short timeout (should trigger timeout)
	ctx, cancel := context.WithTimeout(t.Context(), 1*time.Nanosecond)
	defer cancel()

	// Let the context timeout before we call the function
	time.Sleep(5 * time.Millisecond)

	// This should fail due to context cancellation
	_, err := myaudio.EncodePCMtoWAVWithContext(ctx, largeData)
	require.Error(t, err, "Expected context timeout error")
	assert.ErrorIs(t, err, context.DeadlineExceeded, "Expected context.DeadlineExceeded error")
}

func TestEncodeFlacUsingFFmpeg(t *testing.T) {
	// Skip the test if FFmpeg is not available
	if !conf.IsFfmpegAvailable() {
		t.Skip("FFmpeg not available, skipping FLAC encoding test")
	}

	// Create a settings object with ffmpeg path
	settings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				FfmpegPath: getFFmpegPath(),
			},
			Birdweather: conf.BirdweatherSettings{
				Debug: true, // Enable debug for testing
			},
		},
	}

	// Create test PCM data (1 second of audio at 48kHz, 16-bit mono)
	// Using a simple sine wave pattern for better normalization testing
	sampleCount := 48000 // 1 second at 48kHz
	pcmData := make([]byte, sampleCount*2)

	// Generate a sine wave at 440Hz (A4 note)
	for i := range sampleCount {
		// Calculate sine wave value (-32767 to 32767)
		value := int16(32767.0 * math.Sin(2.0*math.Pi*440.0*float64(i)/48000.0))
		// Convert to bytes and store in PCM data
		binary.LittleEndian.PutUint16(pcmData[i*2:], uint16(value)) //nolint:gosec // G115: audio sample conversion within 16-bit range
	}

	// Determine the ffmpeg path for the test
	ffmpegPathForTest := getFFmpegPath()

	// Encode PCM to FLAC with normalization
	// Pass a background context since this test doesn't need timeout control itself
	ctx := t.Context()
	flacBuffer, err := encodeFlacUsingFFmpeg(ctx, pcmData, ffmpegPathForTest, settings)
	require.NoError(t, err, "encodeFlacUsingFFmpeg failed with valid input")
	require.NotNil(t, flacBuffer, "encodeFlacUsingFFmpeg returned nil buffer")

	// Validate FLAC header (just check signature bytes)
	flacBytes := flacBuffer.Bytes()
	require.GreaterOrEqual(t, len(flacBytes), 4, "FLAC buffer too small, need at least 4 bytes")

	// Check FLAC signature (should start with "fLaC")
	assert.Equal(t, "fLaC", string(flacBytes[0:4]), "FLAC signature not found")

	// The FLAC data should be smaller than the raw PCM (compression)
	if flacBuffer.Len() >= len(pcmData) {
		t.Logf("Warning: FLAC data (%d bytes) is not smaller than PCM data (%d bytes)",
			flacBuffer.Len(), len(pcmData))
	}

	t.Logf("Successfully encoded PCM to normalized FLAC, size: %d bytes", flacBuffer.Len())
}

func getFFmpegPath() string {
	// Try to get FFmpeg path from environment variable first
	path := os.Getenv("FFMPEG_PATH")
	if path != "" {
		return path
	}

	// Fall back to a common system location on Linux/macOS
	if _, err := os.Stat("/usr/bin/ffmpeg"); err == nil {
		return "/usr/bin/ffmpeg"
	}

	// Fall back to just the binary name, assuming it's in PATH
	return "ffmpeg"
}
