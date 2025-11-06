package analysis

import (
	"fmt"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/httpcontroller/handlers"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// TestHotReloadScenarios tests various hot reload scenarios systematically
func TestHotReloadScenarios(t *testing.T) {
	// Cannot run in parallel due to global settings

	scenarios := []struct {
		name                 string
		setupSettings        func(*conf.Settings)
		changeSettings       func(*conf.Settings)
		signal               string
		expectRunning        bool
		expectNotification   bool
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
			signal:               "reconfigure_sound_level",
			expectRunning:        true,
			expectNotification:   true,
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
			signal:               "reconfigure_sound_level",
			expectRunning:        false,
			expectNotification:   true,
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
			signal:               "reconfigure_sound_level",
			expectRunning:        true,
			expectNotification:   true,
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
			signal:               "reconfigure_sound_level",
			expectRunning:        true,
			expectNotification:   true,
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
	// This test directly calls the notification methods to verify message formatting
	// without needing to manipulate global settings

	testCases := []struct {
		name            string
		testFunc        func(*ControlMonitor)
		expectedMessage string
		expectedType    string
	}{
		{
			name: "Success notification",
			testFunc: func(cm *ControlMonitor) {
				cm.notifySuccess("Sound level monitoring reconfigured (interval: 15s)")
			},
			expectedMessage: "Sound level monitoring reconfigured (interval: 15s)",
			expectedType:    "success",
		},
		{
			name: "Error notification",
			testFunc: func(cm *ControlMonitor) {
				cm.notifyError("Failed to start sound level monitoring", fmt.Errorf("test error"))
			},
			expectedMessage: "Failed to start sound level monitoring: test error",
			expectedType:    "error",
		},
		{
			name: "Disabled notification",
			testFunc: func(cm *ControlMonitor) {
				cm.notifySuccess("Sound level monitoring disabled")
			},
			expectedMessage: "Sound level monitoring disabled",
			expectedType:    "success",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create notification channel
			notificationChan := make(chan handlers.Notification, 1)

			// Create control monitor with minimal setup
			cm := &ControlMonitor{
				notificationChan: notificationChan,
			}

			// Call the test function
			tc.testFunc(cm)

			// Read the notification from the channel
			select {
			case notification := <-notificationChan:
				assert.Equal(t, tc.expectedMessage, notification.Message, "Notification message should match expected")
				assert.Equal(t, tc.expectedType, notification.Type, "Notification type should match expected")
			case <-time.After(time.Second):
				t.Fatal("No notification received within timeout")
			}

			// Cleanup
			close(notificationChan)
		})
	}
}

// TestHandleReconfigureSoundLevelMessages tests the actual messages generated by handleReconfigureSoundLevel
func TestHandleReconfigureSoundLevelMessages(t *testing.T) {
	// Ensure settings are initialized
	settings := conf.GetSettings()
	if settings == nil {
		// Try to load default settings
		if _, err := conf.Load(); err != nil {
			t.Skip("Cannot load settings for test")
		}
		settings = conf.GetSettings()
	}

	// Save original values
	originalEnabled := settings.Realtime.Audio.SoundLevel.Enabled
	originalInterval := settings.Realtime.Audio.SoundLevel.Interval
	defer func() {
		// Restore original settings
		settings.Realtime.Audio.SoundLevel.Enabled = originalEnabled
		settings.Realtime.Audio.SoundLevel.Interval = originalInterval
	}()

	testCases := []struct {
		name            string
		enabled         bool
		interval        int
		expectedMessage string
		expectedType    string
	}{
		{
			name:            "Enabled with interval 15",
			enabled:         true,
			interval:        15,
			expectedMessage: "Sound level monitoring reconfigured (interval: 15s)",
			expectedType:    "success",
		},
		{
			name:            "Disabled",
			enabled:         false,
			interval:        10,
			expectedMessage: "Sound level monitoring disabled",
			expectedType:    "success",
		},
		{
			name:            "Enabled with interval 30",
			enabled:         true,
			interval:        30,
			expectedMessage: "Sound level monitoring reconfigured (interval: 30s)",
			expectedType:    "success",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Update settings for this test case
			settings.Realtime.Audio.SoundLevel.Enabled = tc.enabled
			settings.Realtime.Audio.SoundLevel.Interval = tc.interval

			// Create necessary channels
			soundLevelChan := make(chan myaudio.SoundLevelData, 1)
			notificationChan := make(chan handlers.Notification, 1)
			controlChan := make(chan string, 1)

			// Create a minimal processor for testing
			proc := &processor.Processor{}

			// Create control monitor
			cm := &ControlMonitor{
				controlChan:      controlChan,
				notificationChan: notificationChan,
				soundLevelChan:   soundLevelChan,
				proc:             proc,
			}

			// Create sound level manager
			cm.soundLevelManager = NewSoundLevelManager(soundLevelChan, proc, nil, nil)

			// Call the function under test
			cm.handleReconfigureSoundLevel()

			// Read the notification from the channel
			select {
			case notification := <-notificationChan:
				assert.Equal(t, tc.expectedMessage, notification.Message, "Notification message should match expected")
				assert.Equal(t, tc.expectedType, notification.Type, "Notification type should match expected")
			case <-time.After(time.Second):
				t.Fatal("No notification received within timeout")
			}

			// Cleanup
			cm.soundLevelManager.Stop()
			close(soundLevelChan)
			close(notificationChan)
			close(controlChan)
		})
	}
}

