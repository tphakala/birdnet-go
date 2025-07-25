// Package analysis provides hot reload testing infrastructure
package analysis

import (
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/httpcontroller/handlers"
)

// HotReloadTestSuite provides common testing infrastructure for hot reload features
type HotReloadTestSuite struct {
	t                *testing.T
	controlChan      chan string
	notificationChan chan handlers.Notification
	quitChan         chan struct{}
	wg               sync.WaitGroup
	settings         *conf.Settings
	settingsMutex    sync.RWMutex
}

// NewHotReloadTestSuite creates a new test suite for hot reload testing
func NewHotReloadTestSuite(t *testing.T) *HotReloadTestSuite {
	t.Helper()
	
	return &HotReloadTestSuite{
		t:                t,
		controlChan:      make(chan string, 10),
		notificationChan: make(chan handlers.Notification, 10),
		quitChan:         make(chan struct{}),
		settings:         createTestSettings(t),
	}
}

// Cleanup cleans up test resources
func (s *HotReloadTestSuite) Cleanup() {
	s.t.Helper()
	
	close(s.quitChan)
	s.wg.Wait()
	close(s.controlChan)
	close(s.notificationChan)
}

// SendControlSignal sends a control signal and waits for processing
func (s *HotReloadTestSuite) SendControlSignal(signal string) {
	s.t.Helper()
	
	select {
	case s.controlChan <- signal:
		// Give time for signal to be processed
		time.Sleep(100 * time.Millisecond)
	case <-time.After(time.Second):
		s.t.Fatalf("Failed to send control signal: %s", signal)
	}
}

// ExpectNotification waits for and returns a notification
func (s *HotReloadTestSuite) ExpectNotification(timeout time.Duration) *handlers.Notification {
	s.t.Helper()
	
	select {
	case notification := <-s.notificationChan:
		return &notification
	case <-time.After(timeout):
		s.t.Fatal("Expected notification but got none")
		return nil
	}
}

// UpdateSettings updates test settings safely
func (s *HotReloadTestSuite) UpdateSettings(modifier func(*conf.Settings)) {
	s.t.Helper()
	
	s.settingsMutex.Lock()
	defer s.settingsMutex.Unlock()
	
	modifier(s.settings)
	
	// In real implementation, this would update the global settings
	// For testing, we simulate the settings update
}

// GetSettings returns a copy of current settings
func (s *HotReloadTestSuite) GetSettings() *conf.Settings {
	s.t.Helper()
	
	s.settingsMutex.RLock()
	defer s.settingsMutex.RUnlock()
	
	// Return a copy to prevent concurrent modification
	settingsCopy := *s.settings
	return &settingsCopy
}

// AssertNoGoroutineLeaks checks for goroutine leaks
func (s *HotReloadTestSuite) AssertNoGoroutineLeaks(baseline, tolerance int) {
	s.t.Helper()
	
	// Give goroutines time to clean up
	time.Sleep(500 * time.Millisecond)
	
	current := runtime.NumGoroutine()
	if current > baseline+tolerance {
		s.t.Errorf("Goroutine leak detected: baseline=%d, current=%d, tolerance=%d",
			baseline, current, tolerance)
	}
}

// createTestSettings creates minimal test settings
func createTestSettings(t *testing.T) *conf.Settings {
	t.Helper()
	
	return &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				Source: "", // Empty for testing
				SoundLevel: conf.SoundLevelSettings{
					Enabled:              false,
					Interval:             10,
					Debug:                false,
					DebugRealtimeLogging: false,
				},
			},
			MQTT: conf.MQTTSettings{
				Enabled: false,
			},
			RTSP: conf.RTSPSettings{
				URLs: []string{},
			},
			Telemetry: conf.TelemetrySettings{
				Enabled: false,
				Listen:  "0.0.0.0:8090",
			},
			Interval: 15, // Global detection interval
		},
		BirdNET: conf.BirdNETConfig{
			Locale:     "en",
			Latitude:   35.0,
			Longitude:  -120.0,
			Threads:    0,
			ModelPath:  "",
			LabelPath:  "",
			UseXNNPACK: false,
		},
	}
}

