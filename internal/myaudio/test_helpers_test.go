// test_helpers_test.go - Shared test helpers for myaudio package
// These helpers reduce duplication across test files and ensure consistent test setup.
package myaudio

import (
	"context"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// --- Logger Helpers ---

// getTestLogger returns a logger for testing.
func getTestLogger() logger.Logger {
	// Use console logger with debug level for tests, output to io.Discard to keep tests quiet
	return logger.NewSlogLogger(io.Discard, logger.LogLevelDebug, nil)
}

// --- FFprobe Helpers ---

// isFFprobeAvailable checks if ffprobe is available in PATH.
// Use this to skip tests that require ffprobe.
func isFFprobeAvailable() bool {
	_, err := exec.LookPath("ffprobe")
	return err == nil
}

// skipIfNoFFprobe skips the test if ffprobe is not available.
func skipIfNoFFprobe(t *testing.T) {
	t.Helper()
	if !isFFprobeAvailable() {
		t.Skip("ffprobe not available, skipping test")
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

// --- Stream Helpers ---

// TestStreamResult holds a test stream and its audio channel for cleanup.
type TestStreamResult struct {
	Stream    *FFmpegStream
	AudioChan chan UnifiedAudioData
	closed    bool
}

// Close cleans up the test stream and channel.
// Safe to call multiple times.
func (r *TestStreamResult) Close() {
	if r.closed {
		return
	}
	r.closed = true
	if r.Stream != nil {
		r.Stream.Stop()
	}
	if r.AudioChan != nil {
		close(r.AudioChan)
	}
}

// newTestStream creates a new FFmpegStream for testing with a default buffer size.
// Returns a TestStreamResult that should be cleaned up with Close() or via t.Cleanup().
func newTestStream(t *testing.T, url string) *TestStreamResult {
	t.Helper()
	return newTestStreamWithBuffer(t, url, 10)
}

// newTestStreamWithBuffer creates a new FFmpegStream for testing with a custom buffer size.
func newTestStreamWithBuffer(t *testing.T, url string, bufferSize int) *TestStreamResult {
	t.Helper()
	audioChan := make(chan UnifiedAudioData, bufferSize)
	stream := NewFFmpegStream(url, "tcp", audioChan)
	result := &TestStreamResult{
		Stream:    stream,
		AudioChan: audioChan,
	}
	t.Cleanup(result.Close)
	return result
}

// --- Context Helpers ---

// testContextWithTimeout creates a context with the given timeout and registers cleanup.
// Uses t.Cleanup() to ensure the cancel function is called.
func testContextWithTimeout(t *testing.T, timeout time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	t.Cleanup(cancel)
	return ctx
}

// testContext creates a context with a default 5-second timeout for tests.
func testContext(t *testing.T) context.Context {
	t.Helper()
	return testContextWithTimeout(t, 5*time.Second)
}

// --- Source Config Helpers ---

// newTestSourceConfig creates a SourceConfig for testing with common defaults.
func newTestSourceConfig(id, displayName string, sourceType SourceType) SourceConfig {
	return SourceConfig{
		ID:          id,
		DisplayName: displayName,
		Type:        sourceType,
	}
}

// newTestRTSPConfig creates a SourceConfig for RTSP sources with common defaults.
func newTestRTSPConfig(id, displayName string) SourceConfig {
	return newTestSourceConfig(id, displayName, SourceTypeRTSP)
}