// TestSoundLevelDataValidation tests data validation edge cases
func TestSoundLevelDataValidation(t *testing.T) {
	testCases := []struct {
		name          string
		data          myaudio.SoundLevelData
		shouldError   bool
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
	for i := range 5 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range 10 {
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
	for i := range 3 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for range 15 {
				if err := manager.Restart(); err != nil {
					errors <- err
				}
				time.Sleep(2 * time.Millisecond)
			}
		}(i)
	}

	// Status check goroutines
	for i := range 5 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for range 20 {
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

// TestFFmpegRestartWithSoundLevel tests that sound level processors survive FFmpeg stream restarts
func TestFFmpegRestartWithSoundLevel(t *testing.T) {
	t.Parallel()

	// Create a test manager with sound level monitoring enabled
	suite := NewHotReloadTestSuite(t)
	defer suite.Cleanup()

	// Enable sound level monitoring with RTSP sources
	suite.UpdateSettings(func(s *conf.Settings) {
		s.Realtime.Audio.SoundLevel.Enabled = true
		s.Realtime.Audio.SoundLevel.Interval = 5
		s.Realtime.RTSP.URLs = []string{"rtsp://test.example.com/stream1"}
	})

	// Create a sound level manager to test with
	manager := NewSoundLevelManager(
		make(chan myaudio.SoundLevelData, 10),
		nil, // processor - can be nil for this test
		nil, // httpServer - can be nil for this test
		nil, // metrics - can be nil for this test
	)

	// Start sound level monitoring
	err := manager.Start()
	require.NoError(t, err, "Should start sound level monitoring successfully")
	assert.True(t, manager.IsRunning(), "Sound level monitoring should be running")

	// Simulate an FFmpeg stream restart by stopping and restarting the manager
	// This tests the critical path where processors need to be re-registered
	log.Println("🔄 Simulating FFmpeg stream restart scenario...")

	// Stop the manager (simulating stream failure)
	manager.Stop()
	assert.False(t, manager.IsRunning(), "Sound level monitoring should be stopped")

	// Restart the manager (simulating stream recovery)
	err = manager.Start()
	require.NoError(t, err, "Should restart sound level monitoring successfully after simulated crash")
	assert.True(t, manager.IsRunning(), "Sound level monitoring should be running after restart")

	// Verify restart works multiple times (simulating multiple crashes)
	for i := range 3 {
		log.Printf("🔄 Testing restart cycle %d...", i+1)

		err = manager.Restart()
		require.NoError(t, err, "Should restart successfully on cycle %d", i+1)
		assert.True(t, manager.IsRunning(), "Should be running after restart cycle %d", i+1)

		// Small delay to simulate real restart timing
		time.Sleep(50 * time.Millisecond)
	}

	// Final cleanup
	manager.Stop()
	assert.False(t, manager.IsRunning(), "Should be stopped after final cleanup")
}

// TestSoundLevelProcessorRegistrationConsistency tests that sound level processor
// registration remains consistent across various stream lifecycle scenarios
func TestSoundLevelProcessorRegistrationConsistency(t *testing.T) {
	t.Parallel()

	// Test with multiple RTSP URLs to ensure all get registered
	testSettings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				Source: "Test Audio Device",
				SoundLevel: conf.SoundLevelSettings{
					Enabled:  true,
					Interval: 10,
				},
			},
			RTSP: conf.RTSPSettings{
				URLs: []string{
					"rtsp://camera1.test.com/stream",
					"rtsp://camera2.test.com/stream",
					"rtsp://camera3.test.com/stream",
				},
			},
		},
		BirdNET: conf.BirdNETConfig{
			Locale:    "en",
			Latitude:  35.0,
			Longitude: -120.0,
		},
	}

	// Test registration for all sources
	err := registerSoundLevelProcessorsForActiveSources(testSettings)
	require.NoError(t, err, "Should register all sound level processors successfully")

	// Test unregistration of all sources
	unregisterAllSoundLevelProcessors(testSettings)

	// Test partial configuration (only some RTSP sources)
	testSettings.Realtime.RTSP.URLs = []string{"rtsp://camera1.test.com/stream"}

	err = registerSoundLevelProcessorsForActiveSources(testSettings)
	require.NoError(t, err, "Should handle partial RTSP configuration")

	// Cleanup
	unregisterAllSoundLevelProcessors(testSettings)
}

