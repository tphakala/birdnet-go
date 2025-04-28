package birdweather

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

func TestEncodePCMtoWAV_EmptyInput(t *testing.T) {
	// Test with empty PCM data
	emptyData := []byte{}
	ctx := context.Background()
	_, err := myaudio.EncodePCMtoWAVWithContext(ctx, emptyData)

	if err == nil {
		t.Error("EncodePCMtoWAVWithContext should return an error with empty PCM data")
	}

	if err != nil && err.Error() != "PCM data is empty" {
		t.Errorf("Expected error message 'PCM data is empty', got: %v", err)
	}
}

func TestEncodePCMtoWAV_ValidInput(t *testing.T) {
	// Create valid PCM data (simple sine wave or just zeros)
	sampleCount := 48000                   // 1 second of audio at 48kHz
	pcmData := make([]byte, sampleCount*2) // 16-bit samples = 2 bytes per sample

	// Fill with some non-zero values (could be a simple pattern)
	for i := 0; i < sampleCount; i++ {
		// Write a simple sawtooth pattern
		value := uint16(i % 32768) // 16-bit value range
		binary.LittleEndian.PutUint16(pcmData[i*2:], value)
	}

	// Encode to WAV
	ctx := context.Background()
	wavBuffer, err := myaudio.EncodePCMtoWAVWithContext(ctx, pcmData)

	// Check for errors
	if err != nil {
		t.Errorf("EncodePCMtoWAVWithContext failed with valid input: %v", err)
		return
	}

	// Basic validation of WAV header
	if wavBuffer == nil {
		t.Fatal("EncodePCMtoWAVWithContext returned nil buffer")
	}

	// Extract header components
	header := make([]byte, 44) // Standard WAV header size
	_, err = io.ReadFull(wavBuffer, header)
	if err != nil {
		t.Fatalf("Failed to read WAV header: %v", err)
	}

	// Reset buffer position
	wavBuffer.Reset()
	io.ReadFull(wavBuffer, header)

	// Check RIFF header
	if string(header[0:4]) != "RIFF" {
		t.Errorf("WAV header missing RIFF marker, got: %s", string(header[0:4]))
	}

	// Check WAVE format
	if string(header[8:12]) != "WAVE" {
		t.Errorf("WAV header missing WAVE format, got: %s", string(header[8:12]))
	}

	// Check fmt chunk
	if string(header[12:16]) != "fmt " {
		t.Errorf("WAV header missing fmt chunk, got: %s", string(header[12:16]))
	}

	// Check PCM format (should be 1)
	format := binary.LittleEndian.Uint16(header[20:22])
	if format != 1 {
		t.Errorf("WAV format should be 1 (PCM), got: %d", format)
	}

	// Check channels (should be 1 - mono)
	channels := binary.LittleEndian.Uint16(header[22:24])
	if channels != 1 {
		t.Errorf("WAV channels should be 1 (mono), got: %d", channels)
	}

	// Check sample rate (should be 48000)
	sampleRate := binary.LittleEndian.Uint32(header[24:28])
	if sampleRate != 48000 {
		t.Errorf("WAV sample rate should be 48000, got: %d", sampleRate)
	}

	// Check bit depth (should be 16)
	bitDepth := binary.LittleEndian.Uint16(header[34:36])
	if bitDepth != 16 {
		t.Errorf("WAV bit depth should be 16, got: %d", bitDepth)
	}

	// Check data chunk
	if string(header[36:40]) != "data" {
		t.Errorf("WAV header missing data chunk, got: %s", string(header[36:40]))
	}

	// Check data size (should match input size)
	dataSize := binary.LittleEndian.Uint32(header[40:44])
	if int(dataSize) != len(pcmData) {
		t.Errorf("WAV data size should be %d, got: %d", len(pcmData), dataSize)
	}
}

func TestEncodePCMtoWAV_SmallInput(t *testing.T) {
	// Test with very small PCM data (smaller than WAV header)
	smallData := []byte{0x01, 0x02, 0x03, 0x04} // Just 4 bytes

	ctx := context.Background()
	wavBuffer, err := myaudio.EncodePCMtoWAVWithContext(ctx, smallData)

	if err != nil {
		t.Errorf("EncodePCMtoWAVWithContext failed with small input: %v", err)
		return
	}

	// The WAV file should still be valid, just with a very small data chunk
	wavData, err := io.ReadAll(wavBuffer)
	if err != nil {
		t.Fatalf("Failed to read WAV data: %v", err)
	}

	// Expected size: 44 byte header + 4 bytes of data
	expectedSize := 44 + 4
	if len(wavData) != expectedSize {
		t.Errorf("Expected WAV file size to be %d bytes, got %d bytes", expectedSize, len(wavData))
	}
}

