package myaudio

import (
	"encoding/binary"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestFFmpegStream_handleAudioData_AppliesFilters verifies that audio filters
// are applied to FFmpeg stream data when equalizer is enabled.
// NOTE: Do NOT use t.Parallel() - this test modifies global state (filterChain, settings, buffers)
func TestFFmpegStream_handleAudioData_AppliesFilters(t *testing.T) {
	// Setup: Initialize settings with a HighPass filter that will modify the audio
	settings := conf.Setting()
	require.NotNil(t, settings, "Settings must be available")

	// Save original state
	oldEnabled := settings.Realtime.Audio.Equalizer.Enabled
	oldFilters := settings.Realtime.Audio.Equalizer.Filters

	// Configure a strong high-pass filter at 1000Hz that will noticeably affect low-frequency content
	settings.Realtime.Audio.Equalizer.Enabled = true
	settings.Realtime.Audio.Equalizer.Filters = []conf.EqualizerFilter{
		{Type: "HighPass", Frequency: 1000, Q: 0.707, Passes: 4},
	}

	t.Cleanup(func() {
		settings.Realtime.Audio.Equalizer.Enabled = oldEnabled
		settings.Realtime.Audio.Equalizer.Filters = oldFilters
	})

	// Initialize filter chain
	err := InitializeFilterChain(settings)
	require.NoError(t, err, "Filter chain must initialize")

	// Create a unique test URL to avoid conflicts with other tests
	testURL := fmt.Sprintf("rtsp://filter-test-%d.example.com/stream", time.Now().UnixNano())

	// Create FFmpegStream which registers the audio source
	audioChan := make(chan UnifiedAudioData, 100)
	defer close(audioChan)

	stream := NewFFmpegStream(testURL, "tcp", audioChan)
	require.NotNil(t, stream)

	// Get the actual source ID and allocate buffers
	actualSourceID := stream.source.ID

	err = AllocateAnalysisBuffer(conf.BufferSize*3, actualSourceID)
	if err != nil {
		t.Skipf("Cannot allocate analysis buffer for test: %v", err)
	}
	t.Cleanup(func() {
		if removeErr := RemoveAnalysisBuffer(actualSourceID); removeErr != nil {
			t.Logf("Failed to remove analysis buffer: %v", removeErr)
		}
	})

	err = AllocateCaptureBufferIfNeeded(60, conf.SampleRate, conf.BitDepth/8, actualSourceID)
	if err != nil {
		t.Skipf("Cannot allocate capture buffer for test: %v", err)
	}
	t.Cleanup(func() {
		if removeErr := RemoveCaptureBuffer(actualSourceID); removeErr != nil {
			t.Logf("Failed to remove capture buffer: %v", removeErr)
		}
	})

	// Create a low-frequency test signal (100Hz sine wave)
	// This should be significantly attenuated by the 1000Hz high-pass filter
	sampleRate := float64(conf.SampleRate)
	frequency := 100.0                 // Hz - well below the 1000Hz cutoff
	numSamples := int(sampleRate / 10) // 0.1 seconds of audio

	originalSamples := make([]byte, numSamples*2)
	for i := range numSamples {
		// Generate 100Hz sine wave at 50% amplitude
		val := int16(math.Sin(2*math.Pi*frequency*float64(i)/sampleRate) * 16384) //nolint:gosec // G115: sin*16384 is always in int16 range
		binary.LittleEndian.PutUint16(originalSamples[i*2:], uint16(val))         //nolint:gosec // G115: intentional int16→uint16 for PCM test data
	}

	// Make a copy to process through handleAudioData
	filteredSamples := make([]byte, len(originalSamples))
	copy(filteredSamples, originalSamples)

	// Calculate RMS of original signal before processing
	originalRMS := calculateRMSFromBytes(originalSamples)

	// Call handleAudioData - this should apply filters to filteredSamples
	err = stream.handleAudioData(filteredSamples)
	require.NoError(t, err)

	// Calculate RMS of filtered signal
	filteredRMS := calculateRMSFromBytes(filteredSamples)

	// The high-pass filter should significantly attenuate the 100Hz signal
	// We expect at least 50% reduction (6dB) for a 4-pass high-pass at 1000Hz
	attenuation := filteredRMS / originalRMS
	assert.Less(t, attenuation, 0.5,
		"100Hz signal should be attenuated by high-pass filter at 1000Hz (got %.2f%% of original)",
		attenuation*100)
}

// TestFFmpegStream_handleAudioData_NoFiltersWhenDisabled verifies that audio
// is passed through unchanged when equalizer is disabled.
// NOTE: Do NOT use t.Parallel() - this test modifies global state (filterChain, settings, buffers)
func TestFFmpegStream_handleAudioData_NoFiltersWhenDisabled(t *testing.T) {
	settings := conf.Setting()
	require.NotNil(t, settings, "Settings must be available")

	// Save original state
	oldEnabled := settings.Realtime.Audio.Equalizer.Enabled
	oldFilters := settings.Realtime.Audio.Equalizer.Filters

	// Disable equalizer but configure filters (they should not be applied)
	settings.Realtime.Audio.Equalizer.Enabled = false
	settings.Realtime.Audio.Equalizer.Filters = []conf.EqualizerFilter{
		{Type: "HighPass", Frequency: 1000, Q: 0.707, Passes: 4},
	}

	t.Cleanup(func() {
		settings.Realtime.Audio.Equalizer.Enabled = oldEnabled
		settings.Realtime.Audio.Equalizer.Filters = oldFilters
	})

	// Initialize filter chain (should be empty since disabled)
	err := InitializeFilterChain(settings)
	require.NoError(t, err)

	// Create a unique test URL to avoid conflicts with other tests
	testURL := fmt.Sprintf("rtsp://filter-disabled-test-%d.example.com/stream", time.Now().UnixNano())

	// Create FFmpegStream which registers the audio source
	audioChan := make(chan UnifiedAudioData, 100)
	defer close(audioChan)

	stream := NewFFmpegStream(testURL, "tcp", audioChan)
	require.NotNil(t, stream)

	// Get the actual source ID and allocate buffers
	actualSourceID := stream.source.ID

	err = AllocateAnalysisBuffer(conf.BufferSize*3, actualSourceID)
	if err != nil {
		t.Skipf("Cannot allocate analysis buffer for test: %v", err)
	}
	t.Cleanup(func() {
		if removeErr := RemoveAnalysisBuffer(actualSourceID); removeErr != nil {
			t.Logf("Failed to remove analysis buffer: %v", removeErr)
		}
	})

	err = AllocateCaptureBufferIfNeeded(60, conf.SampleRate, conf.BitDepth/8, actualSourceID)
	if err != nil {
		t.Skipf("Cannot allocate capture buffer for test: %v", err)
	}
	t.Cleanup(func() {
		if removeErr := RemoveCaptureBuffer(actualSourceID); removeErr != nil {
			t.Logf("Failed to remove capture buffer: %v", removeErr)
		}
	})

	// Create test audio (100Hz sine wave)
	sampleRate := float64(conf.SampleRate)
	frequency := 100.0
	numSamples := int(sampleRate / 10)

	originalSamples := make([]byte, numSamples*2)
	for i := range numSamples {
		val := int16(math.Sin(2*math.Pi*frequency*float64(i)/sampleRate) * 16384) //nolint:gosec // G115: sin*16384 is always in int16 range
		binary.LittleEndian.PutUint16(originalSamples[i*2:], uint16(val))         //nolint:gosec // G115: intentional int16→uint16 for PCM test data
	}

	testSamples := make([]byte, len(originalSamples))
	copy(testSamples, originalSamples)

	// Process audio through handleAudioData
	err = stream.handleAudioData(testSamples)
	require.NoError(t, err)

	// Audio should be unchanged when equalizer is disabled
	assert.Equal(t, originalSamples, testSamples, "Audio should be unchanged when equalizer is disabled")
}

// calculateRMSFromBytes computes the root mean square of PCM16 audio samples from byte slice
func calculateRMSFromBytes(samples []byte) float64 {
	if len(samples) < 2 {
		return 0
	}

	var sumSquares float64
	numSamples := len(samples) / 2

	for i := range numSamples {
		val := int16(binary.LittleEndian.Uint16(samples[i*2:])) //nolint:gosec // G115: intentional uint16→int16 for PCM test verification
		normalized := float64(val) / 32768.0
		sumSquares += normalized * normalized
	}

	return math.Sqrt(sumSquares / float64(numSamples))
}