// TestHotReloadFramework tests the hot reload test framework itself
func TestHotReloadFramework(t *testing.T) {
	t.Parallel()
	
	suite := NewHotReloadTestSuite(t)
	defer suite.Cleanup()
	
	// Test settings update
	suite.UpdateSettings(func(s *conf.Settings) {
		s.Realtime.Audio.SoundLevel.Enabled = true
	})
	
	settings := suite.GetSettings()
	assert.True(t, settings.Realtime.Audio.SoundLevel.Enabled,
		"Settings update should work")
	
	// Test control signal (would be consumed by control monitor in real test)
	go func() {
		select {
		case signal := <-suite.controlChan:
			assert.Equal(t, "test_signal", signal, "Should receive test signal")
		case <-time.After(time.Second):
			t.Error("Did not receive expected signal")
		}
	}()
	
	suite.SendControlSignal("test_signal")
	
	// Test notification
	go func() {
		suite.notificationChan <- handlers.Notification{
			Message: "Test notification",
			Type:    "success",
		}
	}()
	
	notification := suite.ExpectNotification(time.Second)
	require.NotNil(t, notification)
	assert.Equal(t, "Test notification", notification.Message)
	assert.Equal(t, "success", notification.Type)
}

// TestSoundLevelHotReloadScenarios tests various hot reload scenarios
func TestSoundLevelHotReloadScenarios(t *testing.T) {
	scenarios := []struct {
		name     string
		setup    func(*HotReloadTestSuite)
		action   func(*HotReloadTestSuite)
		verify   func(*HotReloadTestSuite)
	}{
		{
			name: "Enable sound level monitoring",
			setup: func(s *HotReloadTestSuite) {
				s.UpdateSettings(func(settings *conf.Settings) {
					settings.Realtime.Audio.SoundLevel.Enabled = false
				})
			},
			action: func(s *HotReloadTestSuite) {
				s.UpdateSettings(func(settings *conf.Settings) {
					settings.Realtime.Audio.SoundLevel.Enabled = true
				})
				s.SendControlSignal("reconfigure_sound_level")
			},
			verify: func(s *HotReloadTestSuite) {
				notification := s.ExpectNotification(2 * time.Second)
				assert.Contains(t, notification.Message, "Sound level monitoring")
			},
		},
		{
			name: "Change interval",
			setup: func(s *HotReloadTestSuite) {
				s.UpdateSettings(func(settings *conf.Settings) {
					settings.Realtime.Audio.SoundLevel.Enabled = true
					settings.Realtime.Audio.SoundLevel.Interval = 10
				})
			},
			action: func(s *HotReloadTestSuite) {
				s.UpdateSettings(func(settings *conf.Settings) {
					settings.Realtime.Audio.SoundLevel.Interval = 30
				})
				s.SendControlSignal("reconfigure_sound_level")
			},
			verify: func(s *HotReloadTestSuite) {
				notification := s.ExpectNotification(2 * time.Second)
				assert.Contains(t, notification.Message, "interval: 30")
			},
		},
		{
			name: "Toggle debug mode",
			setup: func(s *HotReloadTestSuite) {
				s.UpdateSettings(func(settings *conf.Settings) {
					settings.Realtime.Audio.SoundLevel.Enabled = true
					settings.Realtime.Audio.SoundLevel.Debug = false
				})
			},
			action: func(s *HotReloadTestSuite) {
				s.UpdateSettings(func(settings *conf.Settings) {
					settings.Realtime.Audio.SoundLevel.Debug = true
				})
				s.SendControlSignal("reconfigure_sound_level")
			},
			verify: func(s *HotReloadTestSuite) {
				// Debug change doesn't produce a different notification
				// but should complete without error
				notification := s.ExpectNotification(2 * time.Second)
				assert.NotNil(t, notification)
			},
		},
	}
	
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			suite := NewHotReloadTestSuite(t)
			defer suite.Cleanup()
			
			// Setup
			scenario.setup(suite)
			
			// Action
			scenario.action(suite)
			
			// Verify
			scenario.verify(suite)
		})
	}
}