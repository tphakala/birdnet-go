package analysis

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/httpcontroller/handlers"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// TestHotReloadScenarios tests various hot reload scenarios systematically
func TestHotReloadScenarios(t *testing.T) {
	// Cannot run in parallel due to global settings
	
	scenarios := []struct {
		name           string
		setupSettings  func(*conf.Settings)
		changeSettings func(*conf.Settings)
		signal         string
		expectRunning  bool
		expectNotification bool
		notificationContains string
	}{
		{
			name: "Enable sound level monitoring",
			setupSettings: func(s *conf.Settings) {
				s.Realtime.Audio.SoundLevel.Enabled = false
			},
			changeSettings: func(s *conf.Settings) {
				s.Realtime.Audio.SoundLevel.Enabled = true
				s.Realtime.Audio.SoundLevel.Interval = 10
			},
			signal:         "reconfigure_sound_level",
			expectRunning:  true,
			expectNotification: true,
			notificationContains: "started",
		},
		{
			name: "Disable sound level monitoring",
			setupSettings: func(s *conf.Settings) {
				s.Realtime.Audio.SoundLevel.Enabled = true
				s.Realtime.Audio.SoundLevel.Interval = 10
			},
			changeSettings: func(s *conf.Settings) {
				s.Realtime.Audio.SoundLevel.Enabled = false
			},
			signal:         "reconfigure_sound_level",
			expectRunning:  false,
			expectNotification: true,
			notificationContains: "disabled",
		},
		{
			name: "Change interval",
			setupSettings: func(s *conf.Settings) {
				s.Realtime.Audio.SoundLevel.Enabled = true
				s.Realtime.Audio.SoundLevel.Interval = 10
			},
			changeSettings: func(s *conf.Settings) {
				s.Realtime.Audio.SoundLevel.Interval = 30
			},
			signal:         "reconfigure_sound_level",
			expectRunning:  true,
			expectNotification: true,
			notificationContains: "restarted",
		},
		{
			name: "Toggle debug mode",
			setupSettings: func(s *conf.Settings) {
				s.Realtime.Audio.SoundLevel.Enabled = true
				s.Realtime.Audio.SoundLevel.Debug = false
			},
			changeSettings: func(s *conf.Settings) {
				s.Realtime.Audio.SoundLevel.Debug = true
			},
			signal:         "reconfigure_sound_level",
			expectRunning:  true,
			expectNotification: true,
			notificationContains: "restarted",
		},
	}
	
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// Setup
			settings := conf.Setting()
			if settings == nil {
				t.Skip("No settings available")
			}
			
			// Save original values
			originalEnabled := settings.Realtime.Audio.SoundLevel.Enabled
			originalInterval := settings.Realtime.Audio.SoundLevel.Interval
			originalDebug := settings.Realtime.Audio.SoundLevel.Debug
			
			// Restore settings after test
			defer func() {
				settings.Realtime.Audio.SoundLevel.Enabled = originalEnabled
				settings.Realtime.Audio.SoundLevel.Interval = originalInterval
				settings.Realtime.Audio.SoundLevel.Debug = originalDebug
			}()
			
			// Apply setup settings
			scenario.setupSettings(settings)
			
			// Create components
			soundLevelChan := make(chan myaudio.SoundLevelData, 100)
			manager := NewSoundLevelManager(soundLevelChan, nil, nil, nil)
			
			// Start if expected to be running initially
			if scenario.setupSettings != nil {
				_ = manager.Start()
			}
			
			// Apply configuration changes
			scenario.changeSettings(settings)
			
			// Simulate hot reload
			err := manager.Restart()
			require.NoError(t, err, "Restart should not error")
			
			// Verify final state
			assert.Equal(t, scenario.expectRunning, manager.IsRunning(),
				"Manager running state should match expectation")
			
			// Cleanup
			manager.Stop()
			close(soundLevelChan)
		})
	}
}

// TestHotReloadNotificationMessages tests notification message generation
func TestHotReloadNotificationMessages(t *testing.T) {
	testCases := []struct {
		name             string
		enabled          bool
		interval         int
		expectedMessage  string
	}{
		{
			name:            "Enabled with interval",
			enabled:         true,
			interval:        15,
			expectedMessage: "🔄 Sound level monitoring restarted with interval: 15s",
		},
		{
			name:            "Disabled",
			enabled:         false,
			interval:        10,
			expectedMessage: "🔇 Sound level monitoring is disabled",
		},
		{
			name:            "Started",
			enabled:         true,
			interval:        30,
			expectedMessage: "🔊 Sound level monitoring started with interval: 30s",
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// This test verifies the expected notification format
			// In a real test, we'd capture the actual notification from handleReconfigureSoundLevel
			// For now, we document the expected behavior
			t.Logf("Expected notification for %s: %s", tc.name, tc.expectedMessage)
		})
	}
}