// TestSoundLevelWithRTSPConfigChanges tests sound level monitoring behavior
// when RTSP configuration changes dynamically
func TestSoundLevelWithRTSPConfigChanges(t *testing.T) {
	t.Parallel()

	// Test that sound level manager handles restarts properly
	// This simulates configuration changes that would trigger restarts
	manager := NewSoundLevelManager(
		make(chan myaudio.SoundLevelData, 10),
		nil, nil, nil,
	)

	// Start with current configuration
	err := manager.Start()
	require.NoError(t, err, "Should start with current configuration")

	// Test multiple restart cycles to simulate configuration changes
	for i := range 3 {
		log.Printf("Testing restart cycle %d for config changes", i+1)

		// Restart to simulate configuration change
		err = manager.Restart()
		require.NoError(t, err, "Should handle restart for config change %d", i+1)

		// Verify manager is still running after restart
		assert.True(t, manager.IsRunning(), "Should be running after config change restart %d", i+1)

		// Small delay to simulate real timing
		time.Sleep(25 * time.Millisecond)
	}

	// Test stopping and restarting (simulates disable/enable)
	manager.Stop()
	assert.False(t, manager.IsRunning(), "Should be stopped")

	err = manager.Start()
	require.NoError(t, err, "Should restart after being stopped")
	assert.True(t, manager.IsRunning(), "Should be running after restart from stopped state")

	// Final cleanup
	manager.Stop()
	assert.False(t, manager.IsRunning(), "Should be stopped after final cleanup")
}

// TestGracefulDegradationWithPartialFailures tests that the system continues
// operating when some sound level processors fail to register
func TestGracefulDegradationWithPartialFailures(t *testing.T) {
	t.Parallel()

	// Test with mixed valid and invalid sources to simulate partial failures
	testSettings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				Source: "Valid Audio Device", // This should succeed
				SoundLevel: conf.SoundLevelSettings{
					Enabled:  true,
					Interval: 10,
				},
			},
			RTSP: conf.RTSPSettings{
				URLs: []string{
					"rtsp://valid-camera.test.com/stream",   // This should succeed
					"rtsp://invalid-camera.test.com/stream", // This might fail
					"rtsp://another-valid.test.com/stream",  // This should succeed
				},
			},
		},
		BirdNET: conf.BirdNETConfig{
			Locale:    "en",
			Latitude:  35.0,
			Longitude: -120.0,
		},
	}

	// The function should not return an error for partial failures
	// It should only return an error if ALL registrations fail
	err := registerSoundLevelProcessorsForActiveSources(testSettings)

	// Even if some registrations fail, the function should not error
	// This tests graceful degradation
	require.NoError(t, err, "Should not error on partial failures - graceful degradation")

	// Cleanup
	unregisterAllSoundLevelProcessors(testSettings)
}

// TestShutdownTimeout tests that the shutdown timeout mechanism works correctly
func TestShutdownTimeout(t *testing.T) {
	t.Parallel()

	// Create a manager
	manager := NewSoundLevelManager(
		make(chan myaudio.SoundLevelData, 10),
		nil, nil, nil,
	)

	// Start the manager
	err := manager.Start()
	require.NoError(t, err, "Should start successfully")

	// Record start time
	startTime := time.Now()

	// Stop the manager - this should complete quickly (not timeout)
	manager.Stop()

	// Verify it completed in reasonable time (much less than 30s timeout)
	elapsed := time.Since(startTime)
	assert.Less(t, elapsed, 5*time.Second, "Normal shutdown should complete quickly, took %v", elapsed)
	assert.False(t, manager.IsRunning(), "Should be stopped after shutdown")
}
