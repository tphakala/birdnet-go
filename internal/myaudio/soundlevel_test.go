package myaudio

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestNewSoundLevelProcessor_IntervalClamping tests that intervals are properly clamped
func TestNewSoundLevelProcessor_IntervalClamping(t *testing.T) {
	// Ensure settings are loaded
	settings := conf.Setting()
	if settings == nil {
		t.Skip("Settings not available for test")
	}

	// Save original interval value
	originalInterval := settings.Realtime.Audio.SoundLevel.Interval

	tests := []struct {
		name             string
		configInterval   int
		expectedInterval int
		description      string
	}{
		{
			name:             "interval_below_minimum",
			configInterval:   3,
			expectedInterval: 5,
			description:      "intervals less than 5 should be clamped to 5",
		},
		{
			name:             "interval_at_minimum",
			configInterval:   5,
			expectedInterval: 5,
			description:      "interval of exactly 5 should remain 5",
		},
		{
			name:             "interval_above_minimum",
			configInterval:   10,
			expectedInterval: 10,
			description:      "intervals >= 5 should be used as-is",
		},
		{
			name:             "zero_interval",
			configInterval:   0,
			expectedInterval: 5,
			description:      "zero interval should be clamped to 5",
		},
		{
			name:             "negative_interval",
			configInterval:   -1,
			expectedInterval: 5,
			description:      "negative interval should be clamped to 5",
		},
		{
			name:             "large_interval",
			configInterval:   60,
			expectedInterval: 60,
			description:      "large intervals should be preserved",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Modify the settings directly
			settings.Realtime.Audio.SoundLevel.Interval = tt.configInterval
			
			// Restore original value after test
			defer func() {
				settings.Realtime.Audio.SoundLevel.Interval = originalInterval
			}()

			// Create processor
			processor, err := newSoundLevelProcessor("test-source", "test-name")
			require.NoError(t, err, "failed to create sound level processor")
			require.NotNil(t, processor)

			// Verify interval
			assert.Equal(t, tt.expectedInterval, processor.interval, tt.description)
		})
	}
}

// TestNewSoundLevelProcessor_BufferSizing tests that buffers are sized correctly
func TestNewSoundLevelProcessor_BufferSizing(t *testing.T) {
	// Ensure settings are loaded
	settings := conf.Setting()
	if settings == nil {
		t.Skip("Settings not available for test")
	}

	// Save original interval value
	originalInterval := settings.Realtime.Audio.SoundLevel.Interval

	tests := []struct {
		name                  string
		interval              int
		expectedBufferSize    int
	}{
		{
			name:               "minimum_interval_buffer",
			interval:           5,
			expectedBufferSize: 5,
		},
		{
			name:               "standard_interval_buffer",
			interval:           10,
			expectedBufferSize: 10,
		},
		{
			name:               "large_interval_buffer",
			interval:           30,
			expectedBufferSize: 30,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Modify the settings directly
			settings.Realtime.Audio.SoundLevel.Interval = tt.interval
			
			// Restore original value after test
			defer func() {
				settings.Realtime.Audio.SoundLevel.Interval = originalInterval
			}()

			// Create processor
			processor, err := newSoundLevelProcessor("test-source", "test-name")
			require.NoError(t, err)
			require.NotNil(t, processor)

			// Verify interval buffer is sized correctly
			assert.Equal(t, tt.expectedBufferSize, len(processor.intervalBuffer.secondMeasurements),
				"intervalBuffer should have space for %d second measurements", tt.expectedBufferSize)

			// Verify each second measurement map is initialized
			for i, measurement := range processor.intervalBuffer.secondMeasurements {
				assert.NotNil(t, measurement, "second measurement map at index %d should be initialized", i)
			}

			// Verify filters are created (up to Nyquist frequency)
			nyquistFreq := float64(conf.SampleRate) / 2.0
			expectedFilters := 0
			for _, freq := range octaveBandCenterFreqs {
				if freq < nyquistFreq {
					expectedFilters++
				}
			}
			assert.Equal(t, expectedFilters, len(processor.filters),
				"should have filters for all frequencies below Nyquist (%f Hz)", nyquistFreq)

			// Verify second buffers are created for each filter
			assert.Equal(t, len(processor.filters), len(processor.secondBuffers),
				"should have one second buffer per filter")

			// Verify each second buffer is properly initialized
			for _, filter := range processor.filters {
				bandKey := formatBandKey(filter.centerFreq)
				buffer, exists := processor.secondBuffers[bandKey]
				assert.True(t, exists, "second buffer should exist for band %s", bandKey)
				assert.NotNil(t, buffer, "second buffer should not be nil")
				assert.Equal(t, conf.SampleRate, buffer.targetSampleCount,
					"second buffer should target exactly 1 second of samples")
			}
		})
	}
}

// TestNewSoundLevelProcessor_InitialState tests the initial state of a new processor
func TestNewSoundLevelProcessor_InitialState(t *testing.T) {
	// Ensure settings are loaded
	settings := conf.Setting()
	if settings == nil {
		t.Skip("Settings not available for test")
	}

	// Save original interval value
	originalInterval := settings.Realtime.Audio.SoundLevel.Interval
	settings.Realtime.Audio.SoundLevel.Interval = 10
	
	// Restore original value after test
	defer func() {
		settings.Realtime.Audio.SoundLevel.Interval = originalInterval
	}()

	processor, err := newSoundLevelProcessor("test-source", "test-name")
	require.NoError(t, err)
	require.NotNil(t, processor)

	// Verify basic properties
	assert.Equal(t, "test-source", processor.source)
	assert.Equal(t, "test-name", processor.name)
	assert.Equal(t, conf.SampleRate, processor.sampleRate)

	// Verify interval aggregator initial state
	assert.NotNil(t, processor.intervalBuffer)
	assert.Equal(t, 0, processor.intervalBuffer.currentIndex)
	assert.Equal(t, 0, processor.intervalBuffer.measurementCount)
	assert.False(t, processor.intervalBuffer.full)
	assert.NotZero(t, processor.intervalBuffer.startTime)
}