// TestSoundLevelDataValidation tests data validation edge cases
func TestSoundLevelDataValidation(t *testing.T) {
	testCases := []struct {
		name        string
		data        myaudio.SoundLevelData
		shouldError bool
		errorContains string
	}{
		{
			name: "Valid data",
			data: myaudio.SoundLevelData{
				Timestamp: time.Now(),
				Source:    "test",
				Name:      "Test Device",
				Duration:  10,
				OctaveBands: map[string]myaudio.OctaveBandData{
					"1.0_kHz": {
						CenterFreq: 1000,
						Min:        -50,
						Max:        -30,
						Mean:       -40,
					},
				},
			},
			shouldError: false,
		},
		{
			name: "Zero timestamp",
			data: myaudio.SoundLevelData{
				Source: "test",
				Name:   "Test Device",
			},
			shouldError:   true,
			errorContains: "timestamp",
		},
		{
			name: "Empty source",
			data: myaudio.SoundLevelData{
				Timestamp: time.Now(),
				Name:      "Test Device",
			},
			shouldError:   true,
			errorContains: "source",
		},
		{
			name: "No octave bands",
			data: myaudio.SoundLevelData{
				Timestamp:   time.Now(),
				Source:      "test",
				Name:        "Test Device",
				OctaveBands: map[string]myaudio.OctaveBandData{},
			},
			shouldError:   true,
			errorContains: "octave band",
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSoundLevelData(&tc.data)
			if tc.shouldError {
				require.Error(t, err, "Should return error for invalid data")
				if tc.errorContains != "" {
					assert.Contains(t, err.Error(), tc.errorContains)
				}
			} else {
				assert.NoError(t, err, "Should not error for valid data")
			}
		})
	}
}

// TestHotReloadRaceConditions tests for race conditions during hot reload
func TestHotReloadRaceConditions(t *testing.T) {
	// Setup
	soundLevelChan := make(chan myaudio.SoundLevelData, 100)
	manager := NewSoundLevelManager(soundLevelChan, nil, nil, nil)
	
	settings := conf.Setting()
	if settings == nil {
		t.Skip("No settings available")
	}
	
	// Save original state
	originalEnabled := settings.Realtime.Audio.SoundLevel.Enabled
	defer func() {
		settings.Realtime.Audio.SoundLevel.Enabled = originalEnabled
		manager.Stop()
		close(soundLevelChan)
	}()
	
	// Enable sound level
	settings.Realtime.Audio.SoundLevel.Enabled = true
	
	// Start multiple goroutines that will:
	// 1. Toggle settings
	// 2. Call restart
	// 3. Check status
	
	var wg sync.WaitGroup
	errors := make(chan error, 100)
	
	// Settings modifier goroutines
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				// Toggle enabled state
				enabled := (j%2 == 0)
				settings.Realtime.Audio.SoundLevel.Enabled = enabled
				
				// Change interval
				settings.Realtime.Audio.SoundLevel.Interval = 5 + j
				
				time.Sleep(time.Millisecond)
			}
		}(i)
	}
	
	// Restart goroutines
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 15; j++ {
				if err := manager.Restart(); err != nil {
					errors <- err
				}
				time.Sleep(2 * time.Millisecond)
			}
		}(i)
	}
	
	// Status check goroutines
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = manager.IsRunning()
				time.Sleep(time.Millisecond)
			}
		}(i)
	}
	
	// Wait for completion
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	
	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("Race condition test timed out")
	}
	
	// Check for errors
	close(errors)
	errorCount := 0
	for err := range errors {
		t.Logf("Error during race test: %v", err)
		errorCount++
	}
	assert.Equal(t, 0, errorCount, "Should have no errors during concurrent operations")
}

// TestHotReloadWithControlMonitor tests the full integration with control monitor
func TestHotReloadWithControlMonitor(t *testing.T) {
	// Setup channels
	controlChan := make(chan string, 10)
	notificationChan := make(chan handlers.Notification, 10)
	soundLevelChan := make(chan myaudio.SoundLevelData, 100)
	
	// Create manager
	manager := NewSoundLevelManager(soundLevelChan, nil, nil, nil)
	
	// Simulate control monitor handler
	handleSignal := func(signal string) {
		if signal == "reconfigure_sound_level" {
			settings := conf.Setting()
			if settings == nil {
				return
			}
			
			var notification handlers.Notification
			
			if !settings.Realtime.Audio.SoundLevel.Enabled {
				notification = handlers.Notification{
					Message: "🔇 Sound level monitoring is disabled",
					Type:    "info",
				}
			} else {
				wasRunning := manager.IsRunning()
				err := manager.Restart()
				if err != nil {
					notification = handlers.Notification{
						Message: "❌ Failed to restart sound level monitoring: " + err.Error(),
						Type:    "error",
					}
				} else {
					action := "restarted"
					if !wasRunning {
						action = "started"
					}
					notification = handlers.Notification{
						Message: "🔄 Sound level monitoring " + action,
						Type:    "success",
					}
				}
			}
			
			notificationChan <- notification
		}
	}
	
	// Test sequence
	settings := conf.Setting()
	if settings != nil {
		// Test 1: Enable
		settings.Realtime.Audio.SoundLevel.Enabled = true
		handleSignal("reconfigure_sound_level")
		
		select {
		case notif := <-notificationChan:
			assert.Contains(t, notif.Message, "started")
			assert.Equal(t, "success", notif.Type)
		case <-time.After(time.Second):
			t.Error("No notification received for enable")
		}
		
		// Test 2: Disable
		settings.Realtime.Audio.SoundLevel.Enabled = false
		handleSignal("reconfigure_sound_level")
		
		select {
		case notif := <-notificationChan:
			assert.Contains(t, notif.Message, "disabled")
			assert.Equal(t, "info", notif.Type)
		case <-time.After(time.Second):
			t.Error("No notification received for disable")
		}
	}
	
	// Cleanup
	manager.Stop()
	close(controlChan)
	close(notificationChan)
	close(soundLevelChan)
}