func TestEncodePCMtoWAV_RecreateOriginalPCM(t *testing.T) {
	// Create test PCM data with a known pattern
	sampleCount := 1000
	pcmData := make([]byte, sampleCount*2)

	// Fill with an easily recognizable pattern
	for i := 0; i < sampleCount; i++ {
		value := uint16(i % 256)
		binary.LittleEndian.PutUint16(pcmData[i*2:], value)
	}

	// Encode to WAV
	ctx := context.Background()
	wavBuffer, err := myaudio.EncodePCMtoWAVWithContext(ctx, pcmData)
	if err != nil {
		t.Fatalf("EncodePCMtoWAVWithContext failed: %v", err)
	}

	// Read the WAV file data
	wavData, err := io.ReadAll(wavBuffer)
	if err != nil {
		t.Fatalf("Failed to read WAV data: %v", err)
	}

	// Extract just the PCM portion (skip 44 byte header)
	extractedPCM := wavData[44:]

	// Verify the extracted PCM matches the original
	if !bytes.Equal(extractedPCM, pcmData) {
		t.Error("Extracted PCM data does not match the original PCM data")

		// Find the first mismatch for better diagnostics
		for i := 0; i < len(pcmData) && i < len(extractedPCM); i++ {
			if pcmData[i] != extractedPCM[i] {
				t.Errorf("First mismatch at byte %d: original=0x%02x, extracted=0x%02x",
					i, pcmData[i], extractedPCM[i])
				break
			}
		}
	}
}

func TestEncodePCMtoWAV_LargeInput(t *testing.T) {
	// Test with a larger PCM data (simulate 5 seconds of audio)
	sampleRate := 48000
	seconds := 5
	sampleCount := sampleRate * seconds
	largeData := make([]byte, sampleCount*2) // 16-bit samples

	// Fill with some pattern
	for i := 0; i < sampleCount; i++ {
		value := uint16(i % 32768)
		binary.LittleEndian.PutUint16(largeData[i*2:], value)
	}

	ctx := context.Background()
	wavBuffer, err := myaudio.EncodePCMtoWAVWithContext(ctx, largeData)
	if err != nil {
		t.Errorf("EncodePCMtoWAVWithContext failed with large input: %v", err)
		return
	}

	// Check that the returned buffer size is correct (header + data)
	wavData, err := io.ReadAll(wavBuffer)
	if err != nil {
		t.Fatalf("Failed to read WAV data: %v", err)
	}

	expectedSize := 44 + len(largeData) // 44 byte header + PCM data
	if len(wavData) != expectedSize {
		t.Errorf("Expected WAV size to be %d bytes, got %d bytes", expectedSize, len(wavData))
	}
}

func TestContextTimeout(t *testing.T) {
	// Create a large PCM data buffer
	sampleCount := 48000 * 10                // 10 seconds of audio
	largeData := make([]byte, sampleCount*2) // 16-bit samples

	// Create a context with a very short timeout (should trigger timeout)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Let the context timeout before we call the function
	time.Sleep(5 * time.Millisecond)

	// This should fail due to context cancellation
	_, err := myaudio.EncodePCMtoWAVWithContext(ctx, largeData)
	if err == nil {
		t.Error("Expected context timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected context.DeadlineExceeded error, got: %v", err)
	}
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
	for i := 0; i < sampleCount; i++ {
		// Calculate sine wave value (-32767 to 32767)
		value := int16(32767.0 * math.Sin(2.0*math.Pi*440.0*float64(i)/48000.0))
		// Convert to bytes and store in PCM data
		binary.LittleEndian.PutUint16(pcmData[i*2:], uint16(value))
	}

	// Encode PCM to FLAC with normalization
	flacBuffer, err := encodeFlacUsingFFmpeg(pcmData, settings)
	if err != nil {
		t.Errorf("encodeFlacUsingFFmpeg failed with valid input: %v", err)
		return
	}

	// Basic validation
	if flacBuffer == nil {
		t.Fatal("encodeFlacUsingFFmpeg returned nil buffer")
	}

	// Validate FLAC header (just check signature bytes)
	flacBytes := flacBuffer.Bytes()
	if len(flacBytes) < 4 {
		t.Fatal("FLAC buffer too small, need at least 4 bytes")
	}

	// Check FLAC signature (should start with "fLaC")
	if string(flacBytes[0:4]) != "fLaC" {
		t.Errorf("FLAC signature not found, got: %v", flacBytes[0:4])
	}

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