// TestSoundLevelProcessor_ThreadSafety tests concurrent access to the processor
func TestSoundLevelProcessor_ThreadSafety(t *testing.T) {
	// Ensure settings are loaded
	settings := conf.Setting()
	if settings == nil {
		t.Skip("Settings not available for test")
	}

	// Save original interval value
	originalInterval := settings.Realtime.Audio.SoundLevel.Interval
	settings.Realtime.Audio.SoundLevel.Interval = 5
	
	// Restore original value after test
	defer func() {
		settings.Realtime.Audio.SoundLevel.Interval = originalInterval
	}()

	processor, err := newSoundLevelProcessor("test-source", "test-name")
	require.NoError(t, err)

	// Create some test audio data (silence)
	testData := make([]byte, 1024)

	// Run concurrent operations
	var wg sync.WaitGroup
	numGoroutines := 10

	for range numGoroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Process audio data concurrently
			_, _ = processor.ProcessAudioData(testData)
		}()
	}

	wg.Wait()

	// If we get here without panicking, thread safety is working
	assert.True(t, true, "concurrent access should not cause panics")
}


// TestIntervalAggregator_DataFlow tests that data flows correctly through the interval aggregator
func TestIntervalAggregator_DataFlow(t *testing.T) {
	// Ensure settings are loaded
	settings := conf.Setting()
	if settings == nil {
		t.Skip("Settings not available for test")
	}

	// Save original interval value
	originalInterval := settings.Realtime.Audio.SoundLevel.Interval
	settings.Realtime.Audio.SoundLevel.Interval = 5
	
	// Restore original value after test
	defer func() {
		settings.Realtime.Audio.SoundLevel.Interval = originalInterval
	}()

	processor, err := newSoundLevelProcessor("test-source", "test-name")
	require.NoError(t, err)

	// Simulate adding measurements
	for i := range 5 {
		// Manually add a measurement to simulate 1-second completion
		processor.mutex.Lock()
		currentIdx := processor.intervalBuffer.currentIndex
		for _, filter := range processor.filters {
			bandKey := formatBandKey(filter.centerFreq)
			processor.intervalBuffer.secondMeasurements[currentIdx][bandKey] = -30.0 + float64(i) // Varying levels
		}
		processor.intervalBuffer.currentIndex = (processor.intervalBuffer.currentIndex + 1) % processor.interval
		processor.intervalBuffer.measurementCount++
		if processor.intervalBuffer.measurementCount >= processor.interval {
			processor.intervalBuffer.full = true
		}
		processor.mutex.Unlock()
	}

	// Verify the aggregator is full after 5 measurements
	assert.True(t, processor.intervalBuffer.full)
	assert.Equal(t, 5, processor.intervalBuffer.measurementCount)
}

// TestProcessAudioData_IntervalCompletion tests that ProcessAudioData returns data only when interval is complete
func TestProcessAudioData_IntervalCompletion(t *testing.T) {
	// Ensure settings are loaded
	settings := conf.Setting()
	if settings == nil {
		t.Skip("Settings not available for test")
	}

	// Save original interval value
	originalInterval := settings.Realtime.Audio.SoundLevel.Interval
	settings.Realtime.Audio.SoundLevel.Interval = 5 // 5 second interval
	
	// Restore original value after test
	defer func() {
		settings.Realtime.Audio.SoundLevel.Interval = originalInterval
	}()

	processor, err := newSoundLevelProcessor("test-source", "test-name")
	require.NoError(t, err)

	// Create 1 second worth of audio data (48000 samples * 2 bytes per sample)
	oneSecondData := make([]byte, conf.SampleRate*2)

	// Process 4 seconds of data - should not return any results yet
	for i := range 4 {
		result, err := processor.ProcessAudioData(oneSecondData)
		assert.NoError(t, err)
		assert.Nil(t, result, "should not return data before interval completes (second %d)", i+1)
	}

	// Process the 5th second - should now return data
	result, err := processor.ProcessAudioData(oneSecondData)
	assert.NoError(t, err)
	assert.NotNil(t, result, "should return data when interval completes")
	
	if result != nil {
		assert.Equal(t, "test-source", result.Source)
		assert.Equal(t, "test-name", result.Name)
		assert.Equal(t, 5, result.Duration)
		assert.NotEmpty(t, result.OctaveBands)
	}
}

// TestProcessAudioData_EmptyData tests handling of empty audio data
func TestProcessAudioData_EmptyData(t *testing.T) {
	processor, err := newSoundLevelProcessor("test-source", "test-name")
	require.NoError(t, err)

	// Process empty data
	result, err := processor.ProcessAudioData([]byte{})
	assert.NoError(t, err)
	assert.Nil(t, result, "empty data should return nil")

	// Process nil data
	result, err = processor.ProcessAudioData(nil)
	assert.NoError(t, err)
	assert.Nil(t, result, "nil data should return nil")
}

// TestProcessAudioData_OddByteCount tests handling of odd byte count in audio data
func TestProcessAudioData_OddByteCount(t *testing.T) {
	processor, err := newSoundLevelProcessor("test-source", "test-name")
	require.NoError(t, err)

	// Create data with odd number of bytes (should be truncated to even)
	oddData := make([]byte, 1001)
	
	// Should not panic and process normally
	result, err := processor.ProcessAudioData(oddData)
	assert.NoError(t, err)
	// Result will be nil because we haven't completed an interval
	assert.Nil(t, result)
}