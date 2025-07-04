package adapter

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

	// Wait for some data with context timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	select {
	case <-ctx.Done():
		// Timeout is OK for this test
	case data := <-unifiedAudioChan:
		assert.False(t, data.Timestamp.IsZero(), "Received audio data should have non-zero timestamp")
	}

	// Signal quit
	close(quitChan)

	// Wait for cleanup
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cleanupCancel()

	select {
	case <-done:
		// Good, cleanup completed
	case <-cleanupCtx.Done():
		assert.Fail(t, "Timeout waiting for adapter to stop")
	}
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
				assert.Equal(t, float32(0), result, "Expected zero audio level")
			} else {
				assert.InDelta(t, tt.expected, result, 0.001, "Audio level calculation mismatch")
			}
		})
	}
}

// MockTimeProvider is a test time provider that can be controlled
type MockTimeProvider struct {
	currentTime time.Time
}

// Now returns the mocked current time
func (m *MockTimeProvider) Now() time.Time {
	return m.currentTime
}

// Advance moves time forward by the given duration
func (m *MockTimeProvider) Advance(d time.Duration) {
	m.currentTime = m.currentTime.Add(d)
}

func TestSoundLevelAnalyzer(t *testing.T) {
	t.Parallel()

	mockTime := &MockTimeProvider{currentTime: time.Now()}
	analyzer := NewSoundLevelAnalyzerWithTimeProvider(1, mockTime) // 1 second interval

	// First process should return nil (not enough time passed)
	result := analyzer.Process([]byte{0, 0, 0, 0})
	assert.Nil(t, result, "Expected nil on first process call")

	// Advance time by 2 seconds
	mockTime.Advance(2 * time.Second)

	// Now process should return data
	result = analyzer.Process([]byte{0, 0, 0, 0})
	assert.NotNil(t, result, "Expected sound level data after interval")

	if result != nil {
		assert.Len(t, result.OctaveBands, 6, "Expected 6 octave band levels")

		// Check one octave band
		band, ok := result.OctaveBands["1000"]
		assert.True(t, ok, "Expected 1000Hz octave band to be present")
		if ok {
			assert.Equal(t, 60.0, band.Mean, "Expected mean 60.0 for 1000Hz band")
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

	restartCtx, restartCancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer restartCancel()

	select {
	case <-done:
		// Good, adapter exited on restart
	case <-restartCtx.Done():
		assert.Fail(t, "Timeout waiting for adapter to handle restart")
	}

	close(quitChan)
}
