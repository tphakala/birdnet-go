// test_helpers_test.go - Shared test helpers for myaudio package
// These helpers reduce duplication across test files and ensure consistent test setup.
package myaudio

import (
	"io"
	"os"
	"os/exec"
	"testing"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// --- Logger Helpers ---

// getTestLogger returns a logger for testing.
func getTestLogger() logger.Logger {
	// Use console logger with debug level for tests, output to io.Discard to keep tests quiet
	return logger.NewSlogLogger(io.Discard, logger.LogLevelDebug, nil)
}

// --- Sox Helpers ---

// isSoxAvailable checks if sox is available in PATH.
// Use this to skip tests that require sox.
func isSoxAvailable() bool {
	_, err := exec.LookPath("sox")
	return err == nil
}

// skipIfNoSox skips the test if sox is not available.
func skipIfNoSox(t *testing.T) {
	t.Helper()
	if !isSoxAvailable() {
		t.Skip("sox not available, skipping test")
	}
}

// --- WAV File Helpers ---

// createTestWAVFile creates a test WAV file with specified duration.
// The file contains silence at 44100Hz, 16-bit, mono.
func createTestWAVFile(path string, durationSec float64) error {
	sampleRate := 44100
	numSamples := int(float64(sampleRate) * durationSec)
	dataSize := numSamples * 2 // 16-bit = 2 bytes per sample

	file, err := os.Create(path) //nolint:gosec // G304: test fixture path
	if err != nil {
		return err
	}
	defer func() {
		if err := file.Close(); err != nil {
			// Log error but don't fail - this is a test helper
			_ = err
		}
	}()

	// Write WAV header
	header := []byte{
		'R', 'I', 'F', 'F',
		byte(dataSize + 36), byte((dataSize + 36) >> 8), byte((dataSize + 36) >> 16), byte((dataSize + 36) >> 24),
		'W', 'A', 'V', 'E',
		'f', 'm', 't', ' ',
		16, 0, 0, 0, // Subchunk1Size
		1, 0, // AudioFormat (PCM)
		1, 0, // NumChannels (mono)
		byte(sampleRate), byte(sampleRate >> 8), byte(sampleRate >> 16), byte(sampleRate >> 24), // SampleRate
		byte(sampleRate * 2), byte((sampleRate * 2) >> 8), byte((sampleRate * 2) >> 16), byte((sampleRate * 2) >> 24), // ByteRate
		2, 0, // BlockAlign
		16, 0, // BitsPerSample
		'd', 'a', 't', 'a',
		byte(dataSize), byte(dataSize >> 8), byte(dataSize >> 16), byte(dataSize >> 24),
	}

	if _, err := file.Write(header); err != nil {
		return err
	}

	// Write silence (zeros)
	silence := make([]byte, dataSize)
	_, err = file.Write(silence)
	return err
}

// --- Registry Helpers ---

// newTestRegistry creates a fresh AudioSourceRegistry for testing.
// This helper consolidates the common registry creation pattern.
func newTestRegistry() *AudioSourceRegistry {
	return &AudioSourceRegistry{
		sources:       make(map[string]*AudioSource),
		connectionMap: make(map[string]string),
		refCounts:     make(map[string]*int32),
		logger:        getTestLogger(),
	}
}
