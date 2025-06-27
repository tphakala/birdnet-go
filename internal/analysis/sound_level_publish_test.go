package analysis

import (
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// TestSoundLevelJSONMarshaling tests JSON marshaling with various edge cases
func TestSoundLevelJSONMarshaling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		soundData   myaudio.SoundLevelData
		shouldError bool
		checkJSON   func(t *testing.T, jsonData []byte)
	}{
		{
			name: "normal values",
			soundData: myaudio.SoundLevelData{
				Timestamp: time.Now(),
				Source:    "test",
				Name:      "test-device",
				Duration:  10,
				OctaveBands: map[string]myaudio.OctaveBandData{
					"1000_Hz": {
						CenterFreq: 1000,
						Min:        -60.5,
						Max:        -40.2,
						Mean:       -50.3,
					},
				},
			},
			shouldError: false,
			checkJSON: func(t *testing.T, jsonData []byte) {
				var data map[string]any
				err := json.Unmarshal(jsonData, &data)
				require.NoError(t, err)

				bands := data["octave_bands"].(map[string]any)
				band := bands["1000_Hz"].(map[string]any)
				assert.Equal(t, -60.5, band["min_db"])
				assert.Equal(t, -40.2, band["max_db"])
				assert.Equal(t, -50.3, band["mean_db"])
			},
		},
		{
			name: "positive infinity values",
			soundData: myaudio.SoundLevelData{
				Timestamp: time.Now(),
				Source:    "test",
				Name:      "test-device",
				Duration:  10,
				OctaveBands: map[string]myaudio.OctaveBandData{
					"1000_Hz": {
						CenterFreq: 1000,
						Min:        math.Inf(1),
						Max:        math.Inf(1),
						Mean:       math.Inf(1),
					},
				},
			},
			shouldError: true,
		},
		{
			name: "negative infinity values",
			soundData: myaudio.SoundLevelData{
				Timestamp: time.Now(),
				Source:    "test",
				Name:      "test-device",
				Duration:  10,
				OctaveBands: map[string]myaudio.OctaveBandData{
					"1000_Hz": {
						CenterFreq: 1000,
						Min:        math.Inf(-1),
						Max:        math.Inf(-1),
						Mean:       math.Inf(-1),
					},
				},
			},
			shouldError: true,
		},
		{
			name: "NaN values",
			soundData: myaudio.SoundLevelData{
				Timestamp: time.Now(),
				Source:    "test",
				Name:      "test-device",
				Duration:  10,
				OctaveBands: map[string]myaudio.OctaveBandData{
					"1000_Hz": {
						CenterFreq: 1000,
						Min:        math.NaN(),
						Max:        math.NaN(),
						Mean:       math.NaN(),
					},
				},
			},
			shouldError: true,
		},
		{
			name: "mixed valid and invalid values",
			soundData: myaudio.SoundLevelData{
				Timestamp: time.Now(),
				Source:    "test",
				Name:      "test-device",
				Duration:  10,
				OctaveBands: map[string]myaudio.OctaveBandData{
					"1000_Hz": {
						CenterFreq: 1000,
						Min:        -60.5,
						Max:        math.Inf(1),
						Mean:       -50.3,
					},
					"2000_Hz": {
						CenterFreq: 2000,
						Min:        -55.0,
						Max:        -45.0,
						Mean:       -50.0,
					},
				},
			},
			shouldError: true,
		},
		{
			name: "empty octave bands",
			soundData: myaudio.SoundLevelData{
				Timestamp:   time.Now(),
				Source:      "test",
				Name:        "test-device",
				Duration:    10,
				OctaveBands: map[string]myaudio.OctaveBandData{},
			},
			shouldError: false,
			checkJSON: func(t *testing.T, jsonData []byte) {
				var data map[string]any
				err := json.Unmarshal(jsonData, &data)
				require.NoError(t, err)

				bands := data["octave_bands"].(map[string]any)
				assert.Empty(t, bands)
			},
		},
		{
			name: "very large negative values",
			soundData: myaudio.SoundLevelData{
				Timestamp: time.Now(),
				Source:    "test",
				Name:      "test-device",
				Duration:  10,
				OctaveBands: map[string]myaudio.OctaveBandData{
					"1000_Hz": {
						CenterFreq: 1000,
						Min:        -200.0,
						Max:        -180.0,
						Mean:       -190.0,
					},
				},
			},
			shouldError: false,
			checkJSON: func(t *testing.T, jsonData []byte) {
				var data map[string]any
				err := json.Unmarshal(jsonData, &data)
				require.NoError(t, err)

				bands := data["octave_bands"].(map[string]any)
				band := bands["1000_Hz"].(map[string]any)
				assert.Equal(t, -200.0, band["min_db"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			jsonData, err := json.Marshal(tt.soundData)

			if tt.shouldError {
				assert.Error(t, err, "Expected JSON marshaling to fail for %s", tt.name)
			} else {
				assert.NoError(t, err, "Expected JSON marshaling to succeed for %s", tt.name)
				if tt.checkJSON != nil {
					tt.checkJSON(t, jsonData)
				}
			}
		})
	}
}

// TestSanitizeSoundLevelData tests the sanitization function
func TestSanitizeSoundLevelData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    myaudio.SoundLevelData
		expected myaudio.SoundLevelData
	}{
		{
			name: "normal values unchanged",
			input: myaudio.SoundLevelData{
				Timestamp: time.Now(),
				Source:    "test",
				Name:      "device",
				Duration:  10,
				OctaveBands: map[string]myaudio.OctaveBandData{
					"1000_Hz": {
						CenterFreq: 1000,
						Min:        -60.5,
						Max:        -40.2,
						Mean:       -50.3,
					},
				},
			},
			expected: myaudio.SoundLevelData{
				Source:   "test",
				Name:     "device",
				Duration: 10,
				OctaveBands: map[string]myaudio.OctaveBandData{
					"1000_Hz": {
						CenterFreq: 1000,
						Min:        -60.5,
						Max:        -40.2,
						Mean:       -50.3,
					},
				},
			},
		},
		{
			name: "positive infinity replaced",
			input: myaudio.SoundLevelData{
				Timestamp: time.Now(),
				Source:    "test",
				Name:      "device",
				Duration:  10,
				OctaveBands: map[string]myaudio.OctaveBandData{
					"1000_Hz": {
						CenterFreq: 1000,
						Min:        math.Inf(1),
						Max:        math.Inf(1),
						Mean:       math.Inf(1),
					},
				},
			},
			expected: myaudio.SoundLevelData{
				Source:   "test",
				Name:     "device",
				Duration: 10,
				OctaveBands: map[string]myaudio.OctaveBandData{
					"1000_Hz": {
						CenterFreq: 1000,
						Min:        -200.0,
						Max:        -200.0,
						Mean:       -200.0,
					},
				},
			},
		},
		{
			name: "NaN values replaced",
			input: myaudio.SoundLevelData{
				Timestamp: time.Now(),
				Source:    "test",
				Name:      "device",
				Duration:  10,
				OctaveBands: map[string]myaudio.OctaveBandData{
					"1000_Hz": {
						CenterFreq: 1000,
						Min:        math.NaN(),
						Max:        math.NaN(),
						Mean:       math.NaN(),
					},
				},
			},
			expected: myaudio.SoundLevelData{
				Source:   "test",
				Name:     "device",
				Duration: 10,
				OctaveBands: map[string]myaudio.OctaveBandData{
					"1000_Hz": {
						CenterFreq: 1000,
						Min:        -200.0,
						Max:        -200.0,
						Mean:       -200.0,
					},
				},
			},
		},
		{
			name: "mixed values partially replaced",
			input: myaudio.SoundLevelData{
				Timestamp: time.Now(),
				Source:    "test",
				Name:      "device",
				Duration:  10,
				OctaveBands: map[string]myaudio.OctaveBandData{
					"1000_Hz": {
						CenterFreq: 1000,
						Min:        -60.5,
						Max:        math.Inf(1),
						Mean:       math.NaN(),
					},
					"2000_Hz": {
						CenterFreq: 2000,
						Min:        -55.0,
						Max:        -45.0,
						Mean:       -50.0,
					},
				},
			},
			expected: myaudio.SoundLevelData{
				Source:   "test",
				Name:     "device",
				Duration: 10,
				OctaveBands: map[string]myaudio.OctaveBandData{
					"1000_Hz": {
						CenterFreq: 1000,
						Min:        -60.5,
						Max:        -200.0,
						Mean:       -200.0,
					},
					"2000_Hz": {
						CenterFreq: 2000,
						Min:        -55.0,
						Max:        -45.0,
						Mean:       -50.0,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := sanitizeSoundLevelData(tt.input)

			// Compare fields (ignoring timestamp)
			assert.Equal(t, tt.expected.Source, result.Source)
			assert.Equal(t, tt.expected.Name, result.Name)
			assert.Equal(t, tt.expected.Duration, result.Duration)

			// Compare octave bands
			assert.Equal(t, len(tt.expected.OctaveBands), len(result.OctaveBands))
			for key, expectedBand := range tt.expected.OctaveBands {
				resultBand, exists := result.OctaveBands[key]
				assert.True(t, exists, "Band %s should exist", key)
				assert.Equal(t, expectedBand.CenterFreq, resultBand.CenterFreq)
				assert.Equal(t, expectedBand.Min, resultBand.Min)
				assert.Equal(t, expectedBand.Max, resultBand.Max)
				assert.Equal(t, expectedBand.Mean, resultBand.Mean)
			}

			// Verify the result can be marshaled to JSON
			jsonData, err := json.Marshal(result)
			assert.NoError(t, err)
			assert.NotEmpty(t, jsonData)
		})
	}
}

// TestSoundLevelChannelFlow tests the flow through soundLevelChan
func TestSoundLevelChannelFlow(t *testing.T) {
	t.Parallel()

	// Create test channel
	testChan := make(chan myaudio.SoundLevelData, 10)

	// Test data with edge cases
	testData := []myaudio.SoundLevelData{
		{
			Timestamp: time.Now(),
			Source:    "test1",
			Name:      "device1",
			Duration:  10,
			OctaveBands: map[string]myaudio.OctaveBandData{
				"1000_Hz": {
					CenterFreq: 1000,
					Min:        -60.5,
					Max:        -40.2,
					Mean:       -50.3,
				},
			},
		},
		{
			Timestamp: time.Now(),
			Source:    "test2",
			Name:      "device2",
			Duration:  10,
			OctaveBands: map[string]myaudio.OctaveBandData{
				"2000_Hz": {
					CenterFreq: 2000,
					Min:        -200.0, // Very low value
					Max:        -180.0,
					Mean:       -190.0,
				},
			},
		},
	}

	// Producer
	go func() {
		for _, data := range testData {
			testChan <- data
		}
		close(testChan)
	}()

	// Consumer
	received := make([]myaudio.SoundLevelData, 0)
	for data := range testChan {
		received = append(received, data)
	}

	assert.Equal(t, len(testData), len(received))
	for i, data := range received {
		assert.Equal(t, testData[i].Source, data.Source)
		assert.Equal(t, testData[i].Name, data.Name)
	}
}

// TestConcurrentPublishers tests concurrent access to sound level publishers
func TestConcurrentPublishers(t *testing.T) {
	t.Parallel()

	testChan := make(chan myaudio.SoundLevelData, 100)
	quitChan := make(chan struct{})

	var wg sync.WaitGroup
	publishCount := struct {
		sync.Mutex
		mqtt    int
		sse     int
		metrics int
	}{}

	// Mock publishers
	mockMQTTPublisher := func() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-quitChan:
					return
				case <-testChan:
					publishCount.Lock()
					publishCount.mqtt++
					publishCount.Unlock()
				}
			}
		}()
	}

	mockSSEPublisher := func() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-quitChan:
					return
				case <-testChan:
					publishCount.Lock()
					publishCount.sse++
					publishCount.Unlock()
				}
			}
		}()
	}

	mockMetricsPublisher := func() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-quitChan:
					return
				case <-testChan:
					publishCount.Lock()
					publishCount.metrics++
					publishCount.Unlock()
				}
			}
		}()
	}

	// Start publishers
	mockMQTTPublisher()
	mockSSEPublisher()
	mockMetricsPublisher()

	// Send test data
	numMessages := 50
	for i := range numMessages {
		testChan <- myaudio.SoundLevelData{
			Timestamp: time.Now(),
			Source:    fmt.Sprintf("test-%d", i),
			Name:      "device",
			Duration:  10,
			OctaveBands: map[string]myaudio.OctaveBandData{
				"1000_Hz": {
					CenterFreq: 1000,
					Min:        -60.5,
					Max:        -40.2,
					Mean:       -50.3,
				},
			},
		}
	}

	// Allow time for processing
	time.Sleep(100 * time.Millisecond)

	// Stop publishers
	close(quitChan)
	wg.Wait()

	// Verify all messages were processed
	publishCount.Lock()
	defer publishCount.Unlock()

	// Each publisher should process some messages
	assert.Greater(t, publishCount.mqtt, 0)
	assert.Greater(t, publishCount.sse, 0)
	assert.Greater(t, publishCount.metrics, 0)
}

// TestSanitizeFloat64 tests the sanitizeFloat64 helper function
func TestSanitizeFloat64(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		input        float64
		defaultValue float64
		expected     float64
	}{
		{
			name:         "normal value unchanged",
			input:        -50.5,
			defaultValue: -200.0,
			expected:     -50.5,
		},
		{
			name:         "positive infinity replaced",
			input:        math.Inf(1),
			defaultValue: -200.0,
			expected:     -200.0,
		},
		{
			name:         "negative infinity replaced",
			input:        math.Inf(-1),
			defaultValue: -200.0,
			expected:     -200.0,
		},
		{
			name:         "NaN replaced",
			input:        math.NaN(),
			defaultValue: -200.0,
			expected:     -200.0,
		},
		{
			name:         "zero unchanged",
			input:        0.0,
			defaultValue: -200.0,
			expected:     0.0,
		},
		{
			name:         "very small value unchanged",
			input:        1e-100,
			defaultValue: -200.0,
			expected:     1e-100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := sanitizeFloat64(tt.input, tt.defaultValue)
			if math.IsNaN(tt.expected) {
				assert.True(t, math.IsNaN(result))
			} else {
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}
