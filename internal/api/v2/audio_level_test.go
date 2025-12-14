// internal/api/v2/audio_level_test.go
// Tests for audio level SSE endpoint functionality
package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// TestAudioLevelSSEDataFormat tests the AudioLevelSSEData struct marshaling
func TestAudioLevelSSEDataFormat(t *testing.T) {
	t.Run("empty levels map", func(t *testing.T) {
		data := AudioLevelSSEData{
			Type:   "audio-level",
			Levels: make(map[string]myaudio.AudioLevelData),
		}

		assert.Equal(t, "audio-level", data.Type)
		assert.Empty(t, data.Levels)
	})

	t.Run("single source", func(t *testing.T) {
		data := AudioLevelSSEData{
			Type: "audio-level",
			Levels: map[string]myaudio.AudioLevelData{
				"source1": {
					Level:  45,
					Name:   "Test Source",
					Source: "source1",
				},
			},
		}

		assert.Equal(t, "audio-level", data.Type)
		assert.Len(t, data.Levels, 1)
		assert.Equal(t, 45, data.Levels["source1"].Level)
		assert.Equal(t, "Test Source", data.Levels["source1"].Name)
	})

	t.Run("multiple sources", func(t *testing.T) {
		data := AudioLevelSSEData{
			Type: "audio-level",
			Levels: map[string]myaudio.AudioLevelData{
				"audio_card_1": {
					Level:  50,
					Name:   "Audio Card",
					Source: "audio_card_1",
				},
				"rtsp_camera1": {
					Level:  75,
					Name:   "Camera 1",
					Source: "rtsp_camera1",
				},
			},
		}

		assert.Len(t, data.Levels, 2)
		assert.Equal(t, 50, data.Levels["audio_card_1"].Level)
		assert.Equal(t, 75, data.Levels["rtsp_camera1"].Level)
	})
}

// TestIsSourceInactive tests the source inactivity detection logic
func TestIsSourceInactive(t *testing.T) {
	// Create a minimal controller for testing
	controller := &Controller{}

	t.Run("new source is active", func(t *testing.T) {
		lastUpdate := make(map[string]time.Time)
		lastNonZero := make(map[string]time.Time)
		now := time.Now()

		// Source with no history is considered active
		inactive := controller.isSourceInactive("new_source", now, lastUpdate, lastNonZero)
		assert.False(t, inactive, "New source without history should be active")
	})

	t.Run("recently updated source is active", func(t *testing.T) {
		now := time.Now()
		lastUpdate := map[string]time.Time{
			"source1": now.Add(-1 * time.Second),
		}
		lastNonZero := map[string]time.Time{
			"source1": now.Add(-1 * time.Second),
		}

		inactive := controller.isSourceInactive("source1", now, lastUpdate, lastNonZero)
		assert.False(t, inactive, "Recently updated source should be active")
	})

	t.Run("source with old update is inactive", func(t *testing.T) {
		now := time.Now()
		lastUpdate := map[string]time.Time{
			"source1": now.Add(-20 * time.Second), // Exceeds 15 second threshold
		}
		lastNonZero := map[string]time.Time{
			"source1": now.Add(-5 * time.Second),
		}

		inactive := controller.isSourceInactive("source1", now, lastUpdate, lastNonZero)
		assert.True(t, inactive, "Source with old update should be inactive")
	})

	t.Run("source with old activity is inactive", func(t *testing.T) {
		now := time.Now()
		lastUpdate := map[string]time.Time{
			"source1": now.Add(-5 * time.Second),
		}
		lastNonZero := map[string]time.Time{
			"source1": now.Add(-20 * time.Second), // Exceeds 15 second threshold
		}

		inactive := controller.isSourceInactive("source1", now, lastUpdate, lastNonZero)
		assert.True(t, inactive, "Source with old activity should be inactive")
	})
}

// TestCheckSourceActivity tests the source activity checking for multiple sources
func TestCheckSourceActivity(t *testing.T) {
	controller := &Controller{}

	t.Run("no inactive sources", func(t *testing.T) {
		now := time.Now()
		levels := map[string]myaudio.AudioLevelData{
			"source1": {Level: 50, Name: "Source 1", Source: "source1"},
		}
		lastUpdate := map[string]time.Time{
			"source1": now,
		}
		lastNonZero := map[string]time.Time{
			"source1": now,
		}

		updated := controller.checkSourceActivity(levels, lastUpdate, lastNonZero)
		assert.False(t, updated, "No sources should need update")
		assert.Equal(t, 50, levels["source1"].Level)
	})

	t.Run("inactive source gets zeroed", func(t *testing.T) {
		now := time.Now()
		levels := map[string]myaudio.AudioLevelData{
			"source1": {Level: 50, Name: "Source 1", Source: "source1"},
		}
		lastUpdate := map[string]time.Time{
			"source1": now.Add(-20 * time.Second),
		}
		lastNonZero := map[string]time.Time{
			"source1": now.Add(-20 * time.Second),
		}

		updated := controller.checkSourceActivity(levels, lastUpdate, lastNonZero)
		assert.True(t, updated, "Inactive source should trigger update")
		assert.Equal(t, 0, levels["source1"].Level, "Inactive source level should be zeroed")
	})

	t.Run("already zero source not updated", func(t *testing.T) {
		now := time.Now()
		levels := map[string]myaudio.AudioLevelData{
			"source1": {Level: 0, Name: "Source 1", Source: "source1"},
		}
		lastUpdate := map[string]time.Time{
			"source1": now.Add(-20 * time.Second),
		}
		lastNonZero := map[string]time.Time{
			"source1": now.Add(-20 * time.Second),
		}

		updated := controller.checkSourceActivity(levels, lastUpdate, lastNonZero)
		assert.False(t, updated, "Already zero source should not trigger update")
	})
}

// TestGetAnonymizedSourceNameFallback tests the fallback source name anonymization
func TestGetAnonymizedSourceNameFallback(t *testing.T) {
	controller := &Controller{}

	t.Run("audio card source", func(t *testing.T) {
		name := controller.getAnonymizedSourceNameFallback("audio_card_default")
		assert.Equal(t, "audio-source-1", name)
	})

	t.Run("rtsp source without cache", func(t *testing.T) {
		name := controller.getAnonymizedSourceNameFallback("rtsp_camera123")
		assert.Contains(t, name, "camera-")
	})

	t.Run("file source", func(t *testing.T) {
		name := controller.getAnonymizedSourceNameFallback("file_test.wav")
		assert.Equal(t, "file-source", name)
	})

	t.Run("unknown source", func(t *testing.T) {
		name := controller.getAnonymizedSourceNameFallback("unknown_source")
		assert.Equal(t, "unknown-source", name)
	})
}

// TestAudioLevelManagerConcurrency tests concurrent access to the audio level manager
func TestAudioLevelManagerConcurrency(t *testing.T) {
	t.Run("concurrent connection tracking", func(t *testing.T) {
		// Simulate concurrent connection attempts using unique IPs
		const numGoroutines = 10
		done := make(chan bool, numGoroutines)

		for i := range numGoroutines {
			go func(id int) {
				// Use unique IP for each goroutine to avoid conflicts
				clientIP := "test-" + time.Now().String() + "-" + string(rune('0'+id))
				_, loaded := audioLevelMgr.activeConnections.LoadOrStore(clientIP, time.Now())
				if !loaded {
					audioLevelMgr.activeConnections.Delete(clientIP)
				}
				done <- true
			}(i)
		}

		// Wait for all goroutines
		for range numGoroutines {
			<-done
		}
	})
}
