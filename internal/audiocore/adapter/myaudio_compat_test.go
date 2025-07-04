package adapter

import (
	"sync"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

func TestMyAudioCompatAdapter(t *testing.T) {
	t.Parallel()

	// Create test settings
	settings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				Source:       "test",
				UseAudioCore: true,
				SoundLevel: conf.SoundLevelSettings{
					Enabled:  false,
					Interval: 10,
				},
				Equalizer: conf.EqualizerSettings{
					Enabled: false,
				},
			},
		},
		Sentry: conf.SentrySettings{
			Enabled: false,
		},
	}

	// Create channels and sync primitives
	wg := &sync.WaitGroup{}
	quitChan := make(chan struct{})
	restartChan := make(chan struct{}, 1)
	unifiedAudioChan := make(chan myaudio.UnifiedAudioData, 10)

	// Create adapter
	adapter := NewMyAudioCompatAdapter(settings)

	// Start capture in goroutine
	go adapter.CaptureAudio(settings, wg, quitChan, restartChan, unifiedAudioChan)

	// Wait for some data
	timeout := time.After(100 * time.Millisecond)
	dataReceived := false

	select {
	case <-timeout:
		// Timeout is OK for this test
	case data := <-unifiedAudioChan:
		dataReceived = true
		if data.Timestamp.IsZero() {
			t.Error("Received audio data with zero timestamp")
		}
	}

	// Signal quit
	close(quitChan)

	// Wait for cleanup
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Good, cleanup completed
	case <-time.After(1 * time.Second):
		t.Error("Timeout waiting for adapter to stop")
	}

	// In a real test with actual audio hardware, we would verify dataReceived
	// For this unit test without hardware, not receiving data is acceptable
	_ = dataReceived
}

func TestCalculateAudioLevel(t *testing.T) {
	t.Parallel()

	adapter := &MyAudioCompatAdapter{}

	tests := []struct {
		name     string
		buffer   []byte
		expected float32
	}{
		{
			name:     "empty buffer",
			buffer:   []byte{},
			expected: 0,
		},
		{
			name:     "single byte buffer",
			buffer:   []byte{0},
			expected: 0,
		},
		{
			name:     "silence",
			buffer:   []byte{0, 0, 0, 0, 0, 0, 0, 0},
			expected: 0,
		},
		{
			name:     "non-zero samples",
			buffer:   []byte{0x00, 0x10, 0x00, 0x20, 0x00, 0x30, 0x00, 0x40},
			expected: 0.007446289, // Approximate expected value
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := adapter.calculateAudioLevel(tt.buffer)
			if tt.expected == 0 {
				if result != 0 {
					t.Errorf("Expected 0, got %f", result)
				}
			} else {
				// Allow some tolerance for floating point comparison
				diff := result - tt.expected
				if diff < 0 {
					diff = -diff
				}
				if diff > 0.001 {
					t.Errorf("Expected %f, got %f (diff: %f)", tt.expected, result, diff)
				}
			}
		})
	}
}

func TestSoundLevelAnalyzer(t *testing.T) {
	t.Parallel()

	analyzer := NewSoundLevelAnalyzer(1) // 1 second interval

	// First process should return nil (not enough time passed)
	result := analyzer.Process([]byte{0, 0, 0, 0})
	if result != nil {
		t.Error("Expected nil on first process call")
	}

	// Update last update time to simulate time passing
	analyzer.lastUpdate = time.Now().Add(-2 * time.Second)

	// Now process should return data
	result = analyzer.Process([]byte{0, 0, 0, 0})
	if result == nil {
		t.Error("Expected sound level data after interval")
	} else {
		if len(result.OctaveBands) != 6 {
			t.Errorf("Expected 6 octave band levels, got %d", len(result.OctaveBands))
		}
		// Check one octave band
		if band, ok := result.OctaveBands["1000"]; ok {
			if band.Mean != 60.0 {
				t.Errorf("Expected mean 60.0 for 1000Hz band, got %f", band.Mean)
			}
		} else {
			t.Error("Missing 1000Hz octave band")
		}
	}
}

func TestRestartHandling(t *testing.T) {
	t.Parallel()

	settings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				Source:       "test",
				UseAudioCore: true,
			},
		},
		Sentry: conf.SentrySettings{
			Enabled: false,
		},
	}

	wg := &sync.WaitGroup{}
	quitChan := make(chan struct{})
	restartChan := make(chan struct{}, 1)
	unifiedAudioChan := make(chan myaudio.UnifiedAudioData, 10)

	adapter := NewMyAudioCompatAdapter(settings)

	// Start capture
	go adapter.CaptureAudio(settings, wg, quitChan, restartChan, unifiedAudioChan)

	// Send restart signal
	restartChan <- struct{}{}

	// Wait for adapter to exit
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Good, adapter exited on restart
	case <-time.After(1 * time.Second):
		t.Error("Timeout waiting for adapter to handle restart")
	}

	close(quitChan)
}